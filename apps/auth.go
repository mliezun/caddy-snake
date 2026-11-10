package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/mliezun/caddy-snake/apps/db/dbgen"
)

const (
	sessionCookie   = "caddysnake_apps_session"
	sessionLifetime = 30 * 24 * time.Hour
	accessKeyPrefix = "csk_"
	minPasswordLen  = 8
)

// Identity is a signed-in person.
type Identity struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

var (
	usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

	errBadLogin = errors.New("wrong username or password")
)

// argon2id parameters (OWASP baseline: 19 MiB, 2 passes, 1 lane).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func hashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", argonTime, argonMemory, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	var t, m uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[1]+" "+parts[2]+" "+parts[3], "%d %d %d", &t, &m, &threads); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash keeps login timing the same for unknown usernames.
var dummyHash = hashPassword("not a real password")

func newToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is how session tokens and access keys are stored: they are long
// random strings, so a plain SHA-256 is enough and lets us look them up.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validateCredentials(username, password string) error {
	if !usernameRE.MatchString(username) {
		return errors.New("usernames are 1-40 lowercase letters, digits, dots, hyphens or underscores, starting with a letter or digit")
	}
	if len(password) < minPasswordLen {
		return fmt.Errorf("passwords need at least %d characters", minPasswordLen)
	}
	return nil
}

// AddUser creates a person who can sign in.
func (p *Platform) AddUser(ctx context.Context, username, password string, admin bool) error {
	if err := validateCredentials(username, password); err != nil {
		return err
	}
	if _, err := p.store.q.GetUser(ctx, username); err == nil {
		return fmt.Errorf("user %q already exists", username)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return p.store.q.CreateUser(ctx, dbgen.CreateUserParams{Username: username, PasswordHash: hashPassword(password), IsAdmin: admin})
}

// SetPassword changes a person's password and signs them out everywhere.
func (p *Platform) SetPassword(ctx context.Context, username, password string) error {
	if err := validateCredentials(username, password); err != nil {
		return err
	}
	n, err := p.store.q.SetUserPassword(ctx, dbgen.SetUserPasswordParams{Username: username, PasswordHash: hashPassword(password)})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("there is no user %q", username)
	}
	return p.store.q.DeleteUserSessions(ctx, username)
}

// RemoveUser deletes a person together with their sessions and access keys.
func (p *Platform) RemoveUser(ctx context.Context, username string) error {
	n, err := p.store.q.DeleteUser(ctx, username)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("there is no user %q", username)
	}
	return nil
}

// Users lists everyone who can sign in.
func (p *Platform) Users(ctx context.Context) ([]Identity, error) {
	rows, err := p.store.q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(rows))
	for _, row := range rows {
		out = append(out, Identity{Username: row.Username, Admin: row.IsAdmin})
	}
	return out, nil
}

// Login checks a password and starts a browser session.
func (p *Platform) Login(ctx context.Context, username, password string) (token string, expires time.Time, err error) {
	user, err := p.store.q.GetUser(ctx, strings.ToLower(strings.TrimSpace(username)))
	if errors.Is(err, sql.ErrNoRows) {
		verifyPassword(dummyHash, password)
		return "", time.Time{}, errBadLogin
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if !verifyPassword(user.PasswordHash, password) {
		return "", time.Time{}, errBadLogin
	}
	token = newToken("")
	expires = time.Now().Add(sessionLifetime)
	err = p.store.q.CreateSession(ctx, dbgen.CreateSessionParams{TokenHash: hashToken(token), Username: user.Username, ExpiresAt: expires})
	return token, expires, err
}

// Logout ends a browser session.
func (p *Platform) Logout(ctx context.Context, token string) {
	_ = p.store.q.DeleteSession(ctx, hashToken(token))
}

// SessionIdentity returns who a session token belongs to, or nil.
func (p *Platform) SessionIdentity(ctx context.Context, token string) *Identity {
	if token == "" {
		return nil
	}
	row, err := p.store.q.GetSessionUser(ctx, hashToken(token))
	if err != nil {
		return nil
	}
	return &Identity{Username: row.Username, Admin: row.IsAdmin}
}

// CreateAccessKey makes a key an agent or the CLI uses in place of a password.
// The key is shown once and only its hash is stored.
func (p *Platform) CreateAccessKey(ctx context.Context, username, name string) (string, error) {
	if len([]rune(name)) > maxKeyNameLen {
		return "", fmt.Errorf("the key's name can be at most %d characters", maxKeyNameLen)
	}
	if _, err := p.store.q.GetUser(ctx, username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("there is no user %q", username)
		}
		return "", err
	}
	key := newToken(accessKeyPrefix)
	err := p.store.q.CreateAccessKey(ctx, dbgen.CreateAccessKeyParams{TokenHash: hashToken(key), Username: username, Name: name})
	return key, err
}

// AccessKey describes a key without revealing it.
type AccessKey struct {
	Name      string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// AccessKeys lists a person's access keys.
func (p *Platform) AccessKeys(ctx context.Context, username string) ([]AccessKey, error) {
	rows, err := p.store.q.ListAccessKeys(ctx, username)
	if err != nil {
		return nil, err
	}
	out := make([]AccessKey, 0, len(rows))
	for _, row := range rows {
		k := AccessKey{Name: row.Name, CreatedAt: row.CreatedAt}
		if row.LastUsedAt.Valid {
			t := row.LastUsedAt.Time
			k.LastUsed = &t
		}
		out = append(out, k)
	}
	return out, nil
}

// AdminCount returns how many administrators exist.
func (p *Platform) AdminCount(ctx context.Context) (int64, error) {
	return p.store.q.CountAdmins(ctx)
}

// RevokeAccessKeys removes a person's keys with the given name.
func (p *Platform) RevokeAccessKeys(ctx context.Context, username, name string) (int64, error) {
	return p.store.q.DeleteAccessKeysByName(ctx, dbgen.DeleteAccessKeysByNameParams{Username: username, Name: name})
}

// KeyIdentity returns who an access key belongs to, or nil.
func (p *Platform) KeyIdentity(ctx context.Context, key string) *Identity {
	if !strings.HasPrefix(key, accessKeyPrefix) {
		return nil
	}
	hash := hashToken(key)
	row, err := p.store.q.GetAccessKeyUser(ctx, hash)
	if err != nil {
		return nil
	}
	_ = p.store.q.TouchAccessKey(ctx, hash)
	return &Identity{Username: row.Username, Admin: row.IsAdmin}
}
