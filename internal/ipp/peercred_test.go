package ipp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenPrinting/goipp"
	"github.com/mhd64real/printer-cycle/internal/ipp"
)

// A cupsd that behaves the way the real one does for an administrative
// operation: refuse the first attempt, name the schemes it accepts, and accept
// the retry that says who is calling.
func peerCredCUPS(t *testing.T, challenge string) (*ipp.Client, *[]string) {
	t.Helper()

	// A short path: a Unix socket address is limited to about a hundred bytes
	// and t.TempDir() on macOS is most of that already.
	dir, err := os.MkdirTemp("", "pc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "c.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	var seen []string

	srv := &httptest.Server{
		Listener: listener,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			seen = append(seen, auth)

			if auth == "" {
				w.Header().Set("WWW-Authenticate", challenge)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			reply := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, 1)
			reply.Operation.Add(goipp.MakeAttribute("attributes-charset",
				goipp.TagCharset, goipp.String("utf-8")))
			w.Header().Set("Content-Type", "application/ipp")
			_ = reply.Encode(w)
		})},
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client, err := ipp.New("unix://" + sock)
	if err != nil {
		t.Fatal(err)
	}
	return client, &seen
}

// The stage's own finding. CUPS puts administrative operations behind
// "Require user @SYSTEM" and answers the first attempt with 401. Over a Unix
// socket it offers PeerCred, where the client names itself and cupsd checks
// that name against the credentials of the process on the other end, which
// cannot be lied about. Not doing this is why core could talk to CUPS perfectly
// well until it tried to add a printer on a real machine.
func TestAnAdminOperationAuthenticatesWithPeerCred(t *testing.T) {
	client, seen := peerCredCUPS(t, `Basic realm="CUPS", PeerCred, Local`)

	req := client.NewRequest(goipp.OpCupsGetPrinters)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(client.RootURI())))

	if _, err := client.Do(context.Background(), "/", req, nil); err != nil {
		t.Fatalf("the retry did not succeed: %v", err)
	}

	if len(*seen) != 2 {
		t.Fatalf("made %d requests, want the first refused and one retry", len(*seen))
	}
	if (*seen)[0] != "" {
		t.Errorf("the first attempt already carried %q", (*seen)[0])
	}
	if !strings.HasPrefix((*seen)[1], "PeerCred ") {
		t.Fatalf("the retry sent %q, want a PeerCred scheme", (*seen)[1])
	}

	// The name has to be this process's own account, because that is the one
	// cupsd will compare against the socket's credentials.
	name := strings.TrimPrefix((*seen)[1], "PeerCred ")
	if u, err := user.Current(); err == nil && name != u.Username {
		t.Errorf("named %q, want %q", name, u.Username)
	}
}

// A challenge without PeerCred in it is a cupsd that wants a password, and
// there is no password. Saying so beats retrying something that cannot work.
func TestAChallengeWithoutPeerCredSaysWhatItWanted(t *testing.T) {
	client, seen := peerCredCUPS(t, `Basic realm="CUPS"`)

	req := client.NewRequest(goipp.OpCupsGetPrinters)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(client.RootURI())))

	if _, err := client.Do(context.Background(), "/", req, nil); err == nil {
		t.Fatal("a password-only challenge was reported as success")
	} else if !strings.Contains(err.Error(), "Basic") {
		t.Errorf("the error does not say what CUPS asked for: %v", err)
	}

	for _, auth := range *seen {
		if strings.HasPrefix(auth, "PeerCred") {
			t.Error("it tried PeerCred against a server that did not offer it")
		}
	}
}
