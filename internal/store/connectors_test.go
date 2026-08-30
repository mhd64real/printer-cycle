package store_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/store"
)

func newKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// Installing something must never be the same act as trusting it. A new
// connector arrives disabled and with no key.
func TestNewConnectorIsDisabledAndUnenrolled(t *testing.T) {
	db := newDB(t)

	c, err := db.CreateConnector(ctx(), "telegram", "Telegram", []string{store.ScopeJobsSubmit})
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled {
		t.Error("a newly installed connector is enabled")
	}
	if c.Enrolled() {
		t.Error("a newly installed connector already has a key")
	}
	if len(c.Scopes) != 1 || c.Scopes[0] != store.ScopeJobsSubmit {
		t.Errorf("scopes = %v", c.Scopes)
	}
	if c.IdentityPolicy != store.IdentityNone {
		t.Errorf("identity policy = %q, want none by default", c.IdentityPolicy)
	}
}

// The whole point of Ed25519 here: core stores a public key and nothing else, so
// a copied database cannot be used to impersonate anybody.
func TestEnrolmentStoresOnlyAPublicKey(t *testing.T) {
	db := newDB(t)

	if _, err := db.CreateConnector(ctx(), "telegram", "Telegram", nil); err != nil {
		t.Fatal(err)
	}

	token, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "PCE-") {
		t.Errorf("token = %q, want a readable PCE- prefixed token", token)
	}

	pub := newKey(t)
	c, err := db.Enrol(ctx(), token, pub)
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}
	if !c.Enrolled() {
		t.Fatal("the connector is not enrolled after enrolling")
	}
	if !c.PublicKey.Equal(pub) {
		t.Error("the stored key is not the one presented")
	}

	// Nothing in the database may be a private key or a shared secret.
	var credential string
	if err := db.QueryRow(`SELECT credential FROM connectors WHERE id = 'telegram'`).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	if len(credential) == 0 {
		t.Fatal("no credential stored")
	}
	decoded := c.PublicKey
	if len(decoded) != ed25519.PublicKeySize {
		t.Errorf("stored credential is %d bytes, want a %d byte public key",
			len(decoded), ed25519.PublicKeySize)
	}
}

// A token is spent when used. Replaying one would let anybody who saw it once
// substitute their own key for the connector's.
func TestEnrolmentTokensAreSingleUse(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}

	token, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Enrol(ctx(), token, newKey(t)); err != nil {
		t.Fatal(err)
	}

	_, err = db.Enrol(ctx(), token, newKey(t))
	if !errors.Is(err, store.ErrEnrolmentInvalid) {
		t.Errorf("reusing a token gave %v, want ErrEnrolmentInvalid", err)
	}
}

func TestExpiredAndUnknownTokensAreRefusedIdentically(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}

	expired, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Age it, rather than asking for a negative lifetime: a non-positive TTL is
	// treated as "use the default", so the API cannot mint an expired token and
	// this has to exercise the real expiry path instead.
	if _, err := db.Exec(`UPDATE connector_enrolments SET expires_at = '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	// An expired token, an unknown token and a used one all return the same
	// error. Anything more specific would tell whoever is guessing which of
	// their guesses was closest.
	for name, tok := range map[string]string{
		"expired": expired,
		"unknown": "PCE-AAAAA-BBBBB-CCCCC-DDDDD",
		"garbage": "not a token",
	} {
		if _, err := db.Enrol(ctx(), tok, newKey(t)); !errors.Is(err, store.ErrEnrolmentInvalid) {
			t.Errorf("%s token gave %v, want ErrEnrolmentInvalid", name, err)
		}
	}
}

func TestEnrolRejectsAKeyOfTheWrongSize(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Enrol(ctx(), token, ed25519.PublicKey([]byte("too short"))); err == nil {
		t.Error("a key of the wrong length was accepted")
	}
}

// Enabling a connector that cannot authenticate would leave an entry looking
// live in the dashboard while rejecting every connection.
func TestCannotEnableAnUnenrolledConnector(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}

	if err := db.SetConnectorEnabled(ctx(), "telegram", true); !errors.Is(err, store.ErrNotEnrolled) {
		t.Errorf("enabling an unenrolled connector gave %v, want ErrNotEnrolled", err)
	}

	token, _ := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if _, err := db.Enrol(ctx(), token, newKey(t)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConnectorEnabled(ctx(), "telegram", true); err != nil {
		t.Errorf("enabling an enrolled connector: %v", err)
	}

	c, err := db.Connector(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled {
		t.Error("the connector is not enabled after being enabled")
	}
}

// A scope typo must be refused, not stored. Accepting "printers.mangage" would
// produce a connector that looks permitted in the dashboard and is denied at
// every call.
func TestUnknownScopesAreRefused(t *testing.T) {
	db := newDB(t)

	_, err := db.CreateConnector(ctx(), "telegram", "", []string{"printers.mangage"})
	if err == nil {
		t.Fatal("a misspelled scope was accepted")
	}
	if !strings.Contains(err.Error(), "printers.mangage") {
		t.Errorf("the error does not name the offending scope: %v", err)
	}

	if _, err := db.CreateConnector(ctx(), "telegram", "", store.KnownScopes()); err != nil {
		t.Errorf("every known scope should be acceptable: %v", err)
	}
}

func TestConnectorIDValidation(t *testing.T) {
	for _, ok := range []string{"telegram", "airprint", "my-connector-2"} {
		if err := store.ValidConnectorID(ok); err != nil {
			t.Errorf("ValidConnectorID(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Telegram", "my connector", "tele/gram", strings.Repeat("a", 65)} {
		if err := store.ValidConnectorID(bad); err == nil {
			t.Errorf("ValidConnectorID(%q) accepted it", bad)
		}
	}
}

func TestScopesAreReplacedNotAppended(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "",
		[]string{store.ScopeJobsSubmit, store.ScopeJobsRead}); err != nil {
		t.Fatal(err)
	}

	if err := db.SetConnectorScopes(ctx(), "telegram", []string{store.ScopeJobsSubmit}); err != nil {
		t.Fatal(err)
	}

	c, err := db.Connector(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Scopes) != 1 || c.Scopes[0] != store.ScopeJobsSubmit {
		t.Errorf("scopes = %v, want revoked permissions to actually be gone", c.Scopes)
	}
}

// Deleting a connector must take its enrolment tokens with it, or an unused
// token could enrol a key against an id somebody reinstalls later.
func TestDeletingAConnectorRevokesItsTokens(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	token, _ := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)

	if err := db.DeleteConnector(ctx(), "telegram"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Enrol(ctx(), token, newKey(t)); !errors.Is(err, store.ErrEnrolmentInvalid) {
		t.Errorf("a token from before deletion still works: %v", err)
	}
}
