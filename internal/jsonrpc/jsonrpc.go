// Package jsonrpc carries JSON-RPC 2.0 in both directions over one connection.
//
// Both peers are equals here. Core calls connectors as readily as connectors
// call core, which is the requirement that ruled out plain request-and-response
// designs when the protocol was chosen: a finished print job has to reach the
// connector that submitted it without the connector asking.
package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
)

// Version is the only JSON-RPC version this speaks.
const Version = "2.0"

// Standard JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// printer-cycle's own error codes, from PROTOCOL.md section 3.
const (
	CodeScopeDenied       = -32001
	CodeNotAuthenticated  = -32002
	CodeUnknownPrinter    = -32003
	CodeUnknownJob        = -32004
	CodeUnknownStream     = -32005
	CodeIdentityNotLinked = -32006
	CodePayloadRejected   = -32007
	CodeDriverRequired    = -32008
)

// Transport carries whole messages.
//
// Deliberately not an io.ReadWriter. The underlying carrier is message
// oriented, and an implementation is expected to route anything that is not a
// protocol message, such as the binary frames documents travel in, before it
// ever reaches here.
type Transport interface {
	ReadMessage(ctx context.Context) ([]byte, error)
	WriteMessage(ctx context.Context, data []byte) error
}

// Error is a JSON-RPC error object, and also a Go error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("jsonrpc: %d %s: %s", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("jsonrpc: %d %s", e.Code, e.Message)
}

// Errorf builds an Error.
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// message is one JSON-RPC message. Requests, responses and notifications share
// a shape on the wire, which is why one struct covers all three.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Handler processes incoming requests and notifications.
//
// A nil error sends result back to the caller. An *Error is sent verbatim, so
// handlers control the code the other side sees. Any other error becomes an
// internal error, with its text withheld: an error from deep inside core is
// written for whoever reads the log, not for a connector on the network.
//
// For a notification the return values are discarded, since there is nobody to
// send them to.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// HandlerFunc adapts a function to [Handler].
type HandlerFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

func (f HandlerFunc) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return f(ctx, method, params)
}

// Conn is a JSON-RPC connection.
type Conn struct {
	transport Transport
	handler   Handler

	nextID atomic.Uint64

	// writeMu serialises writes.
	//
	// Two reasons. A transport frame must not interleave with another, and the
	// protocol promises connectors that job updates arrive in order, which holds
	// only if the writes are ordered.
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan *message
	closed    bool
	closeErr  error

	// log records what the wire is not told.
	//
	// An unexpected error is flattened to "internal error" before it leaves,
	// because it may name a file path, a query or a table and a connector on
	// the network has no business seeing any of those. It was flattened for the
	// operator too, though, so a real fault produced two words and no trace of
	// itself anywhere. Whoever runs the machine should be able to find out what
	// happened on it.
	log *slog.Logger
}

// New returns a connection. Incoming requests go to handler, which may be nil
// for a peer that only makes calls.
func New(t Transport, h Handler) *Conn {
	if h == nil {
		h = HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
			return nil, Errorf(CodeMethodNotFound, "this peer accepts no requests")
		})
	}
	return &Conn{
		transport: t,
		handler:   h,
		pending:   make(map[string]chan *message),
		log:       slog.Default(),
	}
}

// WithLogger records unexpected errors somewhere findable. Optional: without
// one they go to the default logger.
func (c *Conn) WithLogger(log *slog.Logger) *Conn {
	if log != nil {
		c.log = log
	}
	return c
}

// NewWithLogger is New for a caller that has a logger to hand.
func NewWithLogger(log *slog.Logger, t Transport, h Handler) *Conn {
	return New(t, h).WithLogger(log)
}

