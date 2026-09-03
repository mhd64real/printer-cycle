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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mhd64real/printer-cycle/internal/connector"
	"github.com/mhd64real/printer-cycle/internal/version"
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
	mux.HandleFunc("/", d.placeholder)
	return mux
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

func (d *dashboard) placeholder(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	state := "connected to core"
	if !d.client.Connected() {
		state = "not connected to core"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>printer-cycle</title></head>
<body>
<h1>printer-cycle</h1>
<p>%s.</p>
<p>The interface is not built yet. See PLAN.md, phase 5.</p>
</body>
</html>
`, state)
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
