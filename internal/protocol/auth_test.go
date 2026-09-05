package protocol_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/connauth"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/protocol"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// client is a minimal connector, written the way a third party would have to
// write one: dial, read the greeting, sign the nonce, call authenticate.
type client struct {
	t     *testing.T
	ws    *websocket.Conn
	ctx   context.Context
	nonce []byte
	next  int
}

func dial(t *testing.T, url string) *client {
	return dialWithTimeout(t, url, 15*time.Second)
}

func dialWithTimeout(t *testing.T, url string, timeout time.Duration) *client {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })

	c := &client{t: t, ws: ws, ctx: ctx}

	g := readHello(t, ctx, ws)
	nonce, err := base64.StdEncoding.DecodeString(g.Params.Nonce)
	if err != nil {
		t.Fatalf("nonce is not base64: %v", err)
	}
	c.nonce = nonce
	return c
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonrpc.Error  `json:"error"`
}

func (c *client) call(method string, params any) rpcResponse {
	c.t.Helper()
	return c.callCollecting(method, params, nil)
}

// callCollecting makes a call while notifications may be arriving.
//
// A connector cannot assume the next frame is its reply: core pushes discovery
// results and job progress whenever it likes. Anything arriving before the reply
// is handed to onNotify, which is what a real connector's read loop does.
func (c *client) callCollecting(method string, params any, onNotify func(method string, params json.RawMessage)) rpcResponse {
	c.t.Helper()

	c.next++
	id := c.next
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.ws.Write(c.ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("writing %s: %v", method, err)
	}

	for {
		_, raw, err := c.ws.Read(c.ctx)
		if err != nil {
			c.t.Fatalf("reading the reply to %s: %v", method, err)
		}

		var probe struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			c.t.Fatalf("frame is not JSON: %v (%s)", err, raw)
		}

		if probe.Method != "" && probe.ID == nil {
			if onNotify != nil {
				onNotify(probe.Method, probe.Params)
			}
			continue
		}
		if probe.ID == nil || *probe.ID != id {
			continue
		}

		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			c.t.Fatalf("reply to %s is not JSON: %v (%s)", method, err, raw)
		}
		return resp
	}
}

// awaitNotification reads frames until one satisfies want, or the deadline.
func (c *client) awaitNotification(want func(method string, params json.RawMessage) bool, timeout time.Duration) {
	c.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		readCtx, cancel := context.WithDeadline(c.ctx, deadline)
		_, raw, err := c.ws.Read(readCtx)
		cancel()
		if err != nil {
			c.t.Fatalf("waiting for a notification: %v", err)
		}

		var probe struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		if probe.Method == "" || probe.ID != nil {
			continue
		}
		if want(probe.Method, probe.Params) {
			return
		}
	}
	c.t.Fatalf("no matching notification within %v", timeout)
}

