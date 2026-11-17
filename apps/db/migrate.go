// Package db holds the platform's database schema and queries as plain SQL
// files (SQLite). No SQL is written in Go:
//
//   - bootstrap/   idempotent bookkeeping applied on every start
//   - migrations/  the schema, one numbered file per change; never edit a
//     released file, add a new one
//   - queries/     queries, compiled to Go in dbgen/ by sqlc
//   - appdb/       the same split for what runs inside each app's own data file
//
// See ../sqlc.yaml for how to regenerate the Go code.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/mliezun/caddy-snake/apps/db/dbgen"
)

//go:embed bootstrap/*.sql migrations/*.sql
var platformSQL embed.FS

//go:embed appdb/schema/*.sql
var appSQL embed.FS

func sqlFiles(fsys embed.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func execDir(ctx context.Context, conn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, fsys embed.FS, dir string) error {
	names, err := sqlFiles(fsys, dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		body, err := fsys.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("%s/%s: %w", dir, name, err)
		}
	}
	return nil
}

// Migrations returns the embedded platform migration file names in the order
// they apply.
func Migrations() ([]string, error) { return sqlFiles(platformSQL, "migrations") }

// Migrate brings the platform database up to date: it applies the bootstrap
// files, then every migration file not applied yet. It returns the names of
// the migrations it applied.
//
// Everything happens in one transaction. The connection is opened so that a
// transaction takes the write lock as it begins (see sqliteDSN in the apps
// package), so when the server and a command-line tool start together, the
// second waits and then finds nothing left to do. SQLite rolls back schema
// changes like any other change, so a failed migration leaves no half-made
// tables behind.
func Migrate(ctx context.Context, conn *sql.DB) ([]string, error) {
	names, err := Migrations()
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	if err := execDir(ctx, tx, platformSQL, "bootstrap"); err != nil {
		return nil, err
	}
	appliedBefore, err := q.ListAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(appliedBefore))
	for _, name := range appliedBefore {
		done[name] = true
	}

	var applied []string
	for _, name := range names {
		if done[name] {
			continue
		}
		body, err := platformSQL.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		if err := q.RecordMigration(ctx, dbgen.RecordMigrationParams{Name: name, AppliedAt: time.Now().Unix()}); err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return applied, nil
}

// PrepareAppDatabase creates the bookkeeping table inside an app's own data
// file, where the app's migrations/*.sql files are tracked.
func PrepareAppDatabase(ctx context.Context, conn *sql.DB) error {
	return execDir(ctx, conn, appSQL, "appdb/schema")
}
