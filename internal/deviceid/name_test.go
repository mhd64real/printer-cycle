package deviceid_test

import (
	"testing"

	"github.com/mhd64real/printer-cycle/internal/deviceid"
)

// One place decides what a printer is called, and this is it.
//
// Every case is a real pairing of device id and make-and-model, taken from a
// live cupsd or from the driver catalogue.
func TestPrinterName(t *testing.T) {
	for name, tc := range map[string]struct {
		deviceID     string
		makeAndModel string
		info         string
		uri          string
		want         string
	}{
		"the maker named twice, under two names": {
			deviceID:     "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;",
			makeAndModel: "Hewlett-Packard HP LaserJet 1018",
			want:         "HP LaserJet 1018",
		},
		"the maker named twice, under one name": {
			deviceID:     "MFG:HP;MDL:HP LaserJet 4000;",
			makeAndModel: "HP HP LaserJet 4000",
			want:         "HP LaserJet 4000",
		},
		"an ordinary printer keeps its maker": {
			deviceID:     "MFG:HP;MDL:LaserJet 4;",
			makeAndModel: "HP LaserJet 4",
			want:         "HP LaserJet 4",
		},
		"a model that names no maker": {
			deviceID:     "MFG:Brother;MDL:HL-2270DW;",
			makeAndModel: "Brother HL-2270DW",
			want:         "Brother HL-2270DW",
		},
		"no maker in the device id at all": {
			deviceID: "MDL:OKIDATA OKIPAGE 6e;CMD:PCL;",
			want:     "OKIDATA OKIPAGE 6e",
		},
		"no usable device id, so the concatenated string is all there is": {
			deviceID:     "CMD:PCL;",
			makeAndModel: "Example Example Printer",
			want:         "Example Printer",
		},
		"nothing but a description": {
			deviceID: "",
			info:     "The printer in the hall",
			want:     "The printer in the hall",
		},
		"nothing at all but an address": {
			deviceID: "",
			uri:      "socket://192.0.2.10:9100",
			want:     "socket://192.0.2.10:9100",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := deviceid.PrinterName(tc.deviceID, tc.makeAndModel, tc.info, tc.uri)
			if got != tc.want {
				t.Errorf("PrinterName = %q, want %q", got, tc.want)
			}
		})
	}
}

// The aliases are the reason this could not live in the interface: knowing that
// Hewlett-Packard and HP are one company is not something a string comparison
// can work out.
func TestSameManufacturer(t *testing.T) {
	for _, pair := range [][2]string{
		{"HP", "Hewlett-Packard"},
		{"hewlett-packard", "hp"},
		{"Seiko Epson", "EPSON"},
		{"Eastman Kodak Company", "KODAK"},
		{"Brother", "Brother Industries"},
	} {
		if !deviceid.SameManufacturer(pair[0], pair[1]) {
			t.Errorf("%q and %q were treated as different companies", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"HP", "Canon"},
		{"Brother", ""},
		{"", ""},
	} {
		if deviceid.SameManufacturer(pair[0], pair[1]) {
			t.Errorf("%q and %q were treated as the same company", pair[0], pair[1])
		}
	}
}
