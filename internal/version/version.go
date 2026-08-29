// Package version carries the build version, stamped in at link time.
package version

// Version is overridden at build time by the Makefile with:
//
//	-X github.com/mhd64real/printer-cycle/internal/version.Version=<value>
//
// A plain `go build` leaves it as "dev".
var Version = "dev"
