package protocol_test

import (
	"os"
	"path/filepath"
	"testing"
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
