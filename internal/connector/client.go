// Package connector is the connector side of the protocol.
//
// Used by the dashboard, which is a connector like any other. A third party is
// welcome to use it too, and equally welcome to ignore it: the protocol is
// documented in PROTOCOL.md and implementable in about fifty lines in any
// language. Nothing here is privileged.
package connector

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/connauth"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
)

// Options configure a [Client].
type Options struct {
	// ID is the connector identity to authenticate as.
	ID string

	// CoreURL is where core listens, for example ws://127.0.0.1:6310.
	CoreURL string

	// KeyPath is where this connector's private key lives. Generated on first
	// run if absent.
	KeyPath string

	// SetupToken enrols this connector's key. Needed once; ignored afterwards.
	SetupToken string

	// Manifest is sent on every connection, so core and the dashboard describe
	// the version that is actually running rather than the one first installed.
	Manifest any

	// OnNotify receives everything core pushes. May be nil.
	OnNotify func(method string, params json.RawMessage)

	// OnConnect runs after a successful handshake, with the settings core
	// returned from registration.
	OnConnect func(settings map[string]any)

	Logger *slog.Logger
}

// Client keeps a connector connected to core.
type Client struct {
	opts Options
	log  *slog.Logger
	key  ed25519.PrivateKey

	mu   sync.RWMutex
	rpc  *jsonrpc.Conn
	live bool
}

// New loads or creates this connector's key.
func New(opts Options) (*Client, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ID == "" || opts.CoreURL == "" || opts.KeyPath == "" {
		return nil, errors.New("connector: id, core url and key path are all required")
	}

	key, err := loadOrCreateKey(opts.KeyPath)
	if err != nil {
		return nil, err
	}
	return &Client{opts: opts, log: opts.Logger, key: key}, nil
}

// PublicKey is what core stores. Nothing here can impersonate this connector.
func (c *Client) PublicKey() ed25519.PublicKey {
	return c.key.Public().(ed25519.PublicKey)
}

// Run keeps the connection up until ctx ends, reconnecting when it drops.
//
// Core restarting, a network blip, the machine sleeping: all ordinary, and a
// connector that gave up on the first of them would need a person to notice and
// restart it.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Enrolling is an expected step on a first run, not a fault. Reporting
		// it as a disconnection would make somebody's first experience of the
		// software look like something had gone wrong.
		if errors.Is(err, errRetryAfterEnrol) {
			c.log.Info("enrolled with core, connecting")
			backoff = time.Second
			continue
		}

		c.log.Warn("disconnected from core, retrying", "error", err, "in", backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// session runs one connection from handshake to disconnection.
func (c *Client) session(ctx context.Context) error {
	ws, _, err := websocket.Dial(ctx, c.opts.CoreURL+"/v1/connector", nil)
	if err != nil {
		return fmt.Errorf("connector: dialling core: %w", err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4 << 20)

	nonce, err := readHello(ctx, ws)
	if err != nil {
		return err
	}

	rpc := jsonrpc.New(&wsTransport{ws: ws}, jsonrpc.HandlerFunc(
		func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			// Core sends notifications rather than requests, so anything with an
			// id is something this build does not know about.
			if c.opts.OnNotify != nil {
				c.opts.OnNotify(method, params)
			}
			return nil, nil
		}))

	serveErr := make(chan error, 1)
	go func() { serveErr <- rpc.Serve(ctx) }()

	if err := c.handshake(ctx, rpc, nonce); err != nil {
		return err
	}

	c.setLive(rpc, true)
	defer c.setLive(nil, false)

	return <-serveErr
}

// handshake authenticates, enrolling first if core does not know this key yet.
func (c *Client) handshake(ctx context.Context, rpc *jsonrpc.Conn, nonce []byte) error {
	proof, err := connauth.Sign(c.key, nonce)
	if err != nil {
		return err
	}

	err = rpc.Call(ctx, "authenticate", map[string]any{
		"connector_id": c.opts.ID,
		"proof":        base64.StdEncoding.EncodeToString(proof),
	}, nil)

	if err != nil {
		// A connector on its first run has a key core has never seen. If there
		// is a setup token, spend it and try once more on this same connection:
		// enrolling does not consume the authentication challenge.
		if c.opts.SetupToken == "" {
			return fmt.Errorf("connector: core would not accept this connector: %w", err)
		}

		c.log.Info("not known to core yet, enrolling")
		if err := rpc.Call(ctx, "enrol", map[string]any{
			"token":      c.opts.SetupToken,
			"public_key": base64.StdEncoding.EncodeToString(c.PublicKey()),
		}, nil); err != nil {
			return fmt.Errorf("connector: enrolling: %w", err)
		}
		// The challenge for this connection is spent, so the next attempt needs
		// a fresh one.
		return errRetryAfterEnrol
	}

	var registered struct {
		Settings map[string]any `json:"settings"`
	}
	if c.opts.Manifest != nil {
		if err := rpc.Call(ctx, "register", c.opts.Manifest, &registered); err != nil {
			return fmt.Errorf("connector: registering: %w", err)
		}
	}

	c.log.Info("connected to core", "connector", c.opts.ID)
	if c.opts.OnConnect != nil {
		c.opts.OnConnect(registered.Settings)
	}
	return nil
}

// errRetryAfterEnrol asks Run for a fresh connection, which is the cheapest way
// to obtain a fresh authentication nonce after spending one.
var errRetryAfterEnrol = errors.New("connector: enrolled, reconnecting to authenticate")

// Call makes a request to core.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.RLock()
	rpc, live := c.rpc, c.live
	c.mu.RUnlock()

	if !live {
		return errors.New("connector: not connected to core")
	}
	return rpc.Call(ctx, method, params, result)
}

