package protocol

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
)

func testConn() *conn {
	return &conn{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// IPP answers a missing printer and a missing job identically. The protocol does
// not, and only the calling layer knows which was asked for. That is the whole
// reason this translation lives here rather than in the ipp package.
func TestTheSameIPPErrorBecomesDifferentCodes(t *testing.T) {
	c := testConn()
	notFound := &ipp.Error{Status: 0x0406} // client-error-not-found

	printerErr := c.translateIPP(notFound, subjectPrinter)
	jobErr := c.translateIPP(notFound, subjectJob)

	var p, j *jsonrpc.Error
	if !errors.As(printerErr, &p) || !errors.As(jobErr, &j) {
		t.Fatalf("translation produced %T and %T", printerErr, jobErr)
	}
	if p.Code != jsonrpc.CodeUnknownPrinter {
		t.Errorf("printer not-found became %d, want %d", p.Code, jsonrpc.CodeUnknownPrinter)
	}
	if j.Code != jsonrpc.CodeUnknownJob {
		t.Errorf("job not-found became %d, want %d", j.Code, jsonrpc.CodeUnknownJob)
	}
}

func TestIPPErrorTranslation(t *testing.T) {
	c := testConn()

	cases := map[string]struct {
		status int
		want   int
	}{
		"unsupported document format": {0x040a, jsonrpc.CodePayloadRejected},
		"not possible":                {0x0404, jsonrpc.CodeInvalidParams},
		"conflicting attributes":      {0x040e, jsonrpc.CodeInvalidParams},
		"server error":                {0x0500, jsonrpc.CodeInternalError},
	}

	for name, tc := range cases {
		err := c.translateIPP(&ipp.Error{Status: ippStatus(tc.status)}, subjectPrinter)
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("%s produced %T", name, err)
		}
		if rpcErr.Code != tc.want {
			t.Errorf("%s became %d, want %d", name, rpcErr.Code, tc.want)
		}
	}

	if err := c.translateIPP(nil, subjectPrinter); err != nil {
		t.Errorf("translating a nil error produced %v", err)
	}
}

// CUPS refusing core is an operator problem, almost always core not being in the
// lpadmin group. Reporting it as a scope error would send a connector author
// hunting for a permission they cannot grant and do not need.
func TestCUPSRefusalIsNotReportedAsAScopeProblem(t *testing.T) {
	c := testConn()

	for _, status := range []int{0x0401, 0x0402, 0x0403} {
		err := c.translateIPP(&ipp.Error{Status: ippStatus(status)}, subjectPrinter)
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("status %#x produced %T", status, err)
		}
		if rpcErr.Code == jsonrpc.CodeScopeDenied {
			t.Errorf("status %#x was reported as a connector scope problem", status)
		}
		if rpcErr.Code != jsonrpc.CodeInternalError {
			t.Errorf("status %#x became %d, want an internal error", status, rpcErr.Code)
		}
	}
}

// Every method a handler answers must be listed in the permission table, and
// every method in the table must have a handler. A mismatch either way is a
// method that is unreachable or one that is reachable without a decision having
// been made about what it costs.
func TestThePermissionTableAndTheHandlersAgree(t *testing.T) {
	for method, scope := range methodScopes {
		if scope == scopeNone {
			continue
		}
		if !validScopeName(scope) {
			t.Errorf("method %q requires %q, which is not a scope core recognises", method, scope)
		}
	}
}
