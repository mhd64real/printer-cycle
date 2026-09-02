package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// The stage's done-when: a connector links an external identity to a user
// through the whole flow, and is told when it completes.
func TestTheFullPairingFlow(t *testing.T) {
	url, db := testServer(t)

	user, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	// The Telegram connector: it may ask for codes and resolve identities, and
	// nothing else.
	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})

	// Before anything is linked, the connector cannot say who this person is.
	resp := telegram.call("identity.resolve", map[string]any{"external_id": "tg:887312"})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeIdentityNotLinked {
		t.Fatalf("resolving an unlinked identity gave %v, want identity not linked", resp.Error)
	}

	// It asks for a code, which it would deliver over Telegram.
	resp = telegram.call("identity.linkRequest", map[string]any{
		"external_id": "tg:887312",
		"display":     "@mhd64",
		"ttl_seconds": 600,
	})
	if resp.Error != nil {
		t.Fatalf("requesting a code: %v", resp.Error)
	}
	var issued struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(resp.Result, &issued); err != nil {
		t.Fatal(err)
	}
	t.Logf("code=%s expires=%s", issued.Code, issued.ExpiresAt)

	// Readable and typeable: no characters that could be mistaken for one
	// another while being copied off a phone by hand.
	if len(issued.Code) != 9 || !strings.Contains(issued.Code, "-") {
		t.Errorf("code %q is not the expected shape", issued.Code)
	}
	for _, r := range strings.ReplaceAll(issued.Code, "-", "") {
		if strings.ContainsRune("ILOU", r) {
			t.Errorf("code %q contains %q, which is easily misread", issued.Code, string(r))
		}
	}

	// The person types it into the dashboard while signed in as themselves.
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")
	resp = dashboard.call("identity.approve", map[string]any{
		"code":    issued.Code,
		"session": session,
	})
	if resp.Error != nil {
		t.Fatalf("approving: %v", resp.Error)
	}

	// Now the connector can say who this is.
	resp = telegram.call("identity.resolve", map[string]any{"external_id": "tg:887312"})
	if resp.Error != nil {
		t.Fatalf("resolving after pairing: %v", resp.Error)
	}
	var resolved struct {
		UserID  string `json:"user_id"`
		Display string `json:"display"`
	}
	if err := json.Unmarshal(resp.Result, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.UserID != user.ID {
		t.Errorf("resolved to %q, want %q", resolved.UserID, user.ID)
	}
	if resolved.Display != "@mhd64" {
		t.Errorf("display = %q, want the name a person recognises", resolved.Display)
	}

	// And one screen can answer what is linked to this account.
	resp = dashboard.call("identity.links", map[string]any{"user_id": user.ID})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var listed struct {
		Links []struct {
			ID          string `json:"id"`
			ConnectorID string `json:"connector_id"`
			Display     string `json:"display"`
		} `json:"links"`
	}
	if err := json.Unmarshal(resp.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Links) != 1 || listed.Links[0].ConnectorID != "telegram" {
		t.Fatalf("links = %+v", listed.Links)
	}

	// Revoking it takes effect immediately.
	if resp := dashboard.call("identity.revoke", map[string]any{"id": listed.Links[0].ID}); resp.Error != nil {
		t.Fatalf("revoking: %v", resp.Error)
	}
	resp = telegram.call("identity.resolve", map[string]any{"external_id": "tg:887312"})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeIdentityNotLinked {
		t.Errorf("the identity still resolves after being revoked: %v", resp.Error)
	}
}

