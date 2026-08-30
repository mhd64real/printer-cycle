package protocol_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/connauth"
	"github.com/mhd64real/printer-cycle/internal/protocol"
	"github.com/mhd64real/printer-cycle/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newServer(t *testing.T) (*protocol.Server, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "printer-cycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return protocol.NewServer(db, protocol.Options{Logger: quietLogger()}), db
}

type greeting struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Protocol    string   `json:"protocol"`
		CoreVersion string   `json:"core_version"`
		Nonce       string   `json:"nonce"`
		Auth        []string `json:"auth"`
	} `json:"params"`
}

func readHello(t *testing.T, ctx context.Context, ws *websocket.Conn) greeting {
	t.Helper()

	kind, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("reading the greeting: %v", err)
	}
	// Control messages travel as text; only documents are binary.
	if kind != websocket.MessageText {
		t.Fatalf("greeting arrived as %v, want a text frame", kind)
	}

	var g greeting
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("greeting is not JSON: %v (%s)", err, data)
	}
	return g
}

func TestConnectorIsGreetedOnConnect(t *testing.T) {
	s, _ := newServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+protocol.ConnectorPath, nil)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	g := readHello(t, ctx, ws)

	if g.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", g.JSONRPC)
	}
	if g.Method != "hello" {
		t.Errorf("method = %q, want hello", g.Method)
	}
	if g.Params.Protocol != protocol.ProtocolVersion {
		t.Errorf("protocol = %q, want %q", g.Params.Protocol, protocol.ProtocolVersion)
	}
	if len(g.Params.Auth) != 1 || g.Params.Auth[0] != "ed25519" {
		t.Errorf("auth = %v, want [ed25519]", g.Params.Auth)
	}

	// The nonce is plain base64, with no "b64:" ornament, and is the size
	// connauth expects. A connector that cannot decode it cannot authenticate.
	nonce, err := base64.StdEncoding.DecodeString(g.Params.Nonce)
	if err != nil {
		t.Fatalf("nonce is not plain base64: %v (%q)", err, g.Params.Nonce)
	}
	if len(nonce) != connauth.NonceSize {
		t.Errorf("nonce is %d bytes, want %d", len(nonce), connauth.NonceSize)
	}
}

// Every connection gets its own nonce. A shared one would make a captured proof
// replayable forever.
func TestEachConnectionGetsItsOwnNonce(t *testing.T) {
	s, _ := newServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + protocol.ConnectorPath

	seen := map[string]bool{}
	for range 5 {
		ws, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		g := readHello(t, ctx, ws)
		ws.Close(websocket.StatusNormalClosure, "")

		if seen[g.Params.Nonce] {
			t.Fatal("two connections were given the same nonce")
		}
		seen[g.Params.Nonce] = true
	}
}

// The same protocol on both transports, so a connector cannot tell which it is
// on. Where a connector runs is a deployment choice, not a difference in what it
// is allowed to do.
func TestUnixSocketAndTCPBehaveIdentically(t *testing.T) {
	s, _ := newServer(t)

	// Not t.TempDir(): it embeds the test name, and the result overruns the
	// kernel's Unix socket path limit on macOS.
	socket := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(ctx, "127.0.0.1:0", socket) }()

	// Wait for the socket to appear rather than guessing at a delay.
	var dialer *websocket.DialOptions
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socket); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	dialer = &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		},
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	ws, _, err := websocket.Dial(dialCtx, "ws://localhost"+protocol.ConnectorPath, dialer)
	if err != nil {
		t.Fatalf("dialling over the unix socket: %v", err)
	}

	g := readHello(t, dialCtx, ws)
	if g.Method != "hello" {
		t.Errorf("method over the unix socket = %q, want hello", g.Method)
	}
	if g.Params.Protocol != protocol.ProtocolVersion {
		t.Errorf("protocol over the unix socket = %q", g.Params.Protocol)
	}

	// Shutting down with the connection still open, on purpose: a connector
	// that is mid-conversation when core stops is the normal case, and it must
	// not make shutdown wait.
	start := time.Now()
	cancel()
	if err := <-serveErr; err != nil {
		t.Errorf("Serve returned %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("shutdown took %v with one connector attached; it should not wait for them", elapsed.Round(time.Millisecond))
	}
	ws.Close(websocket.StatusNormalClosure, "")
}

// A stale socket left by a crash must not stop core from starting. Otherwise a
// power cut leaves a box that will not come back up.
func TestServeReplacesAStaleSocket(t *testing.T) {
	s, _ := newServer(t)

	socket := shortSocketPath(t)
	if err := writeFile(socket); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(ctx, socket) }()

	deadline := time.Now().Add(5 * time.Second)
	var connected bool
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socket); err == nil {
			conn.Close()
			connected = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-serveErr

	if !connected {
		t.Error("core would not start with a stale socket in place")
	}
}

func TestServeRefusesNoAddresses(t *testing.T) {
	s, _ := newServer(t)
	if err := s.Serve(context.Background()); err == nil {
		t.Error("Serve with no addresses returned no error")
	}
}

// A socket path longer than the kernel allows must be refused with an
// explanation, not with "invalid argument" from deep inside the dial.
func TestOverlongSocketPathIsRefusedClearly(t *testing.T) {
	s, _ := newServer(t)

	long := "/tmp/" + strings.Repeat("d", 120) + ".sock"
	err := s.Serve(context.Background(), long)
	if err == nil {
		t.Fatal("an impossible socket path was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error does not explain the length limit: %v", err)
	}
}
