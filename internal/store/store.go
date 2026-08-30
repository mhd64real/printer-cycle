// Package store is printer-cycle's database.
//
// SQLite, through a pure Go driver. The choice of driver is not incidental: the
// cgo driver would make cross-compiling from a development machine to a
// Raspberry Pi stop being one command and start being a toolchain problem, and
// the Makefile builds with CGO_ENABLED=0 so that choice is enforced by the build
// rather than by remembering.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB is printer-cycle's database.
type DB struct {
	*sql.DB
}

// Open opens the database at path, creating it and applying any outstanding
// migrations. Missing parent directories are created.
//
// Pass ":memory:" for a throwaway database, which is what tests use.
func Open(path string) (*DB, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: creating %s: %w", dir, err)
			}
		}
	}

	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}

	// One connection, on purpose.
	//
	// SQLite allows many readers alongside one writer in WAL mode, but juggling
	// that means separate read and write pools and a permanent supply of
	// SQLITE_BUSY bugs. printer-cycle serves a household, not a datacentre: a few
	// users, a handful of connectors, and queries that touch tens of rows. One
	// connection removes an entire category of concurrency bug for a cost that
	// does not exist at this scale.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}

	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// dsn builds the connection string, including the pragmas that matter.
func dsn(path string) string {
	pragmas := []string{
		// Readers do not block the writer, and a crash mid-write cannot leave a
		// corrupt database.
		"journal_mode(WAL)",

		// With WAL, NORMAL syncs at checkpoints rather than every commit. Durable
		// against process crashes, and only at risk from losing power at exactly
		// the wrong moment. It also writes far less, which matters when the disk
		// is an SD card that wears out.
		"synchronous(NORMAL)",

		// Foreign keys are off by default in SQLite, which quietly turns every
		// declared relationship into a comment.
		"foreign_keys(1)",

		// Wait rather than failing instantly if the file is briefly locked, for
		// example while a backup is copying it.
		"busy_timeout(5000)",
	}
	return "file:" + path + "?_pragma=" + strings.Join(pragmas, "&_pragma=")
}

// migrate applies every embedded migration that has not run yet.
//
// Deliberately small and dependency free. Migrations are plain SQL files named
// so they sort into order, each runs once inside a transaction, and the ones
// that have run are recorded. Nothing else is needed, and every migration
// library brings opinions this project does not require.
func (db *DB) migrate() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`)
	if err != nil {
		return fmt.Errorf("store: creating schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: reading applied migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("store: reading applied migrations: %w", err)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("store: reading applied migrations: %w", err)
	}
	rows.Close()

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: reading migration %s: %w", name, err)
		}
		if err := db.applyMigration(name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) applyMigration(name, body string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: migration %s: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(body); err != nil {
		return fmt.Errorf("store: migration %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return fmt.Errorf("store: recording migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %s: %w", name, err)
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: listing migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	// Filenames are numbered so lexical order is application order.
	sort.Strings(names)
	return names, nil
}

// AppliedMigrations lists the migrations that have run, in order.
func (db *DB) AppliedMigrations() ([]string, error) {
	rows, err := db.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
