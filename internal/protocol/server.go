// Package protocol implements the connector protocol described in PROTOCOL.md.
//
// Every connector, the dashboard included, reaches core through here and only
// through here. There is no privileged path: whatever the dashboard can do, a
// connector somebody writes this evening can do too. That constraint is what
// keeps the protocol honest, so it is enforced by there being nothing else.
package protocol

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/connauth"
	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
	"github.com/mhd64real/printer-cycle/internal/version"
)

// ProtocolVersion is the version this build speaks. It appears in the URL path
// and in every hello.
const ProtocolVersion = "v1"

// ConnectorPath is where connectors connect.
const ConnectorPath = "/" + ProtocolVersion + "/connector"

// DefaultTCPAddr is the address core listens on for connectors.
//
// 6310 is deliberately adjacent to 631, the IPP port, so anyone who knows what
// this machine does can guess it. The dashboard's own web port sits next to it
// at 6311.
const DefaultTCPAddr = "0.0.0.0:6310"

// Server accepts connector connections.
type Server struct {
	db   *store.DB
	cups *ipp.Client
	log  *slog.Logger

	// discovering serialises device discovery across every connector.
	//
	// Discovery makes the SNMP backend broadcast across the subnet. Several
	// connectors discovering at once would multiply that across the network for
	// no gain, since they would all be asking the same question of the same
	// machine. Callers wait, bounded by their own context.
	discovering sync.Mutex

	mu    sync.Mutex
	conns map[*conn]struct{}
}

// Options configure a [Server].
type Options struct {
	// Logger receives connection events. Defaults to slog.Default().
	Logger *slog.Logger

	// CUPS is the printing system. Methods that need it fail cleanly when it is
	// absent, which is what lets most of the protocol be tested without one.
	CUPS *ipp.Client
}

// NewServer builds a server. It does not listen until Serve is called.
func NewServer(db *store.DB, opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		db:    db,
		cups:  opts.CUPS,
		log:   log,
		conns: make(map[*conn]struct{}),
	}
}

// Handler returns the HTTP handler serving the connector protocol.
//
// Exposed separately from Serve so tests can drive it through httptest without
// binding a port, and so an operator embedding core elsewhere can mount it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ConnectorPath, s.handleConnector)
	return mux
}

// Serve listens on every address given and serves until ctx is cancelled.
//
// An address is either a TCP host:port or a filesystem path for a Unix socket.
// Both carry the same protocol, and a connector cannot tell which it is on: the
// choice is where to deploy the connector, not a difference in what it may do.
func (s *Server) Serve(ctx context.Context, addrs ...string) error {
	if len(addrs) == 0 {
		return errors.New("protocol: no addresses to listen on")
	}

	httpServer := &http.Server{
		Handler: s.Handler(),
		// No read or write timeout: connections are long lived by design, and a
		// connector may sit silent for hours between print jobs.
		ReadHeaderTimeout: 10 * time.Second,
	}

	var listeners []net.Listener
	for _, addr := range addrs {
		ln, err := listen(addr)
		if err != nil {
			for _, l := range listeners {
				l.Close()
			}
			return err
		}
		listeners = append(listeners, ln)
		s.log.Info("listening for connectors", "address", ln.Addr().String())
	}

	errs := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(ln net.Listener) {
			err := httpServer.Serve(ln)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errs <- err
		}(ln)
	}

	<-ctx.Done()

	// Close connectors first, then the listeners.
	//
	// The other order looks equally reasonable and is not: a WebSocket
	// connection is hijacked out of net/http, so Shutdown neither closes it nor
	// wakes the handler blocked reading from it, and the whole shutdown sits
	// there until the timeout expires. Closing connections first sends every
	// connector a going-away frame, their handlers return, and Shutdown has
	// nothing left to wait for.
	s.closeAll()

	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdown)

	var firstErr error
	for range listeners {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// listen opens either a TCP listener or a Unix socket, chosen by whether the
// address looks like a path.
func listen(addr string) (net.Listener, error) {
	if !isUnixPath(addr) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("protocol: listening on %s: %w", addr, err)
		}
		return ln, nil
	}

	// Unix socket paths are capped by the kernel: 104 bytes on macOS and the
	// BSDs, 108 on Linux. Past that, connecting fails with "invalid argument",
	// which says nothing at all about the actual problem. Checking here costs
	// nothing and turns an afternoon into a sentence.
	if len(addr) >= maxUnixPath {
		return nil, fmt.Errorf(
			"protocol: socket path is %d characters, which exceeds the %d byte limit the kernel imposes: %s",
			len(addr), maxUnixPath, addr)
	}

	if err := os.MkdirAll(filepath.Dir(addr), 0o755); err != nil {
		return nil, fmt.Errorf("protocol: preparing %s: %w", addr, err)
	}
	// A socket left behind by a crash would otherwise make every restart fail
	// with "address already in use" on a file nothing is listening to.
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("protocol: clearing %s: %w", addr, err)
	}

	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("protocol: listening on %s: %w", addr, err)
	}
	// Anyone who can reach this socket can talk to core, so reaching it is
	// restricted to the user core runs as and its group.
	if err := os.Chmod(addr, 0o660); err != nil {
		ln.Close()
		return nil, fmt.Errorf("protocol: securing %s: %w", addr, err)
	}
	return ln, nil
}

