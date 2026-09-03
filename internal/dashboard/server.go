// Package dashboard is the web interface's server side.
//
// It sits between a browser and core. The browser speaks HTTP to this; this
// speaks the connector protocol to core. The split is the point: the connector
// key stays here, so a browser tab cannot act as the dashboard even if somebody
// points one straight at core.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/mhd64real/printer-cycle/internal/version"
	"github.com/mhd64real/printer-cycle/web"
)

// Caller is the part of a connector client this package needs.
//
// An interface rather than the concrete client, so the HTTP layer can be tested
// against a core that answers however a test wants, including badly.
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
	Connected() bool
}

// Server is the dashboard's HTTP side.
type Server struct {
	client Caller
	log    *slog.Logger

	connected atomic.Bool
}

// New builds the server.
func New(client Caller, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{client: client, log: log}
}

// Handler returns everything a browser talks to.
func (d *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", d.health)
	mux.HandleFunc("/api/setup", d.setup)
	mux.HandleFunc("/api/needs-setup", d.needsSetup)
	mux.HandleFunc("/api/login", d.login)
	mux.HandleFunc("/api/logout", d.logout)
	mux.HandleFunc("/api/me", d.me)
	mux.HandleFunc("/api/call", d.call)

	if !web.Built() {
		// Building the Go binary without building the interface is easy to do
		// while working on core, and meeting the result as a wall of 404s would
		// waste somebody's afternoon.
		d.log.Warn("no interface compiled in; run: make web")
		mux.HandleFunc("/", d.notBuilt)
		return mux
	}

	assets, err := web.Assets()
	if err != nil {
		d.log.Error("cannot read the compiled interface", "error", err)
		mux.HandleFunc("/", d.notBuilt)
		return mux
	}
	mux.Handle("/", spaHandler{files: http.FS(assets), fs: assets})
	return mux
}

// health reports whether the dashboard can reach core, which is the first
// question anybody asks when a page does not load.
func (d *Server) health(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"version":        version.Version,
		"core_connected": d.client.Connected(),
	}
	w.Header().Set("Content-Type", "application/json")
	if !d.client.Connected() {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}

func (d *Server) notBuilt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>printer-cycle</title></head>
<body>
<h1>printer-cycle</h1>
<p>This binary was built without its interface.</p>
<p>Run <code>make web</code> and build again.</p>
</body>
</html>
`)
}

// spaHandler serves the built interface, falling back to index.html.
//
// The interface routes in the browser, so a reload on /printers asks this server
// for a path no file matches. Answering with index.html lets the page render and
// decide for itself, which is what makes a bookmark or a refresh work.
//
// Anything under /assets is exempt: those are real files, and quietly returning
// HTML for a missing script would turn a broken build into a blank page with no
// explanation.
type spaHandler struct {
	files http.FileSystem
	fs    fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if upath == "" {
		upath = "index.html"
	}

	if _, err := fs.Stat(h.fs, upath); err == nil {
		http.FileServer(h.files).ServeHTTP(w, r)
		return
	}

	if strings.HasPrefix(upath, "assets/") {
		http.NotFound(w, r)
		return
	}

	index, err := h.fs.Open("index.html")
	if err != nil {
		http.Error(w, "interface unavailable", http.StatusInternalServerError)
		return
	}
	defer index.Close()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Not cached: the page names hashed asset files, so a stale copy points at
	// files a new build no longer has.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, index)
}
