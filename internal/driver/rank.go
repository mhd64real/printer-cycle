// Package driver chooses which driver a printer should use.
//
// CUPS narrows the catalogue for us, and it narrows it loosely. Asking for
// "MFG:HP;MDL:LaserJet 4;" returns 33 drivers, among them ones for a Color
// LaserJet 4610 and a Color LaserJet 4730 MFP, which are different printers
// that happen to contain the same characters. Something has to decide which of
// the 33 is the one, and it has to decide the same way every time.
package driver

import (
	"strings"

	"github.com/mhd64real/printer-cycle/internal/deviceid"
)

// Candidate is a driver CUPS offered, with what was made of it.
type Candidate struct {
	PPD          string
	MakeAndModel string

	// DeviceID is the driver's own claim about what hardware it drives. It is
	// the field that makes ranking possible at all: comparing it to the
	// printer's own device id is how an exact model match is told from CUPS
	// having matched on a substring.
	DeviceID string

	Recommended               bool
	RequiresProprietaryPlugin bool

	// Score is the sum of the signals that fired, and Why names them. Why
	// exists because "printer-cycle picked this one" is not an answer somebody
	// can check, and the whole promise here is being better than a vendor
	// installer rather than differently opaque.
	Score int
	Why   []string
}

// signal is one reason to prefer a driver.
//
// The ranking is this table. Adding a reason means adding a row, and the
// relative weights are visible together rather than spread through a function
// where nobody can see what beats what.
type signal struct {
	// Why is written for a person reading the dashboard, so it completes the
	// sentence "chosen because ...".
	why    string
	weight int
	test   func(printer, driver deviceid.ID, c Candidate) bool
}

// The weights matter only relative to each other, and only two comparisons are
// really being asserted:
//
//   - An exact model match beats everything. A driver for a different printer
//     is wrong however well recommended it is, and CUPS's loose matching means
//     drivers for different printers are always in the list.
//   - Given the same model, an open driver beats one needing a closed vendor
//     binary. Those binaries are x86 only, so on the Raspberry Pi this is
//     built for they are not slower, they do not run.
//
// Everything else is a tie-breaker.
var signals = []signal{
	{
		why:    "it is written for this exact model",
		weight: 100,
		test: func(printer, drv deviceid.ID, _ Candidate) bool {
			return printer.Model != "" && strings.EqualFold(printer.Model, drv.Model)
		},
	},
	{
		why:    "it needs no proprietary plugin",
		weight: 40,
		test: func(_, _ deviceid.ID, c Candidate) bool {
			return !c.RequiresProprietaryPlugin
		},
	},
	{
		why:    "it is by the same manufacturer",
		weight: 20,
		test: func(printer, drv deviceid.ID, _ Candidate) bool {
			return sameManufacturer(printer.Manufacturer, drv.Manufacturer)
		},
	},
	{
		why:    "CUPS recommends it",
		weight: 10,
		test: func(_, _ deviceid.ID, c Candidate) bool {
			return c.Recommended
		},
	},
	{
		// Unproven against real hardware, and labelled as such rather than
		// dressed up.
		//
		// It was very nearly written down as measured. On the one real case in
		// hand, a LaserJet 1018 reporting CMD:ZJS, the two candidate PPDs
		// declare CMD:ACL and no command set at all, so this signal fires for
		// neither and the choice is made entirely by the ones above it. CUPS
		// matched both PPDs on manufacturer and model regardless of the command
		// set, which is worth knowing: a driver's declared languages and a
		// printer's need not agree for CUPS to offer it.
		//
		// Kept because it is correct by construction and weighted low enough to
		// break ties and nothing else. Stage 69 puts a real USB printer on a
		// real board, which is where it can be confirmed or dropped.
		why:    "it speaks a language this printer speaks",
		weight: 5,
		test: func(printer, drv deviceid.ID, _ Candidate) bool {
			for _, cmd := range drv.Commands {
				if printer.Speaks(cmd) {
					return true
				}
			}
			return false
		},
	},
}

// Rank orders candidates for a printer, best first.
//
// Stable: candidates scoring the same keep the order CUPS gave them, so the
// same printer and the same catalogue produce the same answer every time. A
// pairing screen that suggested a different driver on each visit would be
// worse than one that suggested nothing.
func Rank(printerDeviceID string, candidates []Candidate) []Candidate {
	printer := deviceid.Parse(printerDeviceID)

	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)

	for i := range ranked {
		drv := deviceid.Parse(ranked[i].DeviceID)
		ranked[i].Score = 0
		ranked[i].Why = nil
		for _, s := range signals {
			if s.test(printer, drv, ranked[i]) {
				ranked[i].Score += s.weight
				ranked[i].Why = append(ranked[i].Why, s.why)
			}
		}
	}

	// A hand-rolled insertion sort rather than sort.SliceStable, because this
	// list is single digits long in practice and the stability is the point.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].Score > ranked[j-1].Score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	return ranked
}

// Best returns the driver to use, and whether it is a safe automatic choice.
//
// Not safe means: offer it, do not apply it. A driver needing a closed vendor
// binary may be the only one there is, and it is better to name it than to call
// the printer unsupported, but it must not be chosen silently on a machine
// where that binary cannot run.
func Best(printerDeviceID string, candidates []Candidate) (Candidate, bool) {
	ranked := Rank(printerDeviceID, candidates)
	if len(ranked) == 0 {
		return Candidate{}, false
	}

	first := ranked[0]
	printer := deviceid.Parse(printerDeviceID)

	// A driver for a different model is not an automatic choice even when it is
	// the only one. CUPS returning it means the strings overlapped, not that it
	// will drive the hardware.
	if printer.Model != "" && !strings.EqualFold(printer.Model, deviceid.Parse(first.DeviceID).Model) {
		return first, false
	}
	return first, !first.RequiresProprietaryPlugin
}

// sameManufacturer compares makers, tolerating what they call themselves.
//
// "HP" and "Hewlett-Packard" are the same company and both appear in the
// catalogue, sometimes for the same printer: the LaserJet 1018 reports
// Hewlett-Packard while a great many HP PPDs say HP.
func sameManufacturer(a, b string) bool {
	a, b = normaliseMaker(a), normaliseMaker(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// makerAliases folds the names one company uses for itself onto one.
//
// Only pairs seen in the catalogue. A longer list would be guessing at
// companies that may not print anything.
var makerAliases = map[string]string{
	"hewlettpackard":        "hp",
	"eastmankodakcompany":   "kodak",
	"lexmarkinternational":  "lexmark",
	"seikoepson":            "epson",
	"brotherindustries":     "brother",
	"canoninc":              "canon",
	"ricohcompanyltd":       "ricoh",
	"oki":                   "okidata",
	"samsungelectronics":    "samsung",
	"xeroxcorporation":      "xerox",
	"fujixerox":             "fujixerox",
	"konicaminoltabusiness": "konicaminolta",
}

func normaliseMaker(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	folded := b.String()
	if alias, ok := makerAliases[folded]; ok {
		return alias
	}
	return folded
}
