package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// registerTelegram declares a manifest with one of every interesting kind of
// setting, including a secret.
func registerTelegram(t *testing.T, c *client) {
	t.Helper()

	resp := c.call("register", map[string]any{
		"name":     "Telegram",
		"version":  "1.2.0",
		"identity": "linked",
		"settings": []map[string]any{
			{"key": "bot_token", "type": "secret", "label": "Bot token", "required": true},
			{"key": "max_pages", "type": "int", "label": "Page limit", "default": 20, "min": 1, "max": 500},
			{"key": "allow_groups", "type": "bool", "label": "Accept documents in groups", "default": false},
		},
	})
	if resp.Error != nil {
		t.Fatalf("register: %v", resp.Error)
	}
}

// The stage's done-when, first half: a change reaches a running connector
// without it being restarted.
func TestASettingChangeReachesTheRunningConnector(t *testing.T) {
	url, db := testServer(t)

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	registerTelegram(t, telegram)

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	changed := make(chan map[string]any, 4)

	// The dashboard writes while the connector is connected and idle.
	go func() {
		dashboard.callCollecting("connectors.setSetting", map[string]any{
			"connector_id": "telegram",
			"key":          "max_pages",
			"value":        42,
		}, nil)
	}()

	telegram.awaitNotification(func(method string, params json.RawMessage) bool {
		if method != "settings.changed" {
			return false
		}
		var payload struct {
			Settings map[string]any `json:"settings"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return false
		}
		changed <- payload.Settings
		return true
	}, 15*time.Second)

	settings := <-changed
	if settings["max_pages"] != float64(42) {
		t.Errorf("max_pages = %v, want the value just written", settings["max_pages"])
	}
	// The values travel with the notification, so the connector does not have to
	// turn round and ask for them.
	if _, ok := settings["allow_groups"]; !ok {
		t.Error("the notification carried only the changed key, not the connector's settings")
	}
}

// The second half: a secret goes to its owner and to nobody else.
func TestSecretsReachTheirConnectorAndNotTheDashboard(t *testing.T) {
	url, db := testServer(t)

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	registerTelegram(t, telegram)

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	const token = "12345:AAHfake-bot-token"
	if resp := dashboard.call("connectors.setSetting", map[string]any{
		"connector_id": "telegram", "key": "bot_token", "value": token,
	}); resp.Error != nil {
		t.Fatalf("setting a secret: %v", resp.Error)
	}

	// The connector can read its own back, or it could not use it.
	resp := telegram.call("settings.get", nil)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var own struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(resp.Result, &own); err != nil {
		t.Fatal(err)
	}
	if own.Settings["bot_token"] != token {
		t.Errorf("the connector cannot read its own token: %v", own.Settings["bot_token"])
	}

	// The dashboard cannot, anywhere in the response.
	resp = dashboard.call("connectors.list", nil)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if strings.Contains(string(resp.Result), "AAHfake") {
		t.Fatalf("the secret appears in what the dashboard is shown: %s", resp.Result)
	}

	var listed struct {
		Connectors []struct {
			ID       string         `json:"id"`
			Settings map[string]any `json:"settings"`
			Schema   []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"settings_schema"`
			Connected bool `json:"connected"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(resp.Result, &listed); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, connector := range listed.Connectors {
		if connector.ID != "telegram" {
			continue
		}
		found = true

		// Set, but not readable. The dashboard has to be able to show that a
		// token exists without showing what it is.
		marker, ok := connector.Settings["bot_token"].(map[string]any)
		if !ok {
			t.Fatalf("bot_token = %#v, want a set marker rather than a value", connector.Settings["bot_token"])
		}
		if marker["set"] != true {
			t.Error("the dashboard cannot tell that a token has been set")
		}

		// The schema travels too, which is what lets the dashboard draw a
		// settings page for a connector it knows nothing about.
		if len(connector.Schema) != 3 {
			t.Errorf("schema has %d fields, want 3", len(connector.Schema))
		}
		if !connector.Connected {
			t.Error("a connector with an open connection is not shown as connected")
		}
	}
	if !found {
		t.Fatal("telegram is not in the listing")
	}
}

// Overwriting a secret must not be a way to read the old one back.
func TestReplacingASecretRevealsNothing(t *testing.T) {
	url, db := testServer(t)

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	registerTelegram(t, telegram)
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	dashboard.call("connectors.setSetting", map[string]any{
		"connector_id": "telegram", "key": "bot_token", "value": "old-secret-value",
	})
	resp := dashboard.call("connectors.setSetting", map[string]any{
		"connector_id": "telegram", "key": "bot_token", "value": "new-secret-value",
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if strings.Contains(string(resp.Result), "old-secret") ||
		strings.Contains(string(resp.Result), "new-secret") {
		t.Errorf("a secret came back in the reply to setting it: %s", resp.Result)
	}
}

func TestSettingValuesAreCheckedOnTheWay(t *testing.T) {
	url, db := testServer(t)

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	registerTelegram(t, telegram)
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	bad := []struct {
		key   string
		value any
		why   string
	}{
		{"max_pages", 9000, "above the declared maximum"},
		{"max_pages", "lots", "text where a number belongs"},
		{"allow_groups", "yes", "text where a boolean belongs"},
		{"nonexistent", "x", "a key the connector never declared"},
	}
	for _, tc := range bad {
		resp := dashboard.call("connectors.setSetting", map[string]any{
			"connector_id": "telegram", "key": tc.key, "value": tc.value,
		})
		if resp.Error == nil {
			t.Errorf("accepted %s for %s", tc.why, tc.key)
			continue
		}
		if resp.Error.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("%s gave code %d, want invalid params", tc.why, resp.Error.Code)
		}
	}
}

// Reading your own settings needs no permission. Reading everybody's does.
func TestReadingOtherConnectorsSettingsNeedsAScope(t *testing.T) {
	url, db := testServer(t)

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	registerTelegram(t, telegram)

	if resp := telegram.call("settings.get", nil); resp.Error != nil {
		t.Errorf("a connector was refused its own settings: %v", resp.Error)
	}

	if resp := telegram.call("connectors.list", nil); resp.Error == nil ||
		resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("a connector without connectors.read listed every connector: %v", resp.Error)
	}

	if resp := telegram.call("connectors.setSetting", map[string]any{
		"connector_id": "telegram", "key": "max_pages", "value": 5,
	}); resp.Error == nil || resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("a connector changed settings without connectors.manage: %v", resp.Error)
	}
}