// Connected reports whether the link to core is up.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.live
}

func (c *Client) setLive(rpc *jsonrpc.Conn, live bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rpc, c.live = rpc, live
}

// readHello waits for core's greeting and returns the nonce to sign.
func readHello(ctx context.Context, ws *websocket.Conn) ([]byte, error) {
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, data, err := ws.Read(readCtx)
	if err != nil {
		return nil, fmt.Errorf("connector: waiting for core's greeting: %w", err)
	}

	var hello struct {
		Method string `json:"method"`
		Params struct {
			Protocol string   `json:"protocol"`
			Nonce    string   `json:"nonce"`
			Auth     []string `json:"auth"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &hello); err != nil {
		return nil, fmt.Errorf("connector: core's greeting is not JSON: %w", err)
	}
	if hello.Method != "hello" {
		return nil, fmt.Errorf("connector: expected a greeting, got %q", hello.Method)
	}

	nonce, err := base64.StdEncoding.DecodeString(hello.Params.Nonce)
	if err != nil {
		return nil, fmt.Errorf("connector: greeting nonce is not base64: %w", err)
	}
	return nonce, nil
}

// wsTransport carries JSON-RPC over the WebSocket, keeping document frames out
// of the message layer.
type wsTransport struct {
	ws *websocket.Conn
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
		// Core sends no binary frames today. Ignoring rather than failing means a
		// future one does not break connectors built against this.
	}
}

func (t *wsTransport) WriteMessage(ctx context.Context, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return t.ws.Write(writeCtx, websocket.MessageText, data)
}

// loadOrCreateKey reads this connector's private key, generating one on first
// run.
//
// The file is the connector's identity. Anyone who can read it can be this
// connector, so it is written readable by nobody else, and never leaves the
// process otherwise.
func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, decodeErr := base64.StdEncoding.DecodeString(string(data))
		if decodeErr != nil || len(key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("connector: %s is not a usable key", path)
		}
		return ed25519.PrivateKey(key), nil

	case !os.IsNotExist(err):
		return nil, fmt.Errorf("connector: reading %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("connector: preparing %s: %w", filepath.Dir(path), err)
	}

	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("connector: generating a key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("connector: writing %s: %w", path, err)
	}
	return key, nil
}
