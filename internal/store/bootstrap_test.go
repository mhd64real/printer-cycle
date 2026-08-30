package store_test

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

// A fresh box has to offer a way in, and that way has to be available only to
// somebody with access to the machine.
func TestBootstrapIssuesATokenOnAFreshBox(t *testing.T) {
	db := newDB(t)

	needs, err := db.NeedsSetup(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("a fresh box does not report needing setup")
	}

	token, issued, err := db.Bootstrap(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if !issued {
		t.Fatal("no setup token was issued on a fresh box")
	}
	if !strings.HasPrefix(token, "PCE-") {
		t.Errorf("token = %q", token)
	}

	// The dashboard connector exists, holds every scope, and is not yet live.
	c, err := db.Connector(ctx(), store.DashboardConnectorID)
	if err != nil {
		t.Fatalf("the dashboard connector was not created: %v", err)
	}
	if len(c.Scopes) != len(store.KnownScopes()) {
		t.Errorf("dashboard has %d scopes, want all %d", len(c.Scopes), len(store.KnownScopes()))
	}
	if c.Enabled || c.Enrolled() {
		t.Error("the dashboard connector is live before anyone has enrolled it")
	}
}

// The full first run: token, key, account, and a box that no longer offers a way
// in. This is the sequence a person actually goes through.
func TestFirstRunEndToEnd(t *testing.T) {
	db := newDB(t)

	token, issued, err := db.Bootstrap(ctx())
	if err != nil || !issued {
		t.Fatalf("Bootstrap: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dash, err := db.Enrol(ctx(), token, pub)
	if err != nil {
		t.Fatalf("the dashboard could not enrol with its setup token: %v", err)
	}

	// Enrolling during first run enables the dashboard, because there is nobody
	// yet who could enable it.
	if !dash.Enabled {
		t.Error("the dashboard is not enabled after first-run enrolment, so setup cannot continue")
	}

	admin, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !admin.IsAdmin {
		t.Error("the account created during setup is not an administrator")
	}

	// Setup is over. The box must stop offering a way in.
	needs, err := db.NeedsSetup(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("the box still reports needing setup after an account exists")
	}

	_, issued, err = db.Bootstrap(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if issued {
		t.Error("a setup token was issued on a box that already has an account")
	}
}

// Restarting before setup finishes issues a new token and invalidates the old
// one, so exactly one token is ever valid: the one on screen.
func TestRestartingBeforeSetupReplacesTheToken(t *testing.T) {
	db := newDB(t)

	first, _, err := db.Bootstrap(ctx())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := db.Bootstrap(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("restarting produced the same token")
	}

	pub, _, _ := ed25519.GenerateKey(nil)
	if _, err := db.Enrol(ctx(), first, pub); !errors.Is(err, store.ErrEnrolmentInvalid) {
		t.Errorf("the superseded token still works: %v", err)
	}
	if _, err := db.Enrol(ctx(), second, pub); err != nil {
		t.Errorf("the current token does not work: %v", err)
	}
}

// The first-run exemption is narrow: it applies to the dashboard alone. Any
// other connector enrolling still arrives switched off, awaiting a decision.
func TestFirstRunEnablingIsOnlyForTheDashboard(t *testing.T) {
	db := newDB(t)

	if _, err := db.CreateConnector(ctx(), "telegram", "Telegram", nil); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(ctx(), "telegram", 0)
	if err != nil {
		t.Fatal(err)
	}

	pub, _, _ := ed25519.GenerateKey(nil)
	c, err := db.Enrol(ctx(), token, pub)
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled {
		t.Error("a connector other than the dashboard was enabled by enrolling on a fresh box")
	}
}

// Once an account exists, even the dashboard re-enrolling does not switch itself
// on. From that point an administrator decides.
func TestEnrolmentStopsEnablingOnceAnAccountExists(t *testing.T) {
	db := newDB(t)

	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConnector(ctx(), store.DashboardConnectorID, "Dashboard", nil); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(ctx(), store.DashboardConnectorID, 0)
	if err != nil {
		t.Fatal(err)
	}

	pub, _, _ := ed25519.GenerateKey(nil)
	c, err := db.Enrol(ctx(), token, pub)
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled {
		t.Error("enrolment enabled a connector on a box that already has an administrator")
	}
}
