package apps

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite" (pure Go, no C compiler needed)

	"github.com/mliezun/caddy-snake/apps/db"
)

// Everything is kept in SQLite files inside the data folder:
//
//	<data>/platform.db               the platform's own records
//	<data>/apps/<slug>/data/app.db   one file per app (and per draft)
//
// There is nothing to install, start or stop, and a backup is a copy of the
// data folder. The driver is pure Go, so the server still builds without a C
// compiler for every system.
//
// SQL lives in db/ as plain files (see AGENTS.md). The statements in this file
// are the ones that cannot be written there: they take a file name, or they
// are whatever statement a person asked to run.

const (
	platformDBName = "platform.db"
	appDataDir     = "data"
	appDBName      = "app.db"
	// vacuumInto writes a consistent copy of a database to a new file, while
	// other connections (the running app) keep using the original.
	vacuumInto = "VACUUM INTO ?"
)

// sqliteDSN is how every database file is opened:
//   - busy_timeout: wait for another writer (the app itself) instead of failing;
//   - journal_mode WAL: readers and one writer at the same time; it is stored
//     in the file, so apps get it too;
//   - foreign_keys: off by default in SQLite;
//   - _txlock=immediate: a transaction takes the write lock when it begins, so
//     two processes cannot both decide to apply the same migration.
func sqliteDSN(path string) string {
	return path + "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
}

// openSQLite opens (creating if needed) one database file.
func openSQLite(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a few connections are enough and
	// keep readers from waiting on each other.
	conn.SetMaxOpenConns(4)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return conn, nil
}

func (p *Platform) appDataPath(slug string) string {
	return filepath.Join(p.appDir(slug), appDataDir, appDBName)
}

// openAppDB opens an app's data file for a short task (a migration, a copy,
// a look at the data). Callers close it when done: on Windows an open file
// cannot be deleted.
func (p *Platform) openAppDB(ctx context.Context, slug string) (*sql.DB, error) {
	return openAppDBAt(ctx, p.appDataPath(slug))
}

func openAppDBAt(ctx context.Context, path string) (*sql.DB, error) {
	conn, err := openSQLite(ctx, path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// createAppData creates an app's empty data file, with the table that tracks
// its migrations.
func (p *Platform) createAppData(ctx context.Context, slug string) error {
	conn, err := p.openAppDB(ctx, slug)
	if err != nil {
		return fmt.Errorf("creating the app's data file: %w", err)
	}
	defer conn.Close()
	return db.PrepareAppDatabase(ctx, conn)
}

// cloneAppData copies everything in one app's data file into another's, which
// must not exist yet, so a draft starts with the live app's data. The copy is
// consistent even while the live app is writing.
func (p *Platform) cloneAppData(ctx context.Context, fromSlug, toSlug string) error {
	target := p.appDataPath(toSlug)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	if _, err := os.Stat(p.appDataPath(fromSlug)); os.IsNotExist(err) {
		// Nothing to copy: the app has no data file yet.
		return p.createAppData(ctx, toSlug)
	}
	src, err := p.openAppDB(ctx, fromSlug)
	if err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	defer src.Close()
	if _, err := src.ExecContext(ctx, vacuumInto, target); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	return nil
}

// unix is the time as the database stores it: whole seconds since 1970.
func unix(t time.Time) int64 { return t.Unix() }

// fromUnix turns a stored time back into a time.Time.
func fromUnix(seconds int64) time.Time { return time.Unix(seconds, 0) }
