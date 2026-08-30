// Command core is the printer-cycle daemon.
//
// It owns users, printers, jobs, and the connector protocol, and it is the only
// part of the system that talks to CUPS. Connectors reach it over the WebSocket
// protocol described in PROTOCOL.md; nothing, including the dashboard, gets a
// path around that.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mhd64real/printer-cycle/internal/protocol"
	"github.com/mhd64real/printer-cycle/internal/store"
	"github.com/mhd64real/printer-cycle/internal/version"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "printer-cycle-core:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		dataDir     = flag.String("data-dir", defaultDataDir(), "where the database and setup token live")
		listenAddr  = flag.String("listen", protocol.DefaultTCPAddr, "address to serve the connector protocol on")
		socketPath  = flag.String("socket", "", "additional unix socket for connectors on this machine")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}

	// Cancelled on the first interrupt, so a second one can still kill a stuck
	// process rather than being swallowed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.Default()

	db, err := store.Open(filepath.Join(*dataDir, "printer-cycle.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	token, issued, err := db.Bootstrap(ctx)
	if err != nil {
		return fmt.Errorf("first-run setup: %w", err)
	}
	if issued {
		tokenPath := filepath.Join(*dataDir, "setup-token")
		if err := writeSetupToken(tokenPath, token); err != nil {
			return err
		}
		printSetupBanner(token, tokenPath)
	}

	addrs := []string{*listenAddr}
	if *socketPath != "" {
		addrs = append(addrs, *socketPath)
	}

	log.Info("printer-cycle core starting", "version", version.Version, "data", *dataDir)

	server := protocol.NewServer(db, protocol.Options{Logger: log})
	if err := server.Serve(ctx, addrs...); err != nil {
		return err
	}

	log.Info("stopped")
	return nil
}

// writeSetupToken puts the token where only somebody with access to the machine
// can read it. Anyone who can read this file can complete setup and become the
// administrator, so the permissions are the whole point.
func writeSetupToken(path, token string) error {
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing the setup token: %w", err)
	}
	// WriteFile leaves an existing file's permissions alone, so set them again.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("securing the setup token: %w", err)
	}
	return nil
}

func printSetupBanner(token, path string) {
	fmt.Printf(`
printer-cycle is not set up yet.

  Setup token:  %s

Open the dashboard and enter that token to create the first account.
It is also saved at %s, readable only by this user, and it is valid for 24 hours.

A new token is issued every time core starts until setup is finished, so if this
one scrolls away, restart and read the new one. Earlier tokens stop working.

`, token, path)
}

// defaultDataDir is where a packaged install keeps its state. Overridden by the
// flag during development, where writing to /var is neither possible nor wanted.
func defaultDataDir() string {
	if dir := os.Getenv("PRINTER_CYCLE_DATA_DIR"); dir != "" {
		return dir
	}
	return "/var/lib/printer-cycle"
}