// maxUnixPath is the shortest limit across the platforms this runs on: macOS
// and the BSDs allow 104 bytes, Linux 108. Using the smaller everywhere means a
// path that works on the development machine also works on the Pi.
const maxUnixPath = 104

func isUnixPath(addr string) bool {
	return len(addr) > 0 && (addr[0] == '/' || addr[0] == '.')
}

// conn is one connector connection.
type conn struct {
	ws        *websocket.Conn
	rpc       *jsonrpc.Conn
	challenge *connauth.Challenge
	db        *store.DB
	server    *Server
	log       *slog.Logger

	mu        sync.Mutex
	connector *store.Connector // nil until authenticated
}

// authTimeout is how long a connection has to authenticate before it is closed.
//
// An unauthenticated connection consumes a goroutine, a socket and a slice of
// memory while being able to do nothing at all. On a machine with 512MB, letting
// them accumulate is how an idle box runs out of room.
const authTimeout = 30 * time.Second

func (s *Server) handleConnector(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Connectors are programs, not browsers, and arrive with no Origin
		// header or an arbitrary one. Origin checking protects browsers from
		// being used as a confused deputy; it is not what protects core, which
		// is authentication.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("connector handshake failed", "error", err)
		return
	}

	challenge, err := connauth.NewChallenge()
	if err != nil {
		s.log.Error("cannot create an authentication challenge", "error", err)
		ws.Close(websocket.StatusInternalError, "internal error")
		return
	}

	c := &conn{
		ws:        ws,
		challenge: challenge,
		db:        s.db,
		server:    s,
		log:       s.log.With("remote", r.RemoteAddr),
	}
	s.track(c)
	defer s.forget(c)

	ctx := r.Context()

	c.rpc = jsonrpc.New(&wsTransport{ws: ws, log: c.log}, c)

	if err := c.sendHello(ctx); err != nil {
		c.log.Warn("cannot greet connector", "error", err)
		ws.Close(websocket.StatusInternalError, "internal error")
		return
	}

	// Close the connection if it has not authenticated in time.
	authDeadline := time.AfterFunc(authTimeout, func() {
		if c.authenticated() == nil {
			c.log.Debug("closing a connection that never authenticated")
			c.ws.Close(websocket.StatusPolicyViolation, "authentication timed out")
		}
	})
	defer authDeadline.Stop()

	if err := c.rpc.Serve(ctx); err != nil {
		c.log.Debug("connector disconnected", "error", err)
	}
}

