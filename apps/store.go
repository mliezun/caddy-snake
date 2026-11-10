package apps

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mliezun/caddy-snake/apps/db"
	"github.com/mliezun/caddy-snake/apps/db/dbgen"
)

// App is one served thing: a live app, or a draft of one (Parent set).
type App struct {
	Slug        string     `json:"slug"`
	Parent      string     `json:"parent,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	DatabaseURL string     `json:"-"`
	LiveVersion string     `json:"live_version,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	LastUsedAt  time.Time  `json:"last_used_at"`
}

// IsDraft reports whether this is a draft of another app.
func (a *App) IsDraft() bool { return a.Parent != "" }

var errNotFound = errors.New("not found")

// store is the platform's record keeping. All SQL lives in db/queries and is
// compiled by sqlc; this type only converts between generated rows and the
// types the rest of the package uses.
type store struct {
	conn *sql.DB
	q    *dbgen.Queries
}

func newStore(conn *sql.DB) *store { return &store{conn: conn, q: dbgen.New(conn)} }

func (s *store) migrate(ctx context.Context) error {
	_, err := db.Migrate(ctx, s.conn)
	return err
}

func toApp(row dbgen.CaddysnakeAppsApp) *App {
	a := &App{
		Slug:        row.Slug,
		Parent:      row.Parent.String,
		Name:        row.Name,
		Description: row.Description,
		DatabaseURL: row.DatabaseUrl,
		LiveVersion: row.LiveVersion,
		CreatedBy:   row.CreatedBy,
		CreatedAt:   row.CreatedAt,
		LastUsedAt:  row.LastUsedAt,
	}
	if row.PublishedAt.Valid {
		t := row.PublishedAt.Time
		a.PublishedAt = &t
	}
	return a
}

func toApps(rows []dbgen.CaddysnakeAppsApp, err error) ([]*App, error) {
	if err != nil {
		return nil, err
	}
	out := make([]*App, 0, len(rows))
	for _, row := range rows {
		out = append(out, toApp(row))
	}
	return out, nil
}

func (s *store) GetApp(ctx context.Context, slug string) (*App, error) {
	row, err := s.q.GetApp(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return toApp(row), nil
}

// ListApps returns live apps (not drafts).
func (s *store) ListApps(ctx context.Context) ([]*App, error) {
	return toApps(s.q.ListLiveApps(ctx))
}

// ListAll returns apps and drafts.
func (s *store) ListAll(ctx context.Context) ([]*App, error) { return toApps(s.q.ListAllApps(ctx)) }

// ListDrafts returns the drafts of one app.
func (s *store) ListDrafts(ctx context.Context, parent string) ([]*App, error) {
	return toApps(s.q.ListDrafts(ctx, parent))
}

func (s *store) InsertApp(ctx context.Context, a *App) error {
	return s.q.InsertApp(ctx, dbgen.InsertAppParams{
		Slug:        a.Slug,
		Parent:      sql.NullString{String: a.Parent, Valid: a.Parent != ""},
		Name:        a.Name,
		Description: a.Description,
		DatabaseUrl: a.DatabaseURL,
		CreatedBy:   a.CreatedBy,
	})
}

func (s *store) DeleteApp(ctx context.Context, slug string) error {
	if err := s.q.DeleteSettings(ctx, slug); err != nil {
		return err
	}
	if err := s.q.DeletePublishes(ctx, slug); err != nil {
		return err
	}
	return s.q.DeleteApp(ctx, slug)
}

func (s *store) SetLive(ctx context.Context, slug, version string) error {
	return s.q.SetLiveVersion(ctx, dbgen.SetLiveVersionParams{Slug: slug, LiveVersion: version})
}

func (s *store) RecordPublish(ctx context.Context, slug, version, by string, ok bool, detail string) {
	_ = s.q.RecordPublish(ctx, dbgen.RecordPublishParams{Slug: slug, Version: version, PublishedBy: by, Ok: ok, Detail: detail})
}

func (s *store) Touch(ctx context.Context, slugs []string) {
	for _, slug := range slugs {
		_ = s.q.TouchApp(ctx, slug)
	}
}

func (s *store) Settings(ctx context.Context, slug string) (map[string]string, error) {
	rows, err := s.q.ListSettings(ctx, slug)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

func (s *store) SetSetting(ctx context.Context, slug, key string, value *string) error {
	if value == nil {
		return s.q.DeleteSetting(ctx, dbgen.DeleteSettingParams{Slug: slug, Key: key})
	}
	return s.q.UpsertSetting(ctx, dbgen.UpsertSettingParams{Slug: slug, Key: key, Value: *value})
}

func (s *store) CopySettings(ctx context.Context, from, to string) error {
	return s.q.CopySettings(ctx, dbgen.CopySettingsParams{FromSlug: from, ToSlug: to})
}
