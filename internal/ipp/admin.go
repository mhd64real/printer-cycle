package ipp

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/OpenPrinting/goipp"
)

// PrinterSpec describes a queue to create or modify.
type PrinterSpec struct {
	// Name is the CUPS queue name, and CUPS is strict about what it may contain.
	// See ValidPrinterName.
	Name string

	// DeviceURI is where the printer actually is: usb://, socket://, ipp://,
	// dnssd://. It comes from discovery, or from an address the user typed.
	DeviceURI string

	// PPDName is the driver, chosen from [Client.PPDs]. "everywhere" for a
	// driverless IPP Everywhere printer, which needs no driver at all.
	PPDName string

	// Info is the human name, free text, and it is what users actually see.
	//
	// This field exists because Name cannot hold what people want to type. A
	// queue called "Office Laser" is not a legal CUPS name, so the readable name
	// lives here and Name carries a sanitised form.
	Info string

	// Location is free text, shown alongside the printer.
	Location string

	// Shared asks CUPS to advertise this queue on the network itself.
	//
	// Defaults to false, deliberately. printer-cycle publishes printers through
	// connectors, and letting CUPS advertise them too would put one physical
	// printer on the network twice, under two identities, from the same box.
	// Users would see duplicates and have no way to tell which to pick.
	//
	// One consequence to know about. CUPS conflates advertising with remote
	// access: it refuses a print job from a remote client to an unshared queue,
	// answering "The printer or class is not shared." Production is unaffected,
	// because core reaches cupsd over its Unix socket and that counts as local.
	// But a core pointed at CUPS on another machine, which the transport does
	// support, needs its queues shared or nothing will print.
	Shared bool
}

// AddPrinter creates a queue, or modifies one that already exists.
//
// The queue is left enabled and accepting jobs, because every path that reaches
// here is a user asking for a printer they intend to print to. A queue that has
// to be enabled in a second step is a queue that silently swallows the first
// print job.
func (c *Client) AddPrinter(ctx context.Context, spec PrinterSpec) error {
	if err := ValidPrinterName(spec.Name); err != nil {
		return err
	}
	if spec.DeviceURI == "" {
		return fmt.Errorf("ipp: printer %q: device-uri is required", spec.Name)
	}

	req := c.NewRequest(goipp.OpCupsAddModifyPrinter)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.PrinterURI(spec.Name))))

	req.Printer.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String(spec.DeviceURI)))
	if spec.PPDName != "" {
		req.Printer.Add(goipp.MakeAttribute("ppd-name", goipp.TagName, goipp.String(spec.PPDName)))
	}
	if spec.Info != "" {
		req.Printer.Add(goipp.MakeAttribute("printer-info", goipp.TagText, goipp.String(spec.Info)))
	}
	if spec.Location != "" {
		req.Printer.Add(goipp.MakeAttribute("printer-location", goipp.TagText, goipp.String(spec.Location)))
	}
	req.Printer.Add(goipp.MakeAttribute("printer-is-shared", goipp.TagBoolean, goipp.Boolean(spec.Shared)))

	// 3 is idle, meaning enabled. Together with accepting-jobs this leaves the
	// queue immediately usable.
	req.Printer.Add(goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(int32(PrinterStateIdle))))
	req.Printer.Add(goipp.MakeAttribute("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(true)))

	resp, err := c.Do(ctx, "/admin/", req, nil)
	if err != nil {
		return err
	}
	return check(goipp.OpCupsAddModifyPrinter, resp)
}

// DeletePrinter removes a queue.
func (c *Client) DeletePrinter(ctx context.Context, name string) error {
	if err := ValidPrinterName(name); err != nil {
		return err
	}

	req := c.NewRequest(goipp.OpCupsDeletePrinter)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.PrinterURI(name))))

	resp, err := c.Do(ctx, "/admin/", req, nil)
	if err != nil {
		return err
	}
	return check(goipp.OpCupsDeletePrinter, resp)
}

// ValidPrinterName reports whether name is usable as a CUPS queue name.
//
// CUPS is strict and its rejection message is unhelpful, so this checks first
// and says something a user could act on. A queue name may not be empty, may not
// exceed 127 characters, and may not contain a space, a slash, a hash, or any
// control character.
//
// This restriction is the reason [PrinterSpec] carries a separate Info field.
// People want to name a printer "Office Laser"; CUPS will not have it, so the
// readable name goes in Info and Name gets a sanitised form. See [SanitiseName].
func ValidPrinterName(name string) error {
	if name == "" {
		return fmt.Errorf("ipp: printer name is empty")
	}
	if len(name) > 127 {
		return fmt.Errorf("ipp: printer name is %d characters, CUPS allows 127", len(name))
	}
	for _, r := range name {
		switch {
		case r == ' ':
			return fmt.Errorf("ipp: printer name %q contains a space, which CUPS does not allow", name)
		case r == '/' || r == '#':
			return fmt.Errorf("ipp: printer name %q contains %q, which CUPS does not allow", name, string(r))
		case unicode.IsControl(r):
			return fmt.Errorf("ipp: printer name %q contains a control character", name)
		}
	}
	return nil
}

// SanitiseName turns something a person typed into a legal CUPS queue name.
//
// "Office Laser (2nd floor)" becomes "Office_Laser_2nd_floor". The readable
// original belongs in [PrinterSpec.Info], where it survives intact; this is only
// the internal identifier.
//
// Returns an empty string if nothing usable survives, which callers must treat
// as "ask the user for a different name" rather than inventing one.
func SanitiseName(display string) string {
	var b strings.Builder
	lastUnderscore := false

	for _, r := range display {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' || r == '_'):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			// Anything else, including spaces, punctuation, and non-ASCII,
			// collapses to a single underscore. Non-ASCII is excluded because
			// CUPS queue names travel through URIs, log files, and other
			// people's tooling, where an accent is a liability.
			if !lastUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}

	name := strings.Trim(b.String(), "_")
	if len(name) > 127 {
		name = strings.Trim(name[:127], "_")
	}
	return name
}