func (c *client) proof(key ed25519.PrivateKey) string {
	c.t.Helper()
	sig, err := connauth.Sign(key, c.nonce)
	if err != nil {
		c.t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// enrolledConnector registers a connector, enrols a key, and enables it.
func enrolledConnector(t *testing.T, db *store.DB, id string, scopes []string) ed25519.PrivateKey {
	t.Helper()

	if _, err := db.CreateConnector(context.Background(), id, id, scopes); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewEnrolmentToken(context.Background(), id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Enrol(context.Background(), token, pub); err != nil {
		t.Fatal(err)
	}
	if err := db.SetConnectorEnabled(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	return priv
}

func testServer(t *testing.T) (string, *store.DB) {
	t.Helper()
	s, db := newServer(t)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + protocol.ConnectorPath, db
}

func TestAuthenticateWithAValidSignature(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", []string{store.ScopeJobsSubmit, store.ScopeJobsRead})

	c := dial(t, url)
	resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram",
		"proof":        c.proof(key),
	})
	if resp.Error != nil {
		t.Fatalf("authentication failed: %v", resp.Error)
	}

	var result struct {
		ConnectorID string   `json:"connector_id"`
		Scopes      []string `json:"scopes"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ConnectorID != "telegram" {
		t.Errorf("connector_id = %q", result.ConnectorID)
	}
	if len(result.Scopes) != 2 {
		t.Errorf("scopes = %v, want the two granted", result.Scopes)
	}
}

// The point of the stage: nothing but authenticate works beforehand.
func TestNothingButAuthenticateWorksUnauthenticated(t *testing.T) {
	url, _ := testServer(t)

	for _, method := range []string{
		"register", "printers.list", "printers.discover", "jobs.submit",
		"identity.resolve", "settings.get", "users.list",
	} {
		c := dial(t, url)
		resp := c.call(method, map[string]any{})
		if resp.Error == nil {
			t.Errorf("%s succeeded without authentication", method)
			continue
		}
		if resp.Error.Code != jsonrpc.CodeNotAuthenticated {
			t.Errorf("%s gave code %d, want %d", method, resp.Error.Code, jsonrpc.CodeNotAuthenticated)
		}
	}
}

// Every way of failing looks the same from outside. Telling them apart would let
// anyone who can reach the port map out which connectors a household runs.
func TestEveryAuthenticationFailureLooksIdentical(t *testing.T) {
	url, db := testServer(t)

	good := enrolledConnector(t, db, "telegram", nil)

	// A connector that exists but has never enrolled a key.
	if _, err := db.CreateConnector(context.Background(), "unenrolled", "", nil); err != nil {
		t.Fatal(err)
	}
	// A connector enrolled but switched off. Its own key is not needed here:
	// what is being checked is that somebody who cannot sign for it learns
	// nothing from it existing.
	enrolledConnector(t, db, "disabled", nil)
	if err := db.SetConnectorEnabled(context.Background(), "disabled", false); err != nil {
		t.Fatal(err)
	}

	_, wrongKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// Every case here is somebody who has NOT proved possession of a valid key,
	// and they must all look the same. A connector that is switched off is not
	// in this list: it proves who it is first and is then told plainly, which
	// is TestASwitchedOffConnectorIsToldSo below. Whoever cannot sign still
	// cannot tell a disabled connector from one that never existed, because
	// they never get as far as the answer.
	cases := map[string]struct {
		id  string
		key ed25519.PrivateKey
	}{
		"unknown connector":     {"no-such-connector", good},
		"never enrolled":        {"unenrolled", good},
		"wrong key":             {"telegram", wrongKey},
		"disabled, wrong key":   {"disabled", wrongKey},
		"disabled, other's key": {"disabled", good},
	}

	var messages []string
	for name, tc := range cases {
		c := dial(t, url)
		resp := c.call("authenticate", map[string]any{
			"connector_id": tc.id,
			"proof":        c.proof(tc.key),
		})
		if resp.Error == nil {
			t.Fatalf("%s authenticated successfully", name)
		}
		if resp.Error.Code != jsonrpc.CodeNotAuthenticated {
			t.Errorf("%s gave code %d", name, resp.Error.Code)
		}
		messages = append(messages, resp.Error.Message)
	}

	for _, m := range messages[1:] {
		if m != messages[0] {
			t.Errorf("failures are distinguishable: %q vs %q", m, messages[0])
		}
	}
}

// A connector that is switched off has to be told that, rather than that its
// signature was wrong.
//
// It used to be told the latter. Being turned off is the ordinary state of
// every newly enrolled connector, so the first thing a connector author saw
// after getting their signing code exactly right was "authentication failed",
// with nothing anywhere pointing at the switch. The check happens after the
// signature verifies, so only somebody who has already proved who they are ever
// sees this, and they learn nothing they could not ask an administrator.
func TestASwitchedOffConnectorIsToldSo(t *testing.T) {
	url, db := testServer(t)

	key := enrolledConnector(t, db, "sleeping", nil)
	if err := db.SetConnectorEnabled(context.Background(), "sleeping", false); err != nil {
		t.Fatal(err)
	}

	c := dial(t, url)
	resp := c.call("authenticate", map[string]any{
		"connector_id": "sleeping",
		"proof":        c.proof(key),
	})
	if resp.Error == nil {
		t.Fatal("a switched off connector authenticated")
	}
	if resp.Error.Code != jsonrpc.CodeNotAuthenticated {
		t.Errorf("code = %d, want not authenticated", resp.Error.Code)
	}
	if !strings.Contains(strings.ToLower(resp.Error.Message), "turned off") {
		t.Errorf("message = %q, which does not say it is switched off", resp.Error.Message)
	}
}

// One connection, one attempt. A connection that stayed usable after a failed
// try would turn into an unlimited guessing seat.
func TestOneAttemptPerConnection(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", nil)

	c := dial(t, url)

	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	if resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram",
		"proof":        c.proof(wrongKey),
	}); resp.Error == nil {
		t.Fatal("a wrong key authenticated")
	}

	resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram",
		"proof":        c.proof(key),
	})
	if resp.Error == nil {
		t.Fatal("a second attempt on the same connection succeeded")
	}
}

// A proof from one connection must be worthless on another, which is the whole
// reason the nonce is per connection.
func TestAProofCannotBeReplayedOnANewConnection(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", nil)

	first := dial(t, url)
	stolen := first.proof(key)

	second := dial(t, url)
	resp := second.call("authenticate", map[string]any{
		"connector_id": "telegram",
		"proof":        stolen,
	})
	if resp.Error == nil {
		t.Fatal("a proof captured from another connection was accepted")
	}
}

func TestMalformedAuthenticateParams(t *testing.T) {
	url, _ := testServer(t)

	c := dial(t, url)
	resp := c.call("authenticate", "not an object")
	if resp.Error == nil {
		t.Fatal("nonsense params were accepted")
	}
	if resp.Error.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("code = %d, want invalid params", resp.Error.Code)
	}
}

// After authenticating, other methods stop returning "not authenticated". They
// do not work yet, but the reason they fail has to change.
func TestAuthenticationChangesWhatIsRefused(t *testing.T) {
	url, db := testServer(t)
	key := enrolledConnector(t, db, "telegram", nil)

	c := dial(t, url)
	if resp := c.call("authenticate", map[string]any{
		"connector_id": "telegram",
		"proof":        c.proof(key),
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	// A name deliberately chosen never to exist. An earlier version used a real
	// method that had not been built yet, which made the test expire the moment
	// it was: a test asserting a feature is absent has a shelf life.
	resp := c.call("no.such.method.will.ever.exist", map[string]any{})
	if resp.Error == nil {
		t.Fatal("a method that does not exist returned success")
	}
	if resp.Error.Code == jsonrpc.CodeNotAuthenticated {
		t.Error("still reporting not authenticated after a successful handshake")
	}
	if resp.Error.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("code = %d, want method not found", resp.Error.Code)
	}
}