// Handle dispatches an incoming request from a connector.
//
// Everything except authenticate is refused until the connector has proved who
// it is. Refusing by default rather than permitting by default means a method
// added later without a permission check fails closed.
func (c *conn) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == "authenticate" {
		return c.authenticate(ctx, params)
	}

	// One gate, ahead of every handler. Authentication, existence and permission
	// are all decided here, so no handler can be reached by forgetting to check
	// something inside it.
	if err := c.authorise(method); err != nil {
		return nil, err
	}

	switch method {
	case "register":
		return c.register(ctx, params)
	case "users.list":
		return c.usersList(ctx)
	case "printers.discover":
		return c.printersDiscover(ctx, params)
	}

	// Unreachable: authorise refuses anything absent from the permission table,
	// so arriving here means a method was listed there and never wired up.
	c.log.Error("a permitted method has no handler", "method", method)
	return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "internal error")
}

// register records what a connector says about itself and returns its current
// settings.
//
// Called on every connection rather than once at install, because a connector
// that has been upgraded may declare different settings, and the dashboard has
// to render what the running version actually wants rather than what an older
// one wanted.
func (c *conn) register(ctx context.Context, params json.RawMessage) (any, error) {
	connector := c.authenticated()

	var manifest store.Manifest
	if err := json.Unmarshal(params, &manifest); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "manifest is not an object")
	}

	if err := c.db.Register(ctx, connector.ID, manifest); err != nil {
		// Manifest problems are the connector's to fix, so the reason is sent
		// back rather than swallowed as an internal error. This is one of the
		// few places where saying exactly what went wrong helps the person who
		// can act on it and tells an attacker nothing.
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s", err)
	}

	// Secrets included: this is the connector reading its own settings, and a
	// connector that cannot read back its own bot token cannot use it.
	settings, err := c.db.SettingsFor(ctx, connector.ID, true)
	if err != nil {
		return nil, err
	}

	c.log.Info("connector registered",
		"name", manifest.Name, "version", manifest.Version, "settings", len(manifest.Settings))

	return map[string]any{"settings": settings}, nil
}

type authenticateParams struct {
	ConnectorID string `json:"connector_id"`
	Proof       string `json:"proof"`
}

type authenticateResult struct {
	ConnectorID string   `json:"connector_id"`
	Scopes      []string `json:"scopes"`
}

// dummyKey stands in for a connector that does not exist, so an unknown id costs
// the same work as a real one and cannot be told apart by how quickly it fails.
var dummyKey = func() ed25519.PublicKey {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic("protocol: cannot generate a dummy key: " + err.Error())
	}
	return pub
}()

// authenticate verifies a connector's signature over this connection's nonce.
//
// Every failure returns the same error. An unknown connector, a disabled one,
// one that has never enrolled a key, and a bad signature are indistinguishable
// from outside, because telling them apart would let anyone who can reach the
// port map out which connectors a household has installed.
func (c *conn) authenticate(ctx context.Context, params json.RawMessage) (any, error) {
	var p authenticateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	proof, err := base64.StdEncoding.DecodeString(p.Proof)
	if err != nil {
		// Still spend the challenge: one connection, one attempt, whatever the
		// attempt looked like.
		_ = c.challenge.Verify(dummyKey, nil)
		return nil, authFailed()
	}

	key := dummyKey
	var connector *store.Connector

	if found, err := c.db.Connector(ctx, p.ConnectorID); err == nil {
		if found.Enrolled() && found.Enabled {
			key = found.PublicKey
			connector = &found
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	if err := c.challenge.Verify(key, proof); err != nil {
		if errors.Is(err, connauth.ErrSpent) {
			return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated,
				"this connection has already made an authentication attempt")
		}
		c.log.Warn("authentication failed", "connector", p.ConnectorID)
		return nil, authFailed()
	}
	if connector == nil {
		// The signature verified against the dummy key, which cannot happen,
		// but failing closed costs nothing.
		return nil, authFailed()
	}

	c.mu.Lock()
	c.connector = connector
	c.mu.Unlock()

	if err := c.db.TouchConnector(ctx, connector.ID); err != nil {
		c.log.Warn("cannot record that a connector was seen", "connector", connector.ID, "error", err)
	}
	c.log = c.log.With("connector", connector.ID)
	c.log.Info("connector authenticated", "scopes", len(connector.Scopes))

	scopes := connector.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return authenticateResult{ConnectorID: connector.ID, Scopes: scopes}, nil
}

func authFailed() error {
	return jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "authentication failed")
}

