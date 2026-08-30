package ipp

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/OpenPrinting/goipp"
)

// PrinterState is the IPP printer-state enumeration, RFC 8011 section 5.4.11.
type PrinterState int32

const (
	PrinterStateIdle       PrinterState = 3
	PrinterStateProcessing PrinterState = 4
	PrinterStateStopped    PrinterState = 5
)

func (s PrinterState) String() string {
	switch s {
	case PrinterStateIdle:
		return "idle"
	case PrinterStateProcessing:
		return "processing"
	case PrinterStateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("unknown(%d)", int32(s))
	}
}

// Marker is one ink or toner supply reported by a printer.
type Marker struct {
	Name  string
	Color string
	Type  string

	// Level is a percentage from 0 to 100. A negative value means the printer did
	// not report one, which is common and expected rather than a defect: much of
	// the old hardware this project targets has no supply reporting at all.
	// "Unknown" is a legitimate answer and the dashboard is designed to show it.
	Level int32
}

// LevelKnown reports whether the printer actually told us a level.
func (m Marker) LevelKnown() bool { return m.Level >= 0 }

// Printer is a queue configured in CUPS.
type Printer struct {
	Name          string
	URI           string
	DeviceURI     string
	MakeAndModel  string
	Info          string
	Location      string
	State         PrinterState
	StateMessage  string
	StateReasons  []string
	AcceptingJobs bool
	Markers       []Marker
}

// printerFields is what we ask CUPS for when listing queues.
var printerFields = []string{
	"printer-name",
	"printer-uri-supported",
	"device-uri",
	"printer-make-and-model",
	"printer-info",
	"printer-location",
	"printer-state",
	"printer-state-message",
	"printer-state-reasons",
	"printer-is-accepting-jobs",
	"marker-names",
	"marker-colors",
	"marker-types",
	"marker-levels",
}

// Printers lists every queue configured in CUPS.
func (c *Client) Printers(ctx context.Context) ([]Printer, error) {
	req := c.NewRequest(goipp.OpCupsGetPrinters)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))
	req.Operation.Add(requestedAttributes(printerFields...))

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return nil, err
	}

	if err := check(goipp.OpCupsGetPrinters, resp); err != nil {
		// CUPS answers a server with no queues with not-found rather than with
		// an empty list. To a caller that is zero printers, not a failure.
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	// One Printer group per queue. The named per-group fields on Message would
	// flatten them all together, so the groups have to be walked directly.
	var printers []Printer
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagPrinterGroup {
			continue
		}
		p := parsePrinter(g.Attrs)
		if p.Name == "" {
			continue
		}
		printers = append(printers, p)
	}
	return printers, nil
}

func parsePrinter(attrs goipp.Attributes) Printer {
	state, _ := integer(attrs, "printer-state")
	accepting, _ := boolean(attrs, "printer-is-accepting-jobs")

	return Printer{
		Name:          str(attrs, "printer-name"),
		URI:           str(attrs, "printer-uri-supported"),
		DeviceURI:     str(attrs, "device-uri"),
		MakeAndModel:  str(attrs, "printer-make-and-model"),
		Info:          str(attrs, "printer-info"),
		Location:      str(attrs, "printer-location"),
		State:         PrinterState(state),
		StateMessage:  str(attrs, "printer-state-message"),
		StateReasons:  strs(attrs, "printer-state-reasons"),
		AcceptingJobs: accepting,
		Markers:       parseMarkers(attrs),
	}
}

// parseMarkers zips the parallel marker-* arrays CUPS returns. They are meant to
// be the same length. A printer that disagrees gets whatever lines up, because
// discarding the lot over a length mismatch would throw away usable information
// from hardware that is already only half cooperating.
func parseMarkers(attrs goipp.Attributes) []Marker {
	names := strs(attrs, "marker-names")
	if len(names) == 0 {
		return nil
	}

	colors := strs(attrs, "marker-colors")
	types := strs(attrs, "marker-types")
	levels := integers(attrs, "marker-levels")

	markers := make([]Marker, len(names))
	for i, n := range names {
		m := Marker{Name: n, Level: -1}
		if i < len(colors) {
			m.Color = colors[i]
		}
		if i < len(types) {
			m.Type = types[i]
		}
		if i < len(levels) {
			m.Level = levels[i]
		}
		markers[i] = m
	}
	return markers
}

// Printer returns one queue by name.
//
// A queue that does not exist yields an error satisfying errors.Is(err,
// ErrNotFound), so callers branch on meaning rather than on message text.
func (c *Client) Printer(ctx context.Context, name string) (Printer, error) {
	req := c.NewRequest(goipp.OpGetPrinterAttributes)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.PrinterURI(name))))
	req.Operation.Add(requestedAttributes(printerFields...))

	resp, err := c.Do(ctx, "/printers/"+url.PathEscape(name), req, nil)
	if err != nil {
		return Printer{}, err
	}
	if err := check(goipp.OpGetPrinterAttributes, resp); err != nil {
		return Printer{}, err
	}

	for _, g := range resp.Groups {
		if g.Tag == goipp.TagPrinterGroup {
			if p := parsePrinter(g.Attrs); p.Name != "" {
				return p, nil
			}
		}
	}

	// A success carrying no printer group should not happen, but answering with
	// an empty struct and a nil error would be worse than saying so.
	return Printer{}, &Error{
		Op:      goipp.OpGetPrinterAttributes,
		Status:  goipp.StatusErrorNotFound,
		Message: fmt.Sprintf("no printer group in the response for %q", name),
	}
}
