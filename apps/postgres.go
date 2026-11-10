package apps

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"go.uber.org/zap"

	"github.com/mliezun/caddy-snake/apps/db/dbgen"
)

const (
	managedSuperuser  = "caddysnake"
	platformDatabase  = "caddysnake"
	managedMaxConns   = "300"
	pgStartTimeoutSec = "60"
)

// errNoCloneTools means pg_dump/psql were not found, so a database cannot be copied.
var errNoCloneTools = errors.New("pg_dump and psql were not found, so data cannot be copied")

// postgres is the database server behind the platform: either a private
// instance this process starts and stops ("managed"), or one the operator
// pointed us at with a connection URL.
type postgres struct {
	managed  bool
	started  bool     // we started the built-in instance, so we stop it on Close
	binDir   string   // directory holding initdb, pg_ctl, pg_dump, psql; "" if unknown
	clusters string   // data root (managed only)
	base     *url.URL // admin connection to the platform database
	admin    *sql.DB
	logger   *zap.Logger
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// findPostgresBin locates the directory that holds the Postgres programs.
func findPostgresBin() string {
	initdb := exeName("initdb")
	// PGBIN is set where Postgres is installed but kept off PATH (CI images).
	if dir := os.Getenv("PGBIN"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, initdb)); err == nil {
			return dir
		}
	}
	if p, err := exec.LookPath("pg_ctl"); err == nil {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			// Debian/Ubuntu put a wrapper on PATH; the real programs sit elsewhere.
			if _, err := os.Stat(filepath.Join(filepath.Dir(resolved), initdb)); err == nil {
				return filepath.Dir(resolved)
			}
		}
		if _, err := exec.LookPath("initdb"); err == nil {
			return filepath.Dir(p)
		}
	}
	var found []string
	for _, pattern := range postgresBinPatterns(runtime.GOOS) {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join(m, initdb)); err == nil {
				found = append(found, m)
			}
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return found[len(found)-1]
}

func (p *postgres) tool(name string) string {
	if p.binDir != "" {
		candidate := filepath.Join(p.binDir, exeName(name))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	return ""
}

func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func runTool(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

const installHint = "Postgres is not installed. Install it (macOS: `brew install postgresql@18`; Debian/Ubuntu: `sudo apt install postgresql`; Fedora/Rocky: `sudo dnf install postgresql-server`; Windows: the installer from postgresql.org) or pass --database-url to use an existing server"

// runDetached runs a program that leaves another one running behind it
// (pg_ctl start). Its output goes to a file, not a pipe: the program left
// running keeps a pipe open, and waiting for the pipe to close would never end.
func runDetached(ctx context.Context, name string, args ...string) error {
	out, err := os.CreateTemp("", "caddysnake-*.out")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		said, _ := os.ReadFile(out.Name())
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(said)))
	}
	return nil
}

