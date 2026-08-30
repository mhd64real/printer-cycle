// Command core is the printer-cycle daemon.
//
// It owns users, printers, jobs, and the connector protocol, and it is the only
// part of the system that talks to CUPS. Connectors reach it over the WebSocket
// protocol described in PROTOCOL.md; nothing, including the dashboard, gets a
// path around that.
//
// The protocol server itself is not built yet. See PLAN.md, Phase 4.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mhd64real/printer-cycle/internal/store"
	"github.com/mhd64real/printer-cycle/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "printer-cycle-core:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		dataDir     = flag.String("data-dir", defaultDataDir(), "where the database and setup token live")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}

	ctx := context.Background()

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
	} else {
		fmt.Printf("printer-cycle-core %s, database at %s\n", version.Version, *dataDir)
	}

	// The connector server lands in Phase 4. Saying so is better than sitting
	// idle and looking like a daemon that works.
	fmt.Fprintln(os.Stderr, "the connector server is not implemented yet, see PLAN.md phase 4")
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