// authenticated returns the connector this connection proved to be, or nil.
func (c *conn) authenticated() *store.Connector {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connector
}

// wsTransport carries JSON-RPC over a WebSocket, and keeps documents out of it.
//
// Text frames are protocol messages. Binary frames are document data, which
// never travels inside JSON: base64 would inflate it by a third and force the
// whole file into memory, which on a 512MB machine is an out-of-memory kill
// rather than an inefficiency. They are routed away here so the message layer
// only ever sees messages.
type wsTransport struct {
	ws  *websocket.Conn
	log *slog.Logger
}

func (t *wsTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	for {
		kind, data, err := t.ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if kind == websocket.MessageText {
			return data, nil
		}
		// Document streaming is Stage 35. Until then a binary frame is a
		// connector doing something core cannot yet honour, and dropping the
		// connection over it would be worse than saying so and carrying on.
		t.log.Warn("ignoring a binary frame: document streaming is not implemented yet",
			"bytes", len(data))
	}
}

func (t *wsTransport) WriteMessage(ctx context.Context, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return t.ws.Write(writeCtx, websocket.MessageText, data)
}

// hello is the greeting every connection opens with, carrying the nonce the
// connector signs to authenticate.
type hello struct {
	Protocol    string   `json:"protocol"`
	CoreVersion string   `json:"core_version"`
	Nonce       string   `json:"nonce"`
	Auth        []string `json:"auth"`
}

func (c *conn) sendHello(ctx context.Context) error {
	params, err := json.Marshal(hello{
		Protocol:    ProtocolVersion,
		CoreVersion: version.Version,
		Nonce:       base64.StdEncoding.EncodeToString(c.challenge.Nonce()),
		Auth:        []string{"ed25519"},
	})
	if err != nil {
		return err
	}

	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "hello",
		"params":  json.RawMessage(params),
	})
	if err != nil {
		return err
	}

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(writeCtx, websocket.MessageText, msg)
}

func (s *Server) track(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[c] = struct{}{}
}

func (s *Server) forget(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

// closeAll tells every connector core is going away, without letting any of
// them delay it.
//
// The closing handshake is a round trip: it sends a close frame and waits for
// the peer to send one back. A connector with a healthy read loop answers at
// once. One that has crashed, hung, or stopped reading never answers, and the
// library waits out its own timeout, which is several seconds.
//
// So every connection is closed concurrently and the whole operation is bounded.
// Anything still waiting when the grace period ends is abandoned: the process is
// stopping, and the socket goes with it.
//
// Note that CloseNow is not used as a fallback here, which was the obvious first
// attempt. It serialises behind the Close already in flight on the same
// connection, so it waits exactly as long as the thing it was meant to cut short.
func (s *Server) closeAll() {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if len(conns) == 0 {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, c := range conns {
			wg.Add(1)
			go func(c *conn) {
				defer wg.Done()
				_ = c.ws.Close(websocket.StatusGoingAway, "core is shutting down")
			}(c)
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(closeGrace):
		s.log.Debug("some connectors did not acknowledge shutdown in time")
	}
}

// closeGrace is how long connectors collectively get to acknowledge a shutdown.
const closeGrace = 500 * time.Millisecond
