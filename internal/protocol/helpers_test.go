package protocol_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/protocol"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// shortSocketPath returns a temporary socket path brief enough for the kernel.
//
// t.TempDir() embeds the test's name, which on macOS pushes the result past the
// 104 byte limit on Unix socket paths, and the failure that follows is a bare
// "invalid argument" from connect.
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "pc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	path := filepath.Join(dir, "c.sock")
	if len(path) >= 104 {
		t.Skipf("even a minimal temporary path is %d characters here", len(path))
	}
	return path
}

// writeFile leaves a plain file where a socket is expected, standing in for one
// left behind by a crash.
func writeFile(path string) error {
	return os.WriteFile(path, []byte("stale"), 0o600)
}

func ctx() context.Context { return context.Background() }

// cupsBackedServer is a protocol server wired to the development CUPS.
func cupsBackedServer(t *testing.T) (string, *store.DB) {
	t.Helper()

	cups, err := ipp.New(skipWithoutCUPS(t))
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "printer-cycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	s := protocol.NewServer(db, protocol.Options{Logger: quietLogger(), CUPS: cups})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + protocol.ConnectorPath, db
}
