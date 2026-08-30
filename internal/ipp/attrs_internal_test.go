package ipp

import (
	"errors"
	"strings"
	"testing"

	"github.com/OpenPrinting/goipp"
)

// The marker arrays CUPS returns are parallel and are supposed to be the same
// length. Real printers do not always agree. Losing every supply reading because
// one array is short would throw away usable information from hardware that is
// already only half cooperating.
func TestParseMarkersHandlesRaggedArrays(t *testing.T) {
	var attrs goipp.Attributes

	names := goipp.Attribute{Name: "marker-names"}
	names.Values.Add(goipp.TagName, goipp.String("Black Toner"))
	names.Values.Add(goipp.TagName, goipp.String("Cyan Toner"))
	attrs.Add(names)

	colors := goipp.Attribute{Name: "marker-colors"}
	colors.Values.Add(goipp.TagName, goipp.String("#000000"))
	attrs.Add(colors)

	// One level for two supplies, on purpose.
	levels := goipp.Attribute{Name: "marker-levels"}
	levels.Values.Add(goipp.TagInteger, goipp.Integer(42))
	attrs.Add(levels)

	got := parseMarkers(attrs)
	if len(got) != 2 {
		t.Fatalf("got %d markers, want 2", len(got))
	}

	if got[0].Level != 42 || !got[0].LevelKnown() {
		t.Errorf("first marker level = %d, want 42 and known", got[0].Level)
	}
	if got[0].Color != "#000000" {
		t.Errorf("first marker colour = %q, want #000000", got[0].Color)
	}

	// The second supply had no level reported. That has to read as unknown
	// rather than as zero, which would wrongly mean "empty".
	if got[1].LevelKnown() {
		t.Errorf("second marker level = %d, want unknown", got[1].Level)
	}
	if got[1].Name != "Cyan Toner" {
		t.Errorf("second marker name = %q, want Cyan Toner", got[1].Name)
	}
}

// Old printers omit a great deal. Every helper has to answer for an attribute
// that simply is not there, without an error and without a panic.
func TestHelpersTolerateMissingAttributes(t *testing.T) {
	var empty goipp.Attributes

	if got := str(empty, "printer-name"); got != "" {
		t.Errorf("str = %q, want empty", got)
	}
	if got := strs(empty, "printer-state-reasons"); got != nil {
		t.Errorf("strs = %v, want nil", got)
	}
	if _, ok := integer(empty, "printer-state"); ok {
		t.Error("integer reported a value for a missing attribute")
	}
	if _, ok := boolean(empty, "printer-is-accepting-jobs"); ok {
		t.Error("boolean reported a value for a missing attribute")
	}
	if got := parseMarkers(empty); got != nil {
		t.Errorf("parseMarkers = %v, want nil for a printer that reports no supplies", got)
	}
}

func TestRequestedAttributesCarriesEveryName(t *testing.T) {
	attr := requestedAttributes("printer-name", "printer-state")
	if attr.Name != "requested-attributes" {
		t.Errorf("name = %q", attr.Name)
	}
	if len(attr.Values) != 2 {
		t.Fatalf("got %d values, want 2", len(attr.Values))
	}
	if attr.Values[0].T != goipp.TagKeyword {
		t.Errorf("tag = %v, want keyword", attr.Values[0].T)
	}
}

// RFC 8011 makes success a range, not a single value. Several codes in it are
// successes with a caveat: attributes ignored, values substituted, subscriptions
// dropped. Treating those as failures would reject perfectly good print jobs for
// cosmetic reasons, which is exactly what makes a print server feel broken.
func TestCheckTreatsTheWholeSuccessRangeAsSuccess(t *testing.T) {
	successes := []goipp.Status{
		goipp.StatusOk,
		goipp.StatusOkIgnoredOrSubstituted,
		goipp.StatusOkConflicting,
		goipp.StatusOkIgnoredSubscriptions,
		goipp.StatusOkEventsComplete,
	}
	for _, status := range successes {
		resp := goipp.NewResponse(goipp.DefaultVersion, status, 1)
		if err := check(goipp.OpCupsGetPrinters, resp); err != nil {
			t.Errorf("check(%s) = %v, want nil", status, err)
		}
	}

	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusErrorNotFound, 1)
	if err := check(goipp.OpCupsGetPrinters, resp); err == nil {
		t.Error("check(not-found) returned nil, want an error")
	}
}

// CUPS usually sends a status-message that is more specific than the code.
// Losing it would mean throwing away the most useful half of the diagnosis.
func TestCheckCarriesTheServerMessage(t *testing.T) {
	const msg = "The printer or class does not exist."

	resp := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusErrorNotFound, 1)
	resp.Operation.Add(goipp.MakeAttribute("status-message", goipp.TagText, goipp.String(msg)))

	err := check(goipp.OpGetPrinterAttributes, resp)
	if err == nil {
		t.Fatal("want an error")
	}

	var ippErr *Error
	if !errors.As(err, &ippErr) {
		t.Fatalf("err is %T, want *ipp.Error", err)
	}
	if ippErr.Message != msg {
		t.Errorf("Message = %q, want %q", ippErr.Message, msg)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("Error() = %q, should include the server message", err.Error())
	}
}

// CUPS mixes pseudo-devices in with real ones: every backend advertises itself
// with a bare scheme as its URI, carrying class=network exactly like a real
// network printer. Showing those in a "printers we found" list would offer the
// user four things to pair with that are not printers.
func TestPairableRejectsBackendPseudoDevices(t *testing.T) {
	real := []string{
		"dnssd://Virtual%20Office%20Printer._ipp._tcp.local/",
		"usb://HP/LaserJet%201018?serial=KP123",
		"socket://192.168.1.50:9100",
		"ipp://printer.local/ipp/print",
		"file:///var/spool/pc-out/file-ps.out",
	}
	for _, uri := range real {
		if !pairable(uri) {
			t.Errorf("pairable(%q) = false, want true", uri)
		}
	}

	// These are exactly what CUPS returns for its own backends.
	pseudo := []string{"beh", "ipp", "ipps", "http", "https", "lpd", "socket", "cups-brf:/", ""}
	for _, uri := range pseudo {
		if pairable(uri) {
			t.Errorf("pairable(%q) = true, want false: that is a backend, not a device", uri)
		}
	}
}

// "Unknown" is CUPS's placeholder for "the backend could not identify this".
// Passing it through would print the word Unknown in a column headed Model, as
// though the printer were manufactured by a company called Unknown.
func TestParseDeviceTreatsUnknownModelAsAbsent(t *testing.T) {
	var attrs goipp.Attributes
	attrs.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String("dnssd://Some%20Printer._ipp._tcp.local/")))
	attrs.Add(goipp.MakeAttribute("device-make-and-model", goipp.TagText, goipp.String("Unknown")))
	attrs.Add(goipp.MakeAttribute("device-class", goipp.TagKeyword, goipp.String("network")))
	attrs.Add(goipp.MakeAttribute("device-info", goipp.TagText, goipp.String("Some Printer")))

	d, ok := parseDevice(attrs)
	if !ok {
		t.Fatal("parseDevice rejected a real device")
	}
	if d.MakeAndModel != "" {
		t.Errorf("MakeAndModel = %q, want empty", d.MakeAndModel)
	}
	if d.Info != "Some Printer" {
		t.Errorf("Info = %q, want the name to survive", d.Info)
	}
	if d.Transport != "dnssd" {
		t.Errorf("Transport = %q, want dnssd", d.Transport)
	}
}
