package protocol

import (
	"github.com/OpenPrinting/goipp"

	"github.com/mhd64real/printer-cycle/internal/store"
)

// ippStatus is a readability helper for tests that name raw IPP status codes.
func ippStatus(code int) goipp.Status { return goipp.Status(code) }

// validScopeName reports whether a scope is one core knows about.
func validScopeName(scope string) bool {
	for _, known := range store.KnownScopes() {
		if known == scope {
			return true
		}
	}
	return false
}