// A code is spent when used. A second approval must not create a second link.
func TestAPairingCodeWorksOnce(t *testing.T) {
	url, db := testServer(t)
	_, _ = db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:1"})
	var issued struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &issued)

	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")

	if resp := dashboard.call("identity.approve", map[string]any{
		"code": issued.Code, "session": session,
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	if resp := dashboard.call("identity.approve", map[string]any{
		"code": issued.Code, "session": session,
	}); resp.Error == nil {
		t.Error("a pairing code worked twice")
	}
}

// Unknown, expired and already-used codes are refused identically, so guessing
// gets no signal about which guess came closest.
func TestEveryBadCodeLooksTheSame(t *testing.T) {
	url, db := testServer(t)
	_, _ = db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:2"})
	var issued struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &issued)
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")
	dashboard.call("identity.approve", map[string]any{"code": issued.Code, "session": session})

	// And one that expired.
	resp = telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:3"})
	var expired struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &expired)
	if _, err := db.Exec(`UPDATE identity_link_requests SET expires_at = '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	var messages []string
	for name, code := range map[string]string{
		"already used": issued.Code,
		"expired":      expired.Code,
		"never issued": "ABCD-EFGH",
		"nonsense":     "not a code",
	} {
		resp := dashboard.call("identity.approve", map[string]any{"code": code, "session": session})
		if resp.Error == nil {
			t.Fatalf("%s code was accepted", name)
		}
		messages = append(messages, resp.Error.Message)
	}
	for _, m := range messages[1:] {
		if m != messages[0] {
			t.Errorf("bad codes are distinguishable: %q vs %q", m, messages[0])
		}
	}
}

// A code typed by hand should work however it comes out: any case, with or
// without the hyphen, with a stray space.
func TestCodesAreTypedByPeople(t *testing.T) {
	url, db := testServer(t)
	_, _ = db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")

	for i, mangle := range []func(string) string{
		strings.ToLower,
		func(s string) string { return strings.ReplaceAll(s, "-", "") },
		func(s string) string { return " " + s + " " },
	} {
		resp := telegram.call("identity.linkRequest", map[string]any{
			"external_id": "tg:typed" + string(rune('a'+i)),
		})
		var issued struct {
			Code string `json:"code"`
		}
		json.Unmarshal(resp.Result, &issued)

		resp = dashboard.call("identity.approve", map[string]any{
			"code": mangle(issued.Code), "session": session,
		})
		if resp.Error != nil {
			t.Errorf("a code typed as %q was refused: %v", mangle(issued.Code), resp.Error)
		}
	}
}

// A connector may only resolve identities in its own namespace. A Telegram
// connector must not be able to discover who a Signal identity belongs to.
func TestAConnectorCannotResolveAnotherConnectorsIdentities(t *testing.T) {
	url, db := testServer(t)
	_, _ = db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")

	signal := authedClient(t, url, db, "signal", []string{store.ScopeIdentityLink})
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := signal.call("identity.linkRequest", map[string]any{"external_id": "person:1"})
	var issued struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &issued)
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")
	dashboard.call("identity.approve", map[string]any{"code": issued.Code, "session": session})

	// Same external id, different connector.
	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})
	resp = telegram.call("identity.resolve", map[string]any{"external_id": "person:1"})
	if resp.Error == nil {
		t.Error("one connector resolved an identity belonging to another's namespace")
	}
}

// Approving asserts who a person is, so it needs more than the scope for asking
// about them.
func TestApprovingNeedsMoreThanIdentityLink(t *testing.T) {
	url, db := testServer(t)
	user, _ := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})

	resp := telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:9"})
	var issued struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &issued)

	// Holding identity.link is not enough on its own: approving needs a session,
	// and getting one needs a scope this connector does not have.
	resp = telegram.call("identity.approve", map[string]any{"code": issued.Code, "session": "made-up"})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("a made-up session was accepted: %v", resp.Error)
	}

	if resp := telegram.call("users.authenticate", map[string]any{
		"username": "mohamed", "password": "hunter2hunter2",
	}); resp.Error == nil || resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("a connector without users.authenticate hosted a sign-in: %v", resp.Error)
	}
	_ = user
}

// Re-linking moves an identity rather than failing. Somebody handing an old
// phone to a family member should not need an administrator to unpick a row.
func TestRelinkingMovesAnIdentity(t *testing.T) {
	url, db := testServer(t)
	_, _ = db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	second, _ := db.CreateUser(ctx(), "yasmin", "", "hunter2hunter2")

	telegram := authedClient(t, url, db, "telegram", []string{store.ScopeIdentityLink})
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	link := func(username string) {
		resp := telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:shared"})
		var issued struct {
			Code string `json:"code"`
		}
		json.Unmarshal(resp.Result, &issued)
		if resp := dashboard.call("identity.approve", map[string]any{
			"code": issued.Code, "session": signIn(t, dashboard, username, "hunter2hunter2"),
		}); resp.Error != nil {
			t.Fatalf("approving: %v", resp.Error)
		}
	}

	link("mohamed")
	link("yasmin")

	resp := telegram.call("identity.resolve", map[string]any{"external_id": "tg:shared"})
	var resolved struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(resp.Result, &resolved)
	if resolved.UserID != second.ID {
		t.Errorf("resolved to %q, want the account it was moved to", resolved.UserID)
	}

	links, err := db.IdentityLinks(ctx(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Errorf("%d links exist, want one: re-linking should move, not duplicate", len(links))
	}
}
