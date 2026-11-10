// Package db holds the platform's database schema and queries as plain SQL
// files. No SQL is written in Go:
//
//   - bootstrap/   idempotent bookkeeping applied on every start
//   - migrations/  the schema, one numbered file per change; never edit a
//     released file, add a new one
//   - queries/     queries, compiled to Go in dbgen/ by sqlc
//   - appdb/       the same split for what runs inside each app's own database
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

	"github.com/mliezun/caddy-snake/apps/db/dbgen"
)

//go:embed bootstrap/*.sql migrations/*.sql
var platformSQL embed.FS

//go:embed appdb/schema/*.sql
var appSQL embed.FS

// advisoryLockKey serializes migration runs when several processes start at once.
const advisoryLockKey int64 = 7231906420251110

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
// files, then every migration file not applied yet, each in its own
// transaction. It returns the names of the migrations it applied.
func Migrate(ctx context.Context, conn *sql.DB) ([]string, error) {
	names, err := Migrations()
	if err != nil {
		return nil, err
	}
	// One connection for the whole run so the advisory lock is held throughout.
	c, err := conn.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	q := dbgen.New(c)
	if err := q.LockMigrations(ctx, advisoryLockKey); err != nil {
		return nil, err
	}
	defer func() { _ = q.UnlockMigrations(context.Background(), advisoryLockKey) }()

	if err := execDir(ctx, c, platformSQL, "bootstrap"); err != nil {
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
			return applied, err
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		if err := q.WithTx(tx).RecordMigration(ctx, name); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// PrepareAppDatabase creates the bookkeeping table inside an app's own
// database, where the app's migrations/*.sql files are tracked.
func PrepareAppDatabase(ctx context.Context, conn *sql.DB) error {
	return execDir(ctx, conn, appSQL, "appdb/schema")
}