// Serve reads and dispatches until the transport fails or ctx ends.
func (c *Conn) Serve(ctx context.Context) error {
	defer c.shutdown(errors.New("jsonrpc: connection closed"))

	for {
		data, err := c.transport.ReadMessage(ctx)
		if err != nil {
			return err
		}

		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			// A message that will not parse has no id, so there is nobody to
			// answer. Reply with a null-id error, as the specification requires,
			// and carry on rather than dropping the connection: one bad frame
			// should not end a conversation.
			c.writeMessage(ctx, &message{
				JSONRPC: Version,
				ID:      json.RawMessage("null"),
				Error:   Errorf(CodeParseError, "message is not valid JSON"),
			})
			continue
		}

		switch {
		case m.Method != "":
			// Handled off the read loop. A slow handler must not stop responses
			// to calls this side is waiting on from being read.
			go c.dispatch(ctx, &m)
		case len(m.ID) > 0:
			c.deliver(&m)
		default:
			c.writeMessage(ctx, &message{
				JSONRPC: Version,
				ID:      json.RawMessage("null"),
				Error:   Errorf(CodeInvalidRequest, "message is neither a request nor a response"),
			})
		}
	}
}

// Call sends a request and waits for the response.
//
// result, if not nil, receives the unmarshalled result.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)

	encodedParams, err := encodeParams(params)
	if err != nil {
		return err
	}

	reply := make(chan *message, 1)
	if err := c.addPending(id, reply); err != nil {
		return err
	}
	defer c.removePending(id)

	if err := c.writeMessage(ctx, &message{
		JSONRPC: Version,
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  encodedParams,
	}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case m := <-reply:
		if m == nil {
			return c.closedError()
		}
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(m.Result, result); err != nil {
			return fmt.Errorf("jsonrpc: decoding the result of %s: %w", method, err)
		}
		return nil
	}
}

// Notify sends a notification. There is no reply and no confirmation beyond the
// write succeeding.
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	encodedParams, err := encodeParams(params)
	if err != nil {
		return err
	}
	return c.writeMessage(ctx, &message{
		JSONRPC: Version,
		Method:  method,
		Params:  encodedParams,
	})
}

func (c *Conn) dispatch(ctx context.Context, m *message) {
	result, err := c.handler.Handle(ctx, m.Method, m.Params)

	// A notification carries no id, so there is nobody to answer. Errors from
	// one are the log's business.
	if len(m.ID) == 0 {
		return
	}

	resp := &message{JSONRPC: Version, ID: m.ID}
	switch {
	case err == nil:
		encoded, encErr := json.Marshal(result)
		if encErr != nil {
			c.log.Error("cannot encode a reply", "method", m.Method, "error", encErr)
			resp.Error = Errorf(CodeInternalError, "internal error")
			break
		}
		if result == nil {
			encoded = json.RawMessage("null")
		}
		resp.Result = encoded
	default:
		var rpcErr *Error
		if errors.As(err, &rpcErr) {
			resp.Error = rpcErr
			break
		}
		// Deliberately opaque on the wire, and recorded here. An unexpected
		// error from inside core may name a file path, a query, or a table,
		// none of which a connector on the network has any business seeing.
		// Whoever runs the machine does.
		c.log.Error("a method failed unexpectedly", "method", m.Method, "error", err)
		resp.Error = Errorf(CodeInternalError, "internal error")
	}

	c.writeMessage(ctx, resp)
}

func (c *Conn) deliver(m *message) {
	c.pendingMu.Lock()
	ch, ok := c.pending[string(m.ID)]
	if ok {
		delete(c.pending, string(m.ID))
	}
	c.pendingMu.Unlock()

	if ok {
		ch <- m
	}
	// A response to something never asked for is discarded. It means the peer is
	// confused or a call already gave up, and neither is worth ending the
	// connection over.
}

func (c *Conn) writeMessage(ctx context.Context, m *message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("jsonrpc: encoding a message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transport.WriteMessage(ctx, data)
}

func (c *Conn) addPending(id string, ch chan *message) error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.closed {
		return c.closeErr
	}
	c.pending[id] = ch
	return nil
}

func (c *Conn) removePending(id string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delete(c.pending, id)
}

// shutdown wakes every waiting call so none of them hangs for the lifetime of
// the process when the connection goes away.
func (c *Conn) shutdown(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	if c.closed {
		return
	}
	c.closed = true
	c.closeErr = err

	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
}

func (c *Conn) closedError() error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.closeErr != nil {
		return c.closeErr
	}
	return errors.New("jsonrpc: connection closed")
}

func encodeParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: encoding params: %w", err)
	}
	return encoded, nil
}
