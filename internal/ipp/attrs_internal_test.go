package ipp

import (
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
