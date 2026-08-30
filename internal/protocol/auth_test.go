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
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	c.next++
	req := map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method}
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

	_, raw, err := c.ws.Read(c.ctx)
	if err != nil {
		c.t.Fatalf("reading the reply to %s: %v", method, err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.t.Fatalf("reply to %s is not JSON: %v (%s)", method, err, raw)
	}
	return resp
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
	// A connector enrolled but switched off.
	disabledKey := enrolledConnector(t, db, "disabled", nil)
	if err := db.SetConnectorEnabled(context.Background(), "disabled", false); err != nil {
		t.Fatal(err)
	}

	_, wrongKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		id  string
		key ed25519.PrivateKey
	}{
		"unknown connector":  {"no-such-connector", good},
		"never enrolled":     {"unenrolled", good},
		"disabled connector": {"disabled", disabledKey},
		"wrong key":          {"telegram", wrongKey},
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

	resp := c.call("printers.list", map[string]any{})
	if resp.Error == nil {
		t.Fatal("printers.list is not implemented yet but returned success")
	}
	if resp.Error.Code == jsonrpc.CodeNotAuthenticated {
		t.Error("still reporting not authenticated after a successful handshake")
	}
	if resp.Error.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("code = %d, want method not found", resp.Error.Code)
	}
}
