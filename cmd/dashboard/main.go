// Command dashboard is printer-cycle's web interface.
//
// It is a connector like any other. It holds a connector key, speaks the same
// protocol a third party would, and has no privileged path into core: whatever
// it can do, anything you write can do too. That constraint is not a slogan, it
// is enforced by there being no other way in.
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
	"syscall"
	"time"

	"github.com/mhd64real/printer-cycle/internal/connector"
	"github.com/mhd64real/printer-cycle/internal/dashboard"
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

	// Declared before the client so the client can hand it notifications, and
	// given its client immediately afterwards. Neither can be built without the
	// other, and this is the smaller knot.
	var web *dashboard.Server

	client, err := connector.New(connector.Options{
		ID:         "dashboard",
		CoreURL:    *coreURL,
		KeyPath:    filepath.Join(*dataDir, "connector.key"),
		SetupToken: tokenFrom(*setupToken),
		Manifest:   manifest(),
		Logger:     log,
		OnNotify: func(method string, params json.RawMessage) {
			web.HandleNotification(method, params)
		},
	})
	if err != nil {
		return err
	}
	web = dashboard.New(client, log)

	go func() {
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("gave up talking to core", "error", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           web.Handler(),
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
