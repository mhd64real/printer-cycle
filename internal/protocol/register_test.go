package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

func TestRegisterStoresTheManifest(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", []string{store.ScopeJobsSubmit})

	c := dial(t, url)
	if resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram", "proof": c.proof(key),
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	resp := c.call("register", map[string]any{
		"name":        "Telegram",
		"version":     "1.2.0",
		"description": "Print by sending a document to a Telegram bot.",
		"identity":    "linked",
		"settings": []map[string]any{
			{"key": "bot_token", "type": "secret", "label": "Bot token", "required": true},
			{"key": "max_pages", "type": "int", "label": "Page limit", "default": 20, "min": 1, "max": 500},
		},
	})
	if resp.Error != nil {
		t.Fatalf("register failed: %v", resp.Error)
	}

	var result struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	// Registering hands back current values, so a connector knows how it is
	// configured without a second call.
	if result.Settings["max_pages"] != float64(20) {
		t.Errorf("max_pages = %v, want the declared default", result.Settings["max_pages"])
	}

	// The declaration reached the database, where the dashboard will find it.
	schema, err := db.SettingsSchemaOf(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 2 {
		t.Errorf("stored schema has %d fields, want 2", len(schema))
	}

	connector, err := db.Connector(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if connector.IdentityPolicy != store.IdentityLinked {
		t.Errorf("identity policy = %q, want linked", connector.IdentityPolicy)
	}
	if connector.Version != "1.2.0" {
		t.Errorf("version = %q", connector.Version)
	}
}

// A bad manifest is the connector author's problem, so the reason comes back
// rather than being swallowed as an internal error. This is one of the few
// places where saying exactly what went wrong helps the person who can fix it
// and tells an attacker nothing.
func TestABadManifestSaysWhy(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", nil)

	c := dial(t, url)
	if resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram", "proof": c.proof(key),
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	resp := c.call("register", map[string]any{
		"name": "Telegram",
		"settings": []map[string]any{
			{"key": "quality", "type": "enum", "label": "Quality"},
		},
	})
	if resp.Error == nil {
		t.Fatal("an enum with no options was accepted")
	}
	if resp.Error.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("code = %d, want invalid params", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "quality") {
		t.Errorf("the error does not name the offending setting: %q", resp.Error.Message)
	}
}

func TestRegisterRequiresAuthentication(t *testing.T) {
	url, _ := testServer(t)

	c := dial(t, url)
	resp := c.call("register", map[string]any{"name": "Telegram"})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("register without authenticating gave %v", resp.Error)
	}
}
