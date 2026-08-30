package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

func TestOpenCreatesAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "printer-cycle.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Parent directories are created, because the installer should not have to
	// know where the database wants to live.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no database file at %s: %v", path, err)
	}

	applied, err := db.AppliedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("no migrations ran on a fresh database")
	}
	t.Logf("applied: %v", applied)

	var created string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'created_at'`).Scan(&created); err != nil {
		t.Fatalf("the first migration did not take effect: %v", err)
	}
	if created == "" {
		t.Error("created_at is empty")
	}
}

// Migrations must run once and only once. A runner that reapplied them would
// destroy data on every restart, which is the worst possible bug in this file.
func TestMigrationsRunOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printer-cycle.db")

	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := first.AppliedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	// Leave a mark that a re-run would wipe.
	if _, err := first.Exec(`INSERT INTO meta (key, value) VALUES ('probe', 'survives')`); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer second.Close()

	after, err := second.AppliedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("migrations went from %d to %d on reopen, they must run once", len(before), len(after))
	}

	var probe string
	if err := second.QueryRow(`SELECT value FROM meta WHERE key = 'probe'`).Scan(&probe); err != nil {
		t.Fatalf("data written before reopening is gone: %v", err)
	}
	if probe != "survives" {
		t.Errorf("probe = %q, want survives", probe)
	}
}

// The pragmas are set through the connection string, where a typo fails
// silently: SQLite simply keeps its defaults and nothing complains. Each one is
// therefore read back rather than assumed.
func TestPragmasActuallyApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printer-cycle.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal: readers would block the writer", journal)
	}

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Error("foreign_keys is off, which turns every declared relationship into a comment")
	}

	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL): FULL writes far more, and the disk is an SD card", synchronous)
	}

	var busy int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

// Foreign keys being enabled is only worth anything if they are enforced.
func TestForeignKeysAreEnforced(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "printer-cycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child  (id INTEGER PRIMARY KEY,
		                     parent_id INTEGER NOT NULL REFERENCES parent(id));
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`INSERT INTO child (parent_id) VALUES (999)`); err == nil {
		t.Error("a row referencing a parent that does not exist was accepted")
	}
}

func TestOpenInMemory(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	defer db.Close()

	applied, err := db.AppliedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Error("no migrations ran on an in-memory database")
	}
}