// startManagedPostgres initialises (first run) and starts a private Postgres
// instance under dataDir. It listens on 127.0.0.1 only, with a generated
// password, and is stopped again by Close.
func startManagedPostgres(ctx context.Context, dataDir string, logger *zap.Logger) (*postgres, error) {
	bin := findPostgresBin()
	if bin == "" {
		return nil, errors.New(installHint)
	}
	if os.Geteuid() == 0 {
		return nil, errors.New("the built-in Postgres cannot run as root; run as a regular user or pass --database-url")
	}
	p := &postgres{managed: true, binDir: bin, clusters: dataDir, logger: logger}
	cluster := filepath.Join(dataDir, "postgres")
	pwFile := filepath.Join(dataDir, "postgres.password")
	portFile := filepath.Join(dataDir, "postgres.port")

	if _, err := os.Stat(filepath.Join(cluster, "PG_VERSION")); err != nil {
		if err := os.WriteFile(pwFile, []byte(randomHex(24)+"\n"), 0o600); err != nil {
			return nil, err
		}
		port, err := freeLocalPort()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
			return nil, err
		}
		logger.Info("creating the built-in Postgres database", zap.String("dir", cluster))
		if _, err := runTool(ctx, nil, filepath.Join(bin, "initdb"),
			"-D", cluster, "-U", managedSuperuser, "--auth=scram-sha-256",
			"--pwfile="+pwFile, "-E", "UTF8", "--locale=C"); err != nil {
			_ = os.RemoveAll(cluster)
			return nil, fmt.Errorf("creating the built-in Postgres database: %w", err)
		}
	}
	pwBytes, err := os.ReadFile(pwFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", pwFile, err)
	}
	portBytes, err := os.ReadFile(portFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", portFile, err)
	}
	password := strings.TrimSpace(string(pwBytes))
	port := strings.TrimSpace(string(portBytes))

	pgCtl := filepath.Join(bin, "pg_ctl")
	if _, err := runTool(ctx, nil, pgCtl, "-D", cluster, "status"); err != nil {
		opts := fmt.Sprintf("-p %s -c listen_addresses=127.0.0.1 -c max_connections=%s", port, managedMaxConns)
		if runtime.GOOS != "windows" {
			// Network connections on this computer only. Windows has no socket
			// files by default, and its command line would not pass '' through.
			opts += " -c unix_socket_directories=''"
		}
		if err := runDetached(ctx, pgCtl, "-D", cluster, "-l", filepath.Join(dataDir, "postgres.log"),
			"-w", "-t", pgStartTimeoutSec, "-o", opts, "start"); err != nil {
			return nil, fmt.Errorf("starting the built-in Postgres: %w (see %s)", err, filepath.Join(dataDir, "postgres.log"))
		}
		p.started = true
		logger.Info("built-in Postgres started", zap.String("port", port))
	}

	root := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(managedSuperuser, password),
		Host:   net.JoinHostPort("127.0.0.1", port),
		Path:   "/postgres",
	}
	bootstrap, err := sql.Open("pgx", root.String())
	if err != nil {
		return nil, err
	}
	defer bootstrap.Close()
	if err := waitForDB(ctx, bootstrap); err != nil {
		return nil, fmt.Errorf("connecting to the built-in Postgres: %w", err)
	}
	exists, err := dbgen.New(bootstrap).DatabaseExists(ctx, platformDatabase)
	if err != nil {
		return nil, err
	}
	if !exists {
		if _, err := bootstrap.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(platformDatabase)); err != nil {
			return nil, err
		}
	}
	base := *root
	base.Path = "/" + platformDatabase
	p.base = &base
	return p, p.open(ctx)
}

// connectPostgres uses an existing server. The URL's user must be allowed to
// create roles and databases.
func connectPostgres(ctx context.Context, databaseURL string, logger *zap.Logger) (*postgres, error) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return nil, fmt.Errorf("--database-url must look like postgresql://user:password@host:5432/dbname")
	}
	p := &postgres{binDir: findPostgresBin(), base: u, logger: logger}
	return p, p.open(ctx)
}

