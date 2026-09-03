package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// authedClient dials, authenticates, and hands back a client ready to work.
func authedClient(t *testing.T, url string, db *store.DB, id string, scopes []string) *client {
	t.Helper()

	key := enrolledConnector(t, db, id, scopes)
	c := dial(t, url)
	if resp := c.call("authenticate", map[string]any{
		"connector_id": id, "proof": c.proof(key),
	}); resp.Error != nil {
		t.Fatalf("authenticating %s: %v", id, resp.Error)
	}
	return c
}

// The stage's done-when: holding one scope does not confer another.
func TestAConnectorIsRefusedAScopeItDoesNotHold(t *testing.T) {
	url, db := testServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})

	resp := c.call("users.list", nil)
	if resp.Error == nil {
		t.Fatal("a connector holding only jobs.submit listed the users")
	}
	if resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Fatalf("code = %d, want %d", resp.Error.Code, jsonrpc.CodeScopeDenied)
	}

	// The refusal names the scope needed, so a connector author knows what to
	// ask an administrator for.
	var data struct {
		Required string `json:"required_scope"`
	}
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("error data is not an object: %v (%s)", err, resp.Error.Data)
	}
	if data.Required != store.ScopeUsersRead {
		t.Errorf("required_scope = %q, want %q", data.Required, store.ScopeUsersRead)
	}
}

func TestAConnectorMayUseAScopeItHolds(t *testing.T) {
	url, db := testServer(t)

	if _, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	c := authedClient(t, url, db, "dashboard", []string{store.ScopeUsersRead})

	resp := c.call("users.list", nil)
	if resp.Error != nil {
		t.Fatalf("a connector holding users.read was refused: %v", resp.Error)
	}

	var result struct {
		Users []struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"users"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Users) != 1 || result.Users[0].Username != "mohamed" {
		t.Fatalf("users = %+v", result.Users)
	}
	if !result.Users[0].IsAdmin {
		t.Error("the first account is not reported as an administrator")
	}

	// Nothing resembling a credential may appear in what goes over the wire.
	for _, forbidden := range []string{"password", "hash", "argon2"} {
		if strings.Contains(string(resp.Result), forbidden) {
			t.Errorf("the response mentions %q: %s", forbidden, resp.Result)
		}
	}
}

// A method absent from the permission table does not exist, whatever handler
// somebody may have written. That is what makes the table the single decision
// point rather than a suggestion.
func TestAMethodNotInThePermissionTableDoesNotExist(t *testing.T) {
	url, db := testServer(t)
	c := authedClient(t, url, db, "telegram", store.KnownScopes())

	// Names chosen never to be implemented. Listing methods that merely had not
	// been built yet made this test expire as soon as they were.
	for _, method := range []string{"nonsense", "printers.explode", "jobs.teleport"} {
		resp := c.call(method, map[string]any{})
		if resp.Error == nil {
			t.Errorf("%s succeeded despite not being in the permission table", method)
			continue
		}
		if resp.Error.Code != jsonrpc.CodeMethodNotFound {
			t.Errorf("%s gave code %d, want method not found", method, resp.Error.Code)
		}
	}
}

// Holding every scope is still not authentication.
func TestScopesDoNotSubstituteForAuthentication(t *testing.T) {
	url, _ := testServer(t)

	c := dial(t, url)
	resp := c.call("users.list", nil)
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("unauthenticated users.list gave %v", resp.Error)
	}
}

// Revoking a scope takes effect at once, on a connection that is already open.
//
// Permissions used to be read from a snapshot taken when the connection
// authenticated, which meant a revoked scope kept working until the connector
// happened to reconnect. For something that stays connected for weeks, that is
// indistinguishable from never.
func TestRevokingAScopeTakesEffectImmediately(t *testing.T) {
	url, db := testServer(t)

	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	c := authedClient(t, url, db, "dashboard", []string{store.ScopeUsersRead})
	if resp := c.call("users.list", nil); resp.Error != nil {
		t.Fatalf("the scope did not work to begin with: %v", resp.Error)
	}

	if err := db.SetConnectorScopes(ctx(), "dashboard", nil); err != nil {
		t.Fatal(err)
	}

	resp := c.call("users.list", nil)
	if resp.Error == nil {
		t.Fatal("a revoked scope still worked on an open connection")
	}
	if resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("code = %d, want scope denied", resp.Error.Code)
	}
}

// Switching a connector off stops it at once, without having to find and close
// its connection.
func TestDisablingAConnectorTakesEffectImmediately(t *testing.T) {
	url, db := testServer(t)

	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})
	if resp := c.call("register", map[string]any{"name": "Telegram"}); resp.Error != nil {
		t.Fatalf("registering while enabled: %v", resp.Error)
	}

	if err := db.SetConnectorEnabled(ctx(), "telegram", false); err != nil {
		t.Fatal(err)
	}

	resp := c.call("register", map[string]any{"name": "Telegram"})
	if resp.Error == nil {
		t.Fatal("a disabled connector carried on working on its existing connection")
	}
	if resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("code = %d, want not authenticated", resp.Error.Code)
	}
}
