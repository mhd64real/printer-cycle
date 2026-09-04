package dashboard_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/dashboard"
)

// fakeCore stands in for core, recording what the dashboard asked it and
// answering however a test needs.
type fakeCore struct {
	mu    sync.Mutex
	calls []call
	reply map[string]any
	err   map[string]error

	// documents records what was streamed, keyed by stream id, so a test can
	// assert the bytes that reached core rather than only that a call happened.
	documents map[uint32][]byte
	sendErr   error
}

type call struct {
	method string
	params map[string]any
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		reply:     map[string]any{},
		err:       map[string]error{},
		documents: map[uint32][]byte{},
	}
}

func (f *fakeCore) Connected() bool { return true }

func (f *fakeCore) SendDocument(ctx context.Context, streamID uint32, r io.Reader) (int64, string, error) {
	if f.sendErr != nil {
		return 0, "", f.sendErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return 0, "", err
	}
	f.mu.Lock()
	f.documents[streamID] = body
	f.mu.Unlock()

	sum := sha256.Sum256(body)
	return int64(len(body)), "hex:" + hex.EncodeToString(sum[:]), nil
}

func (f *fakeCore) Call(ctx context.Context, method string, params, result any) error {
	f.mu.Lock()
	encoded, _ := json.Marshal(params)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	f.calls = append(f.calls, call{method: method, params: decoded})
	err := f.err[method]
	reply := f.reply[method]
	f.mu.Unlock()

	if err != nil {
		return err
	}
	if reply == nil || result == nil {
		return nil
	}
	data, _ := json.Marshal(reply)
	return json.Unmarshal(data, result)
}

func (f *fakeCore) lastCall(method string) (call, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].method == method {
			return f.calls[i], true
		}
	}
	return call{}, false
}

// countCalls is how many times a method has been asked for so far.
//
// Needed because signing in legitimately calls users.authenticate, so asking
// "did this method ever reach core" cannot tell a refused relay from the sign-in
// that set the test up.
func (f *fakeCore) countCalls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.method == method {
			n++
		}
	}
	return n
}

func testServer(t *testing.T) (*httptest.Server, *fakeCore) {
	t.Helper()

	core := newFakeCore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(dashboard.New(core, log).Handler())
	t.Cleanup(srv.Close)
	return srv, core
}

