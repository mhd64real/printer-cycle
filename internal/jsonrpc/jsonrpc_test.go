package jsonrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
)

// pipe is an in-memory transport pair, so the message layer can be exercised
// without a network or a WebSocket anywhere near it.
type pipe struct {
	in  chan []byte
	out chan []byte
}

func newPipes() (*pipe, *pipe) {
	a, b := make(chan []byte, 16), make(chan []byte, 16)
	return &pipe{in: a, out: b}, &pipe{in: b, out: a}
}

func (p *pipe) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data, ok := <-p.in:
		if !ok {
			return nil, errors.New("pipe closed")
		}
		return data, nil
	}
}

func (p *pipe) WriteMessage(ctx context.Context, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.out <- data:
		return nil
	}
}

// connected wires two peers together and serves both.
func connected(t *testing.T, aHandler, bHandler jsonrpc.Handler) (*jsonrpc.Conn, *jsonrpc.Conn, context.Context) {
	t.Helper()

	ap, bp := newPipes()
	a := jsonrpc.New(ap, aHandler)
	b := jsonrpc.New(bp, bHandler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	go a.Serve(ctx)
	go b.Serve(ctx)
	return a, b, ctx
}

func echoHandler(t *testing.T) jsonrpc.Handler {
	return jsonrpc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "echo":
			var in map[string]any
			if len(params) > 0 {
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
				}
			}
			return in, nil
		case "boom":
			return nil, jsonrpc.Errorf(jsonrpc.CodeScopeDenied, "scope denied")
		case "panic-ish":
			return nil, errors.New("a table name and a file path nobody outside should see")
		default:
			return nil, jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "no such method %q", method)
		}
	})
}

// The point of the stage: both peers can call, not just one.
func TestEitherPeerCanCallTheOther(t *testing.T) {
	a, b, ctx := connected(t, echoHandler(t), echoHandler(t))

	var out map[string]any
	if err := a.Call(ctx, "echo", map[string]any{"from": "a"}, &out); err != nil {
		t.Fatalf("a calling b: %v", err)
	}
	if out["from"] != "a" {
		t.Errorf("result = %v", out)
	}

	out = nil
	if err := b.Call(ctx, "echo", map[string]any{"from": "b"}, &out); err != nil {
		t.Fatalf("b calling a: %v", err)
	}
	if out["from"] != "b" {
		t.Errorf("result = %v", out)
	}
}

// Notifications get no reply, and a caller must not be left waiting for one.
func TestNotificationsGetNoReply(t *testing.T) {
	got := make(chan string, 1)
	handler := jsonrpc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		got <- method
		return "ignored", nil
	})

	a, _, ctx := connected(t, nil, handler)

	if err := a.Notify(ctx, "job.updated", map[string]any{"state": "printing"}); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-got:
		if m != "job.updated" {
			t.Errorf("method = %q", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the notification never arrived")
	}
}

func TestErrorsCarryTheirCode(t *testing.T) {
	a, _, ctx := connected(t, nil, echoHandler(t))

	err := a.Call(ctx, "boom", nil, nil)
	if err == nil {
		t.Fatal("no error")
	}

	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err is %T, want *jsonrpc.Error", err)
	}
	if rpcErr.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("code = %d, want %d", rpcErr.Code, jsonrpc.CodeScopeDenied)
	}
}

// An unexpected error from inside core must not leak its text. It may name a
// file path, a query, or a table, none of which a connector should see.
func TestUnexpectedErrorsAreOpaque(t *testing.T) {
	a, _, ctx := connected(t, nil, echoHandler(t))

	err := a.Call(ctx, "panic-ish", nil, nil)
	if err == nil {
		t.Fatal("no error")
	}

	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err is %T", err)
	}
	if rpcErr.Code != jsonrpc.CodeInternalError {
		t.Errorf("code = %d, want internal error", rpcErr.Code)
	}
	if strings.Contains(rpcErr.Message, "table name") || strings.Contains(rpcErr.Message, "file path") {
		t.Errorf("the internal error text escaped to the peer: %q", rpcErr.Message)
	}
}

func TestUnknownMethod(t *testing.T) {
	a, _, ctx := connected(t, nil, echoHandler(t))

	var rpcErr *jsonrpc.Error
	if err := a.Call(ctx, "no.such.method", nil, nil); !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v", err)
	} else if rpcErr.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("code = %d, want %d", rpcErr.Code, jsonrpc.CodeMethodNotFound)
	}
}

// Responses are matched to their own request. Overlapping calls with slow,
// out-of-order handlers is exactly where a correlation bug shows itself.
func TestOverlappingCallsCorrelateCorrectly(t *testing.T) {
	handler := jsonrpc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		var in struct {
			N     int `json:"n"`
			Delay int `json:"delay_ms"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(in.Delay) * time.Millisecond)
		return map[string]int{"n": in.N}, nil
	})

	a, _, ctx := connected(t, nil, handler)

	const calls = 20
	var wg sync.WaitGroup
	errs := make(chan error, calls)

	for i := range calls {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Later calls finish sooner, so replies come back out of order.
			var out struct {
				N int `json:"n"`
			}
			params := map[string]int{"n": n, "delay_ms": (calls - n) * 5}
			if err := a.Call(ctx, "slow", params, &out); err != nil {
				errs <- err
				return
			}
			if out.N != n {
				errs <- errors.New("a reply was delivered to the wrong caller")
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// A frame that will not parse must not end the conversation. One malformed
// message from a buggy connector should not take down a working connection.
func TestMalformedMessageDoesNotKillTheConnection(t *testing.T) {
	ap, bp := newPipes()
	a := jsonrpc.New(ap, echoHandler(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go a.Serve(ctx)

	if err := bp.WriteMessage(ctx, []byte("{not json at all")); err != nil {
		t.Fatal(err)
	}

	reply, err := bp.ReadMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID    json.RawMessage `json:"id"`
		Error *jsonrpc.Error  `json:"error"`
	}
	if err := json.Unmarshal(reply, &m); err != nil {
		t.Fatal(err)
	}
	if m.Error == nil || m.Error.Code != jsonrpc.CodeParseError {
		t.Fatalf("reply = %s, want a parse error", reply)
	}
	if string(m.ID) != "null" {
		t.Errorf("id = %s, want null: there is no request to attribute it to", m.ID)
	}

	// The connection still works.
	if err := bp.WriteMessage(ctx, []byte(`{"jsonrpc":"2.0","id":7,"method":"echo","params":{"ok":true}}`)); err != nil {
		t.Fatal(err)
	}
	second, err := bp.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("the connection died after one bad frame: %v", err)
	}
	if !strings.Contains(string(second), `"ok":true`) {
		t.Errorf("reply = %s", second)
	}
}

// A call waiting when the connection dies must be woken, not left hanging for
// the lifetime of the process.
func TestPendingCallsAreWokenWhenTheConnectionDies(t *testing.T) {
	ap, _ := newPipes()
	a := jsonrpc.New(ap, nil)

	serveCtx, stopServe := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() { defer close(served); a.Serve(serveCtx) }()

	done := make(chan error, 1)
	go func() {
		done <- a.Call(context.Background(), "never.answered", nil, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	stopServe()
	<-served

	select {
	case err := <-done:
		if err == nil {
			t.Error("the call returned success on a dead connection")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call is still waiting after the connection died")
	}
}
