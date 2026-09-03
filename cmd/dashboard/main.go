// Command dashboard is printer-cycle's web interface.
//
// It is a connector like any other. It holds a connector key, speaks the same
// protocol a third party would, and has no privileged path into core: whatever
// it can do, anything you write can do too. That constraint is not a slogan, it
// is enforced by there being no other way in.
//
// The browser talks to this process over HTTP. This process talks to core over
// the connector protocol. The connector key never leaves here, so a browser tab
// cannot act as the dashboard even if somebody points one at core directly.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mhd64real/printer-cycle/internal/connector"
	"github.com/mhd64real/printer-cycle/internal/version"
	"github.com/mhd64real/printer-cycle/web"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "printer-cycle-dashboard:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		coreURL     = flag.String("core", defaultCore(), "where core listens")
		listenAddr  = flag.String("listen", defaultListen(), "address to serve the dashboard on")
		dataDir     = flag.String("data-dir", defaultDataDir(), "where this connector's key lives")
		setupToken  = flag.String("setup-token", "", "one-time token from core's first run")
		logLevel    = flag.String("log-level", "info", "debug, info, warn, or error")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}

	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &dashboard{log: log}

	client, err := connector.New(connector.Options{
		ID:         "dashboard",
		CoreURL:    *coreURL,
		KeyPath:    filepath.Join(*dataDir, "connector.key"),
		SetupToken: tokenFrom(*setupToken),
		Manifest:   manifest(),
		Logger:     log,
		OnConnect: func(map[string]any) {
			srv.connected.Store(true)
		},
		OnNotify: srv.onNotify,
	})
	if err != nil {
		return err
	}
	srv.client = client

	go func() {
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("gave up talking to core", "error", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           srv.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	log.Info("dashboard starting", "version", version.Version, "listen", *listenAddr, "core", *coreURL)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

// dashboard is the HTTP side: what a browser talks to.
type dashboard struct {
	client    *connector.Client
	log       *slog.Logger
	connected atomic.Bool
}

func (d *dashboard) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", d.health)

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

// health reports whether the dashboard can reach core, which is the first
// question anybody asks when a page does not load.
func (d *dashboard) health(w http.ResponseWriter, r *http.Request) {
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

func (d *dashboard) notBuilt(w http.ResponseWriter, r *http.Request) {
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

func (d *dashboard) onNotify(method string, params json.RawMessage) {
	// Nothing renders yet, so this only records that the push side is alive.
	d.log.Debug("core said something", "method", method)
}

// manifest is what the dashboard tells core about itself on every connection.
//
// No settings: the dashboard has nothing an administrator configures through the
// generic settings page, because it is the thing rendering that page.
func manifest() any {
	return map[string]any{
		"name":        "Dashboard",
		"version":     version.Version,
		"description": "printer-cycle's web interface.",
		"identity":    "linked",
		"settings":    []any{},
	}
}

// tokenFrom accepts a token directly or reads one from a file, since core writes
// its setup token to disk and asking somebody to copy it by hand is worse than
// pointing at it.
func tokenFrom(value string) string {
	if value == "" {
		return ""
	}
	if data, err := os.ReadFile(value); err == nil {
		return strings.TrimSpace(string(data))
	}
	return value
}

func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q is not one of debug, info, warn, error", level)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}

func defaultCore() string {
	if v := os.Getenv("PRINTER_CYCLE_CORE"); v != "" {
		return v
	}
	return "ws://127.0.0.1:6310"
}

func defaultListen() string {
	if v := os.Getenv("PRINTER_CYCLE_DASHBOARD_LISTEN"); v != "" {
		return v
	}
	// Next to core's 6310, both adjacent to 631, the IPP port.
	return "0.0.0.0:6311"
}

func defaultDataDir() string {
	if v := os.Getenv("PRINTER_CYCLE_DASHBOARD_DATA_DIR"); v != "" {
		return v
	}
	return "/var/lib/printer-cycle/dashboard"
}
