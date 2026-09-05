package protocol_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

func TestSigningInAndOut(t *testing.T) {
	url, db := testServer(t)
	user, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := dashboard.call("users.authenticate", map[string]any{
		"username": "mohamed", "password": "hunter2hunter2",
	})
	if resp.Error != nil {
		t.Fatalf("signing in: %v", resp.Error)
	}
	var out struct {
		Session string `json:"session"`
		User    struct {
			ID      string `json:"id"`
			IsAdmin bool   `json:"is_admin"`
		} `json:"user"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != user.ID || !out.User.IsAdmin {
		t.Errorf("signed in as %+v", out.User)
	}

	// Nothing resembling a credential comes back with the session.
	if containsCredentialWords(string(resp.Result)) {
		t.Errorf("the sign-in reply mentions credentials: %s", resp.Result)
	}

	// Core can now say who this is, without any connector asserting it.
	resp = dashboard.call("users.whoami", map[string]any{"session": out.Session})
	if resp.Error != nil {
		t.Fatalf("whoami: %v", resp.Error)
	}
	var who struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	json.Unmarshal(resp.Result, &who)
	if who.ID != user.ID || who.Username != "mohamed" {
		t.Errorf("whoami = %+v", who)
	}

	// Signing out ends it.
	if resp := dashboard.call("users.signOut", map[string]any{"session": out.Session}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := dashboard.call("users.whoami", map[string]any{"session": out.Session}); resp.Error == nil {
		t.Error("a session still works after signing out")
	}
}

// A session belongs to the connector that issued it. Otherwise one seen going
// past could be replayed by anything else that holds a connection.
func TestASessionIsUsableOnlyByTheConnectorThatIssuedIt(t *testing.T) {
	url, db := testServer(t)
	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")

	other := authedClient(t, url, db, "telegram", store.KnownScopes())
	resp := other.call("users.whoami", map[string]any{"session": session})
	if resp.Error == nil {
		t.Fatal("a session issued to one connector was accepted from another")
	}
	if resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("code = %d, want not authenticated", resp.Error.Code)
	}
}

// Issuing a session from a password makes this an oracle unless attempts are
// limited. Argon2 makes each guess cost real time, which is a tax on an attacker
// and no defence against a patient one.
func TestFailedSignInsAreThrottled(t *testing.T) {
	url, db := testServer(t)
	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	var throttled bool
	for range 12 {
		resp := dashboard.call("users.authenticate", map[string]any{
			"username": "mohamed", "password": "wrong",
		})
		if resp.Error == nil {
			t.Fatal("a wrong password was accepted")
		}
		if resp.Error.Message == "too many attempts, try again shortly" {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("twelve wrong passwords in a row were all allowed through")
	}

	// Even the correct password is refused while throttled, which is the point:
	// somebody guessing does not get a free pass by happening to land on it.
	resp := dashboard.call("users.authenticate", map[string]any{
		"username": "mohamed", "password": "hunter2hunter2",
	})
	if resp.Error == nil {
		t.Error("throttling let the correct password straight through")
	}
}

// Changing a password is what somebody does when they think it is known.
// Leaving existing sessions alive would mean the change achieves nothing against
// the person they are worried about.
func TestChangingAPasswordEndsExistingSessions(t *testing.T) {
	url, db := testServer(t)
	user, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")

	if resp := dashboard.call("users.whoami", map[string]any{"session": session}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	if err := db.SetPassword(ctx(), user.ID, "a-completely-new-password"); err != nil {
		t.Fatal(err)
	}

	if resp := dashboard.call("users.whoami", map[string]any{"session": session}); resp.Error == nil {
		t.Error("a session survived the password being changed")
	}
}

func TestMadeUpSessionsAreRefused(t *testing.T) {
	url, db := testServer(t)
	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())

	for _, session := range []string{"", "   ", "not-a-session", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		resp := dashboard.call("users.whoami", map[string]any{"session": session})
		if resp.Error == nil {
			t.Errorf("session %q was accepted", session)
		}
	}
}

// Hosting a sign-in is its own permission: listing who has an account and being
// allowed to try their passwords are different powers.
func TestSigningInNeedsItsOwnScope(t *testing.T) {
	url, db := testServer(t)
	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	// A connector that may read the user list and nothing more.
	reader := authedClient(t, url, db, "reader", []string{store.ScopeUsersRead})

	if resp := reader.call("users.list", nil); resp.Error != nil {
		t.Fatalf("users.read did not allow listing users: %v", resp.Error)
	}

	resp := reader.call("users.authenticate", map[string]any{
		"username": "mohamed", "password": "hunter2hunter2",
	})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("a connector holding only users.read tried passwords: %v", resp.Error)
	}
}

func containsCredentialWords(s string) bool {
	for _, w := range []string{"password", "argon2", "hunter2"} {
		if len(w) > 0 && stringsContains(s, w) {
			return true
		}
	}
	return false
}

// Removing an account is an administrator's decision, proven by their session.
// A connector holding users.manage is not a person and cannot be one.
func TestOnlyAnAdministratorRemovesAnAccount(t *testing.T) {
	url, db := testServer(t)
	for _, name := range []string{"mohamed", "sara"} {
		if _, err := db.CreateUser(ctx(), name, "", "hunter2hunter2"); err != nil {
			t.Fatal(err)
		}
	}

	c := authedClient(t, url, db, "dashboard", store.KnownScopes())
	admin := signIn(t, c, "mohamed", "hunter2hunter2")
	ordinary := signIn(t, c, "sara", "hunter2hunter2")

	sara, err := db.UserByUsername(ctx(), "sara")
	if err != nil {
		t.Fatal(err)
	}
	mohamed, err := db.UserByUsername(ctx(), "mohamed")
	if err != nil {
		t.Fatal(err)
	}

	// The scope alone is not enough: no session, no removal.
	if resp := c.call("users.remove", map[string]any{"user_id": sara.ID}); resp.Error == nil {
		t.Error("an account was removed with no session")
	}

	// Nor is being signed in as somebody ordinary.
	if resp := c.call("users.remove", map[string]any{
		"user_id": mohamed.ID, "session": ordinary,
	}); resp.Error == nil {
		t.Error("a non-administrator removed an account")
	}

	if resp := c.call("users.remove", map[string]any{
		"user_id": sara.ID, "session": admin,
	}); resp.Error != nil {
		t.Fatalf("an administrator could not remove an account: %v", resp.Error)
	}

	if _, err := db.UserByUsername(ctx(), "sara"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the account is still there: %v", err)
	}
}

// The last administrator cannot be removed, or nobody could manage the box.
func TestTheLastAdministratorStays(t *testing.T) {
	url, db := testServer(t)
	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	c := authedClient(t, url, db, "dashboard", store.KnownScopes())
	admin := signIn(t, c, "mohamed", "hunter2hunter2")

	mohamed, err := db.UserByUsername(ctx(), "mohamed")
	if err != nil {
		t.Fatal(err)
	}

	resp := c.call("users.remove", map[string]any{"user_id": mohamed.ID, "session": admin})
	if resp.Error == nil {
		t.Fatal("the only administrator removed themselves")
	}
	if !strings.Contains(resp.Error.Message, "administrator") {
		t.Errorf("refused with %q, which does not explain why", resp.Error.Message)
	}
}
