package store_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

func newDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "printer-cycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(query), "\n", 2)[0], err)
	}
}

func seedUser(t *testing.T, db *store.DB, id, username string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO users (id, username, password_hash) VALUES (?, ?, 'x')`, id, username)
}

func seedConnector(t *testing.T, db *store.DB, id string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO connectors (id, name, credential) VALUES (?, ?, 'secret')`, id, id)
}

func seedPrinter(t *testing.T, db *store.DB, id, queue string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO printers (id, queue_name, display_name, device_uri)
	                 VALUES (?, ?, ?, 'usb://x/y')`, id, queue, queue)
}

// Usernames are case insensitive. Two accounts differing only in capitals would
// be indistinguishable to the person trying to log in.
func TestUsernamesAreCaseInsensitive(t *testing.T) {
	db := newDB(t)
	seedUser(t, db, "u1", "Mohamed")

	_, err := db.Exec(`INSERT INTO users (id, username, password_hash) VALUES ('u2', 'mohamed', 'x')`)
	if err == nil {
		t.Error("a username differing only in case was accepted")
	}
}

// Deleting a connector must take its scopes, settings and identity links with
// it. Orphaned scopes would be a security problem: reinstalling a connector
// under the same id would silently inherit permissions somebody had revoked.
func TestDeletingAConnectorRemovesItsGrants(t *testing.T) {
	db := newDB(t)
	seedUser(t, db, "u1", "owner")
	seedConnector(t, db, "telegram")

	mustExec(t, db, `INSERT INTO connector_scopes (connector_id, scope) VALUES ('telegram', 'jobs.submit')`)
	mustExec(t, db, `INSERT INTO connector_settings (connector_id, key, value) VALUES ('telegram', 'bot_token', 'abc')`)
	mustExec(t, db, `INSERT INTO identity_links (id, connector_id, external_id, user_id)
	                 VALUES ('l1', 'telegram', 'tg:1', 'u1')`)

	mustExec(t, db, `DELETE FROM connectors WHERE id = 'telegram'`)

	for _, table := range []string{"connector_scopes", "connector_settings", "identity_links"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows after the connector was deleted", table, n)
		}
	}
}

// Job history has to outlive the connector that created it. Somebody
// uninstalling a Telegram bot should not lose the record of everything they
// printed through it.
func TestJobsOutliveTheirConnector(t *testing.T) {
	db := newDB(t)
	seedUser(t, db, "u1", "owner")
	seedConnector(t, db, "telegram")
	seedPrinter(t, db, "p1", "office")

	mustExec(t, db, `INSERT INTO jobs (id, printer_id, user_id, connector_id, name)
	                 VALUES ('j1', 'p1', 'u1', 'telegram', 'invoice.pdf')`)

	mustExec(t, db, `DELETE FROM connectors WHERE id = 'telegram'`)

	var name string
	var connector any
	if err := db.QueryRow(`SELECT name, connector_id FROM jobs WHERE id = 'j1'`).Scan(&name, &connector); err != nil {
		t.Fatalf("the job vanished with its connector: %v", err)
	}
	if name != "invoice.pdf" {
		t.Errorf("name = %q", name)
	}
	if connector != nil {
		t.Errorf("connector_id = %v, want null once the connector is gone", connector)
	}
}

// Deleting a printer does take its jobs: a job with no printer is meaningless,
// and keeping it would leave rows nothing can render.
func TestDeletingAPrinterRemovesItsJobs(t *testing.T) {
	db := newDB(t)
	seedPrinter(t, db, "p1", "office")
	mustExec(t, db, `INSERT INTO jobs (id, printer_id, name) VALUES ('j1', 'p1', 'x')`)

	mustExec(t, db, `DELETE FROM printers WHERE id = 'p1'`)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d jobs survived their printer", n)
	}
}

// One external identity maps to at most one user per connector. Without this,
// two accounts could claim the same Telegram user and jobs would go to whichever
// row was read first.
func TestOneExternalIdentityPerConnector(t *testing.T) {
	db := newDB(t)
	seedUser(t, db, "u1", "one")
	seedUser(t, db, "u2", "two")
	seedConnector(t, db, "telegram")

	mustExec(t, db, `INSERT INTO identity_links (id, connector_id, external_id, user_id)
	                 VALUES ('l1', 'telegram', 'tg:887312', 'u1')`)

	_, err := db.Exec(`INSERT INTO identity_links (id, connector_id, external_id, user_id)
	                   VALUES ('l2', 'telegram', 'tg:887312', 'u2')`)
	if err == nil {
		t.Error("the same external identity was linked to two users")
	}

	// The same external id under a different connector is fine: they are
	// different namespaces entirely.
	seedConnector(t, db, "signal")
	mustExec(t, db, `INSERT INTO identity_links (id, connector_id, external_id, user_id)
	                 VALUES ('l3', 'signal', 'tg:887312', 'u2')`)
}

// Many jobs may be waiting to reach CUPS, so cups_job_id is null for each of
// them, but no two jobs may claim the same CUPS id once assigned.
func TestCupsJobIdIsUniqueButOptional(t *testing.T) {
	db := newDB(t)
	seedPrinter(t, db, "p1", "office")

	mustExec(t, db, `INSERT INTO jobs (id, printer_id) VALUES ('j1', 'p1')`)
	mustExec(t, db, `INSERT INTO jobs (id, printer_id) VALUES ('j2', 'p1')`)

	mustExec(t, db, `UPDATE jobs SET cups_job_id = 42 WHERE id = 'j1'`)
	if _, err := db.Exec(`UPDATE jobs SET cups_job_id = 42 WHERE id = 'j2'`); err == nil {
		t.Error("two jobs were allowed to claim the same CUPS job id")
	}
}

func TestCheckConstraintsRejectNonsense(t *testing.T) {
	db := newDB(t)
	seedConnector(t, db, "c1")

	if _, err := db.Exec(`UPDATE connectors SET identity_policy = 'sometimes' WHERE id = 'c1'`); err == nil {
		t.Error("an identity policy outside none and linked was accepted")
	}
	if _, err := db.Exec(`UPDATE connectors SET enabled = 7 WHERE id = 'c1'`); err == nil {
		t.Error("a boolean column accepted 7")
	}
}

// Printers are shared by default. Restriction is opt-in, and the access table
// only means anything once it is set.
func TestPrintersAreUnrestrictedByDefault(t *testing.T) {
	db := newDB(t)
	seedPrinter(t, db, "p1", "office")

	var restricted int
	if err := db.QueryRow(`SELECT restricted FROM printers WHERE id = 'p1'`).Scan(&restricted); err != nil {
		t.Fatal(err)
	}
	if restricted != 0 {
		t.Error("a new printer is restricted by default; a household printer is a shared appliance")
	}
}