func waitForDB(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (p *postgres) open(ctx context.Context) error {
	db, err := sql.Open("pgx", p.base.String())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(8)
	if err := waitForDB(ctx, db); err != nil {
		db.Close()
		return fmt.Errorf("connecting to Postgres at %s: %w", p.base.Host, err)
	}
	p.admin = db
	return nil
}

// Close closes connections and stops the built-in instance if we started one.
func (p *postgres) Close() {
	if p == nil {
		return
	}
	if p.admin != nil {
		_ = p.admin.Close()
	}
	// Only stop an instance this process started: a command-line tool that
	// found the server's database already running must leave it running.
	if p.managed && p.started {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cluster := filepath.Join(p.clusters, "postgres")
		if _, err := runTool(ctx, nil, filepath.Join(p.binDir, "pg_ctl"), "-D", cluster, "-m", "fast", "-w", "stop"); err != nil {
			p.logger.Warn("stopping the built-in Postgres", zap.Error(err))
		}
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// urlFor builds a connection URL to database db on the same server.
func (p *postgres) urlFor(user, password, db string) string {
	u := *p.base
	u.User = url.UserPassword(user, password)
	u.Path = "/" + db
	return u.String()
}

// adminURLFor is the admin connection to another database on the server.
func (p *postgres) adminURLFor(db string) *url.URL {
	u := *p.base
	u.Path = "/" + db
	return &u
}

// CreateAppDatabase creates the role and database for a serving slug and
// returns the connection URL the app should use. Calling it again for an
// existing database rotates the password.
func (p *postgres) CreateAppDatabase(ctx context.Context, slug string) (string, error) {
	name := dbName(slug)
	password := randomHex(20)
	catalog := dbgen.New(p.admin)
	roleExists, err := catalog.RoleExists(ctx, name)
	if err != nil {
		return "", err
	}
	verb := "CREATE"
	if roleExists {
		verb = "ALTER"
	}
	// password is hex we generated, so it is safe inside the literal.
	if _, err := p.admin.ExecContext(ctx, fmt.Sprintf("%s ROLE %s LOGIN PASSWORD '%s'", verb, quoteIdent(name), password)); err != nil {
		return "", fmt.Errorf("creating database user: %w", err)
	}
	// Needed to make the role an owner when we are not a superuser.
	_, _ = p.admin.ExecContext(ctx, fmt.Sprintf("GRANT %s TO CURRENT_USER", quoteIdent(name)))
	dbExists, err := catalog.DatabaseExists(ctx, name)
	if err != nil {
		return "", err
	}
	if !dbExists {
		if _, err := p.admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", quoteIdent(name), quoteIdent(name))); err != nil {
			return "", fmt.Errorf("creating database: %w", err)
		}
		// Other apps' roles must not be able to connect to this database.
		if _, err := p.admin.ExecContext(ctx, fmt.Sprintf("REVOKE CONNECT ON DATABASE %s FROM PUBLIC", quoteIdent(name))); err != nil {
			return "", err
		}
	}
	return p.urlFor(name, password, name), nil
}

// DropAppDatabase removes the database and role for a serving slug.
func (p *postgres) DropAppDatabase(ctx context.Context, slug string) error {
	name := dbName(slug)
	if _, err := p.admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(name))); err != nil {
		return fmt.Errorf("removing database: %w", err)
	}
	if _, err := p.admin.ExecContext(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", quoteIdent(name))); err != nil {
		return fmt.Errorf("removing database user: %w", err)
	}
	return nil
}

func splitPassword(u *url.URL) (noPassword string, env []string) {
	c := *u
	pw, _ := c.User.Password()
	c.User = url.User(c.User.Username())
	return c.String(), []string{"PGPASSWORD=" + pw}
}

// CloneDatabase copies everything in src's database into dst's (which must
// already exist and be empty), so a draft starts with the live app's data.
func (p *postgres) CloneDatabase(ctx context.Context, srcSlug, dstSlug, dstURL string) error {
	pgDump, psql := p.tool("pg_dump"), p.tool("psql")
	if pgDump == "" || psql == "" {
		return errNoCloneTools
	}
	srcURL, srcEnv := splitPassword(p.adminURLFor(dbName(srcSlug)))
	dump, err := runTool(ctx, srcEnv, pgDump, "--no-owner", "--no-acl", "--dbname="+srcURL)
	if err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	parsed, err := url.Parse(dstURL)
	if err != nil {
		return err
	}
	dst, dstEnv := splitPassword(parsed)
	cmd := exec.CommandContext(ctx, psql, "--quiet", "--set", "ON_ERROR_STOP=1", "--dbname="+dst)
	cmd.Env = append(os.Environ(), dstEnv...)
	cmd.Stdin = strings.NewReader(dump)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying data: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// openApp opens a short-lived connection pool to an app's database.
func openAppDB(ctx context.Context, appURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", appURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
