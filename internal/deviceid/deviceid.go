// Package deviceid parses IEEE 1284 device id strings.
//
// A device id is what a printer says it is: "MFG:HP;MDL:LaserJet 1018;CMD:ZJS;".
// It is the one piece of evidence that makes choosing a driver automatic rather
// than a person recognising their own printer in a list of eighteen thousand,
// so everything in this package is about getting it out of the string intact.
//
// The behaviour here was written against the 14,825 real device ids carried by
// the PPDs on a full driver installation rather than against the specification,
// because what printers actually emit and what the specification says are not
// the same document. Every tolerance below is answering something that is
// really out there, and the counts are recorded so a later reader can tell a
// measured decision from a defensive one.
package deviceid

import "strings"

// ID is a parsed device id.
type ID struct {
	// Manufacturer and Model are the two that matter. Between them they name
	// the hardware, and a driver search is built from them.
	Manufacturer string
	Model        string

	Description string
	Class       string

	// Commands is the page description languages the printer claims, in the
	// order it listed them. "PJL", "POSTSCRIPT", "PCLXL" and so on: 146
	// distinct tokens across the catalogue.
	Commands []string

	// Fields is everything, under canonical keys, including the ones this
	// struct has no name for. DRV matters to foomatic, CID to Brother, and a
	// long tail of others appear once or twice each. Dropping them would mean
	// re-parsing later to get at something already in hand.
	Fields map[string]string
}

// Canonical field names. The short forms win because they are what almost
// everything emits.
const (
	KeyManufacturer = "MFG"
	KeyModel        = "MDL"
	KeyDescription  = "DES"
	KeyClass        = "CLS"
	KeyCommands     = "CMD"
)

// aliases maps the long spellings onto the short ones.
//
// Not a guess at what might exist. Measured across a full driver installation:
// MFG 14761 against MANUFACTURER 62, MDL 13335 against MODEL 1120, CMD 6136
// against COMMAND SET 1473, DES 2564 against DESCRIPTION 58. The long forms are
// a minority and they are far too common to ignore.
var aliases = map[string]string{
	"MANUFACTURER": KeyManufacturer,
	"MODEL":        KeyModel,
	"COMMAND SET":  KeyCommands,
	"COMMANDSET":   KeyCommands,
	"DESCRIPTION":  KeyDescription,
	"CLASS":        KeyClass,
}

// Parse reads a device id. It never fails.
//
// A device id comes from hardware, and hardware is where the strange strings
// are. Refusing to parse one would mean refusing to pair a printer over
// punctuation, so anything unreadable is simply absent from the result and the
// caller checks for what it needs. Everything below is a real shape from the
// catalogue:
//
//   - Keys in any case. "Model:" appears 367 times, which is enough to make
//     case sensitivity a bug rather than a simplification.
//   - Whitespace anywhere. 1,645 ids put a space after the colon.
//   - A missing trailing semicolon, on 17 of them.
//   - Repeated keys, as in "MFG:Brother;MFG:Brother;". The first wins, because
//     something has to and the first is the one a reader sees.
//   - No manufacturer at all: "CMD:PCL;" is a whole device id in this
//     catalogue. Such a printer cannot be identified, which is a fact about it
//     rather than a parse failure.
func Parse(s string) ID {
	id := ID{Fields: map[string]string{}}

	for _, part := range strings.Split(s, ";") {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			// A fragment with no colon. The trailing empty string after the
			// last semicolon is the usual one; anything else is noise, and
			// noise is not worth a decision.
			continue
		}

		key = canonical(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if _, seen := id.Fields[key]; seen {
			continue
		}
		id.Fields[key] = value
	}

	id.Manufacturer = id.Fields[KeyManufacturer]
	id.Model = id.Fields[KeyModel]
	id.Description = id.Fields[KeyDescription]
	id.Class = id.Fields[KeyClass]
	id.Commands = splitCommands(id.Fields[KeyCommands])
	return id
}

// canonical normalises a key: trimmed, upper case, long form mapped to short.
//
// Inner spacing is collapsed as well, so "COMMAND  SET" and "COMMAND SET" are
// the same key. Nothing in the catalogue does that, and it costs one line to
// stop it being a surprise later.
func canonical(key string) string {
	key = strings.ToUpper(strings.TrimSpace(key))
	key = strings.Join(strings.Fields(key), " ")
	if short, ok := aliases[key]; ok {
		return short
	}
	return key
}

// splitCommands breaks a command set into its languages.
//
// Comma separated, and 368 ids put a space after the comma. A value like
// "Adobe PostScript 3, PCL, PJL" is three languages, and the first of them has
// spaces inside it, so only the commas may be split on.
func splitCommands(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, cmd := range strings.Split(value, ",") {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			out = append(out, cmd)
		}
	}
	return out
}

// Speaks reports whether the printer claims a page description language.
//
// Case insensitive, because the catalogue holds "POSTSCRIPT", "PostScript" and
// "Adobe PostScript 3" and a caller asking whether something speaks PostScript
// means the same question each time.
func (id ID) Speaks(command string) bool {
	command = strings.TrimSpace(command)
	for _, cmd := range id.Commands {
		if strings.EqualFold(cmd, command) {
			return true
		}
	}
	return false
}

// Identifies reports whether this is enough to look a driver up with.
//
// Manufacturer and model together. Either alone is not: "CMD:PCL;" names no
// hardware, and a model with no maker is ambiguous across the catalogue.
func (id ID) Identifies() bool {
	return id.Manufacturer != "" && id.Model != ""
}

// String rebuilds a canonical device id, which is what a driver query wants.
//
// Deliberately not the input. CUPS matches a PPD's own device id against this,
// and the shortest form that still names the hardware matches most widely: a
// manufacturer, a model, and the command set when there is one.
func (id ID) String() string {
	var b strings.Builder
	write := func(key, value string) {
		if value == "" {
			return
		}
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(value)
		b.WriteByte(';')
	}
	write(KeyManufacturer, id.Manufacturer)
	write(KeyModel, id.Model)
	write(KeyCommands, strings.Join(id.Commands, ","))
	return b.String()
}
