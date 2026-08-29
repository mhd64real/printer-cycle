// Command dashboard is the web dashboard, and it is a connector like any other.
//
// It holds a connector credential, speaks the same protocol third-party
// connectors speak, and has no privileged path into core. Anything it can do,
// anything you write can do. That constraint is deliberate: it is what keeps
// the protocol honest.
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

	fmt.Fprintf(os.Stderr, "printer-cycle-dashboard %s\n", version.Version)
	fmt.Fprintln(os.Stderr, "not implemented yet, see PLAN.md")
	os.Exit(1)
}
