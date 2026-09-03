package protocol_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// A connector on its first run has a key core has never seen, so enrolling has
// to work before it can authenticate. What gates it is the token.
func TestEnrollingBeforeAuthenticating(t *testing.T) {
	url, db := testServer(t)

	if _, err := db.CreateConnector(ctx(), "telegram", "Telegram", []string{store.ScopeJobsSubmit}); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	c := dial(t, url)

	// Core does not know this key yet.
	if resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram", "proof": c.proof(priv),
	}); resp.Error == nil {
		t.Fatal("an unenrolled key authenticated")
	}

	// Enrolling works on the same connection, before authenticating.
	resp := c.call("enrol", map[string]any{
		"token":      token,
		"public_key": base64.StdEncoding.EncodeToString(pub),
	})
	if resp.Error != nil {
		t.Fatalf("enrolling: %v", resp.Error)
	}
	var enrolled struct {
		ConnectorID string `json:"connector_id"`
	}
	if err := json.Unmarshal(resp.Result, &enrolled); err != nil {
		t.Fatal(err)
	}
	if enrolled.ConnectorID != "telegram" {
		t.Errorf("enrolled as %q", enrolled.ConnectorID)
	}

	// The key now works, on a fresh connection with an unspent challenge.
	if err := db.SetConnectorEnabled(ctx(), "telegram", true); err != nil {
		t.Fatal(err)
	}
	second := dial(t, url)
	if resp := second.call("authenticate", map[string]any{
		"connector_id": "telegram", "proof": second.proof(priv),
	}); resp.Error != nil {
		t.Fatalf("authenticating after enrolling: %v", resp.Error)
	}
}

func TestEnrolmentRefusesBadInput(t *testing.T) {
	url, db := testServer(t)

	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	goodKey := base64.StdEncoding.EncodeToString(pub)

	cases := map[string]map[string]any{
		"no token":       {"public_key": goodKey},
		"unknown token":  {"token": "PCE-AAAAA-BBBBB-CCCCC-DDDDD", "public_key": goodKey},
		"key not base64": {"token": token, "public_key": "!!!not base64!!!"},
		"key wrong size": {"token": token, "public_key": base64.StdEncoding.EncodeToString([]byte("short"))},
	}
	for name, params := range cases {
		c := dial(t, url)
		if resp := c.call("enrol", params); resp.Error == nil {
			t.Errorf("enrolment with %s was accepted", name)
		}
	}
}

// Enrolling must not be a way to skip authenticating.
func TestEnrollingDoesNotGrantAccess(t *testing.T) {
	url, db := testServer(t)

	if _, err := db.CreateConnector(ctx(), "telegram", "", store.KnownScopes()); err != nil {
		t.Fatal(err)
	}
	token, _ := db.NewEnrolmentToken(ctx(), "telegram", time.Hour)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	c := dial(t, url)
	if resp := c.call("enrol", map[string]any{
		"token": token, "public_key": base64.StdEncoding.EncodeToString(pub),
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	resp := c.call("users.list", nil)
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("enrolling alone gave access: %v", resp.Error)
	}
}