func post(t *testing.T, srv *httptest.Server, jar http.CookieJar, path, body string) *http.Response {
	t.Helper()

	client := &http.Client{Jar: jar}
	resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// signInAndKeepCookie signs in and returns a jar holding whatever the browser
// was given.
func signInAndKeepCookie(t *testing.T, srv *httptest.Server, core *fakeCore) http.CookieJar {
	t.Helper()

	core.reply["users.authenticate"] = map[string]any{
		"session":    "core-session-token",
		"expires_at": "2030-01-01T00:00:00Z",
		"user":       map[string]any{"id": "user_1", "username": "mohamed", "is_admin": true},
	}

	jar := newJar(t)
	resp := post(t, srv, jar, "/api/login", `{"username":"mohamed","password":"hunter2hunter2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in returned %d", resp.StatusCode)
	}
	return jar
}

// The stage's done-when, first half: nothing the browser holds is a connector
// credential.
func TestTheBrowserNeverHoldsAConnectorCredential(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	var found bool
	for _, c := range jar.Cookies(mustURL(t, srv.URL)) {
		found = true
		// The cookie carries a core session, which is bound to the dashboard
		// connector and useless to anything else. It must never carry the key
		// that makes this process the dashboard.
		if strings.Contains(c.Value, "-----BEGIN") || len(c.Value) > 200 {
			t.Errorf("cookie %q looks like it carries a key", c.Name)
		}
	}
	if !found {
		t.Fatal("signing in set no cookie at all")
	}

	// And nothing in any response body names a connector key.
	resp := post(t, srv, jar, "/api/login", `{"username":"mohamed","password":"hunter2hunter2"}`)
	body, _ := io.ReadAll(resp.Body)
	for _, forbidden := range []string{"connector.key", "private", "PRIVATE"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("a response mentions %q: %s", forbidden, body)
		}
	}
}

// The session cookie must be unreadable from the page and not sent across sites.
func TestTheSessionCookieIsProtected(t *testing.T) {
	srv, core := testServer(t)
	core.reply["users.authenticate"] = map[string]any{
		"session": "core-session-token", "expires_at": "2030-01-01T00:00:00Z",
		"user": map[string]any{"id": "user_1"},
	}

	resp := post(t, srv, nil, "/api/login", `{"username":"mohamed","password":"hunter2hunter2"}`)

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "printer_cycle_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Error("the session cookie is sent on cross-site requests")
	}
}

// A page may only ask for methods the dashboard names. A general relay would
// give any scripting flaw every power the dashboard connector holds, which is
// all of them.
func TestThePageCannotCallArbitraryMethods(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	for _, method := range []string{
		"users.authenticate", // would let a page try passwords
		"enrol",              // would let a page enrol a key of its own
		"users.signOut",
		"settings.get",
		"nonsense",
	} {
		before := core.countCalls(method)

		body, _ := json.Marshal(map[string]any{"method": method, "params": map[string]any{}})
		resp := post(t, srv, jar, "/api/call", string(body))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s returned %d, want 403", method, resp.StatusCode)
		}
		if after := core.countCalls(method); after != before {
			t.Errorf("%s reached core through the relay", method)
		}
	}
}

// The session is attached by the dashboard, not sent by the page. Otherwise a
// page could act for somebody other than whoever it is signed in as.
func TestThePageCannotSupplyItsOwnSession(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	body, _ := json.Marshal(map[string]any{
		"method": "identity.approve",
		"params": map[string]any{"session": "somebody-elses-session", "request_id": "req_1"},
	})
	post(t, srv, jar, "/api/call", string(body))

	submitted, ok := core.lastCall("identity.approve")
	if !ok {
		t.Fatal("the call never reached core")
	}
	if submitted.params["session"] != "core-session-token" {
		t.Errorf("session reached core as %v, want the one from the cookie", submitted.params["session"])
	}
}

func TestCallsRequireSigningIn(t *testing.T) {
	srv, _ := testServer(t)

	body, _ := json.Marshal(map[string]any{"method": "printers.list"})
	resp := post(t, srv, newJar(t), "/api/call", string(body))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unauthenticated call returned %d, want 401", resp.StatusCode)
	}
}

// Signing out has to end the session in core, not merely drop the cookie: a
// cookie already copied elsewhere would otherwise keep working.
func TestSigningOutEndsTheSessionInCore(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	post(t, srv, jar, "/api/logout", "")

	out, ok := core.lastCall("users.signOut")
	if !ok {
		t.Fatal("signing out did not tell core")
	}
	if out.params["session"] != "core-session-token" {
		t.Errorf("core was asked to end %v", out.params["session"])
	}
}

// A refused sign-in must not relay core's wording. Core deliberately makes a
// wrong password and an unknown name indistinguishable, and passing its message
// through would undo that.
func TestARefusedSignInSaysNothingUseful(t *testing.T) {
	srv, core := testServer(t)
	core.err["users.authenticate"] = errors.New("jsonrpc: -32002 no such user mohamed in the users table")

	resp := post(t, srv, newJar(t), "/api/login", `{"username":"mohamed","password":"wrong"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	for _, leaked := range []string{"no such user", "users table"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("the refusal leaked core's wording: %s", body)
		}
	}
}

// Core's errors are addressed half to a program and half to a person. What
// reaches a screen should be only the second half.
func TestErrorsReachThePageWithoutProtocolNoise(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	core.err["printers.add"] = errors.New("jsonrpc: -32602 no driver claims this printer")

	body, _ := json.Marshal(map[string]any{"method": "printers.add", "params": map[string]any{}})
	resp := post(t, srv, jar, "/api/call", string(body))

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "no driver claims this printer" {
		t.Errorf("error = %q, want the sentence without the protocol wrapper", payload.Error)
	}
}
