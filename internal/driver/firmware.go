package driver

import (
	"strings"

	"github.com/mhd64real/printer-cycle/internal/deviceid"
)

// Firmware is what a printer needs before it can print at all.
//
// A small number of laser printers ship with no firmware in them. They load it
// from the host over USB at every power-on, and the file cannot be redistributed
// by anybody but the manufacturer, so no Linux distribution ships it. The
// printer is otherwise perfectly good hardware: it prints nothing, reports no
// error, and looks broken.
//
// This is the single most common way an old printer looks unsupported when it is
// not, which is most of what this project is for.
type Firmware struct {
	// File is what has to be fetched, named as the driver package names it.
	File string

	// Model is the model this was matched as, which is not always the model on
	// the front of the printer: a P1007 loads the P1005 file.
	Model string
}

// firmwareNeeded maps a model to the file it loads at power-on.
//
// Taken from the foo2zjs package itself rather than from memory or the web:
// /usr/sbin/getweb names the files it can fetch, and
// /usr/share/foo2zjs/hplj10xx_gui.tcl maps models to them, including the two
// cases where a printer loads another model's file. On a full driver
// installation /usr/lib/firmware/hp exists and is empty, which is what a
// non-redistributable file looks like after packaging.
//
// Only models with evidence behind them. A longer list covering printers nobody
// has checked would be worse than a short one, because a wrong entry here tells
// somebody their working printer needs something it does not.
var firmwareNeeded = map[string]string{
	"hp laserjet 1000":  "sihp1000.dl",
	"hp laserjet 1005":  "sihp1005.dl",
	"hp laserjet 1018":  "sihp1018.dl",
	"hp laserjet 1020":  "sihp1020.dl",
	"hp laserjet p1005": "sihpP1005.dl",
	"hp laserjet p1006": "sihpP1006.dl",

	// These two load another model's firmware, which is the driver package's
	// own mapping and not a guess at a family resemblance.
	"hp laserjet p1007": "sihpP1005.dl",
	"hp laserjet p1008": "sihpP1006.dl",

	"hp laserjet p1505": "sihpP1505.dl",
}

// NeedsFirmware reports whether a printer loads firmware from the host.
//
// Matched on the model, with the manufacturer folded in when the model does not
// already carry it: a 1018 reports "MDL:HP LaserJet 1018" but nothing guarantees
// every printer names itself so completely.
func NeedsFirmware(printerDeviceID string) (Firmware, bool) {
	id := deviceid.Parse(printerDeviceID)
	if id.Model == "" {
		return Firmware{}, false
	}

	for _, candidate := range []string{
		strings.ToLower(id.Model),
		strings.ToLower(strings.TrimSpace(id.Manufacturer + " " + id.Model)),
	} {
		candidate = strings.Join(strings.Fields(candidate), " ")
		if file, ok := firmwareNeeded[candidate]; ok {
			return Firmware{File: file, Model: candidate}, true
		}
	}
	return Firmware{}, false
}

// Blocked is a driver that must never be chosen for a printer, whatever it
// scores.
type Blocked struct {
	// Model is matched against the printer's model, lower case.
	Model string

	// PPD is matched as a substring of the driver's ppd name, because a ppd
	// name carries its package and its path and only part of it is stable.
	PPD string

	// Why is shown to whoever asks, so a refusal is never mysterious.
	Why string
}

// blocked is the known-bad list.
//
// Deliberately empty.
//
// The mechanism is here because a known-bad match needs somewhere to go the day
// one is found, and finding one after release should be a one-line change
// rather than a design. What is not here is entries: every one of them is a
// claim that a particular driver ruins a particular printer, and there is no
// way to make that claim honestly without having seen it happen.
//
// A wrong entry is worse than an empty list. It would take a driver that works
// away from somebody and give them a reason that was never true, and nothing in
// the interface would ever contradict it.
//
// Entries need a reproducible report: the printer's device id, the ppd, and
// what came out of the printer.
var blocked []Blocked

// blockedBy returns the reason a driver is refused for a printer, if it is.
func blockedBy(printer deviceid.ID, c Candidate) (string, bool) {
	model := strings.ToLower(printer.Model)
	for _, b := range blocked {
		if model == "" || !strings.EqualFold(model, b.Model) {
			continue
		}
		if b.PPD != "" && !strings.Contains(c.PPD, b.PPD) {
			continue
		}
		return b.Why, true
	}
	return "", false
}
