// Command core is the printer-cycle daemon.
//
// It owns users, printers, jobs, and the connector protocol, and it is the only
// part of the system that talks to CUPS. Connectors reach it over the WebSocket
// protocol described in PROTOCOL.md; nothing, including the dashboard, gets a
// path around that.
//
// None of that is implemented yet. See PLAN.md.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mhd64real/printer-cycle/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	fmt.Fprintf(os.Stderr, "printer-cycle-core %s\n", version.Version)
	fmt.Fprintln(os.Stderr, "not implemented yet, see PLAN.md")
	os.Exit(1)
}
