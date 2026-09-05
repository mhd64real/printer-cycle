package deviceid_test

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/deviceid"
)

// Every case here is a real string, or a real shape, taken from the device ids
// carried by the PPDs on a full driver installation. The counts in the names
// are how often that shape occurs across 13,109 unique ids.
func TestParsingRealDeviceIDs(t *testing.T) {
	for name, tc := range map[string]struct {
		in    string
		want  deviceid.ID
		notes string
	}{
		"the ordinary case": {
			in: "MFG:HP;MDL:LaserJet 1018;CMD:ZJS;",
			want: deviceid.ID{
				Manufacturer: "HP", Model: "LaserJet 1018", Commands: []string{"ZJS"},
			},
		},
		"long key spellings, 1473 of them for the command set": {
			in: "MFG:Kyocera;Model:Kyocera FS-6500+;COMMAND SET: POSTSCRIPT,PJL,PCL;",
			want: deviceid.ID{
				Manufacturer: "Kyocera",
				Model:        "Kyocera FS-6500+",
				Commands:     []string{"POSTSCRIPT", "PJL", "PCL"},
			},
			notes: "Model in mixed case appears 367 times, so keys cannot be case sensitive",
		},
		"a command with spaces inside it, 368 ids put spaces after commas": {
			in: "MFG:Xerox;MDL:Phaser 7300DX;CMD:Adobe PostScript 3, PCL, PJL;",
			want: deviceid.ID{
				Manufacturer: "Xerox",
				Model:        "Phaser 7300DX",
				Commands:     []string{"Adobe PostScript 3", "PCL", "PJL"},
			},
			notes: "only commas separate, or the first language becomes three",
		},
		"no trailing semicolon, 17 of them": {
			in: "MFG:KODAK;CMD:KODAK305;MDL:305 Photo Printer;CLS:PRINTER;DES:KODAK 305 Photo Printer",
			want: deviceid.ID{
				Manufacturer: "KODAK", Model: "305 Photo Printer",
				Class: "PRINTER", Description: "KODAK 305 Photo Printer",
				Commands: []string{"KODAK305"},
			},
		},
		"a repeated key": {
			in: "MFG:Brother;MFG:Brother;CMD:PJL,HBP;MDL:FAX-2840;CLS:PRINTER;",
			want: deviceid.ID{
				Manufacturer: "Brother", Model: "FAX-2840", Class: "PRINTER",
				Commands: []string{"PJL", "HBP"},
			},
		},
		"no manufacturer at all, which is a whole device id in the catalogue": {
			in:   "CMD:PCL;",
			want: deviceid.ID{Commands: []string{"PCL"}},
			notes: "the printer cannot be identified, which is a fact about it " +
				"rather than a parse failure",
		},
		"a model with no maker": {
			in: "MDL:OKIDATA OKIPAGE 6e;CMD:ENHANCED PCL5,PJL;DES:OKIDATA OKIPAGE 6e (HP4P);",
			want: deviceid.ID{
				Model: "OKIDATA OKIPAGE 6e", Description: "OKIDATA OKIPAGE 6e (HP4P)",
				Commands: []string{"ENHANCED PCL5", "PJL"},
			},
		},
		"empty": {
			in:   "",
			want: deviceid.ID{},
		},
		"nothing that looks like a device id": {
			in:   "not a device id at all",
			want: deviceid.ID{},
		},
		"a colon and nothing else": {
			in:   ":",
			want: deviceid.ID{},
		},
		"whitespace everywhere": {
			in: "  MFG : HP ;  MDL :  LaserJet 1018  ; CMD : ZJS , PJL ;",
			want: deviceid.ID{
				Manufacturer: "HP", Model: "LaserJet 1018",
				Commands: []string{"ZJS", "PJL"},
			},
		},
		"a value containing a colon": {
			in: "MFG:HP;MDL:LaserJet 1018;DES:Prints at 12:00;",
			want: deviceid.ID{
				Manufacturer: "HP", Model: "LaserJet 1018", Description: "Prints at 12:00",
			},
			notes: "split on the first colon only, or the description loses its time",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := deviceid.Parse(tc.in)

			if got.Manufacturer != tc.want.Manufacturer {
				t.Errorf("manufacturer = %q, want %q", got.Manufacturer, tc.want.Manufacturer)
			}
			if got.Model != tc.want.Model {
				t.Errorf("model = %q, want %q", got.Model, tc.want.Model)
			}
			if got.Description != tc.want.Description {
				t.Errorf("description = %q, want %q", got.Description, tc.want.Description)
			}
			if got.Class != tc.want.Class {
				t.Errorf("class = %q, want %q", got.Class, tc.want.Class)
			}
			if !slices.Equal(got.Commands, tc.want.Commands) {
				t.Errorf("commands = %q, want %q", got.Commands, tc.want.Commands)
			}
			if tc.notes != "" {
				t.Log(tc.notes)
			}
		})
	}
}

// A real typo, in a real driver package.
//
// One PPD in the catalogue writes MCL where MDL was meant, so a Kodak 605 will
// not match itself automatically. The parser does not correct it, and that is
// deliberate: guessing what a broken key was meant to say is how a parser
// starts inventing hardware. It is recorded here so the next person to find it
// knows it was seen and decided rather than missed.
func TestATypoInTheCatalogueIsNotGuessedAt(t *testing.T) {
	id := deviceid.Parse(
		"MFG:Eastman Kodak Company;CMD:SUPCC;MCL:KODAK 605 Photo Printer;" +
			"CLS:PRINTER;DES:Thermal Dye Photo Printer;")

	if id.Model != "" {
		t.Errorf("model = %q, which was guessed from a misspelled key", id.Model)
	}
	if id.Identifies() {
		t.Error("it claims to identify a printer on a key that does not exist")
	}
	// The value is still there for anybody who wants to deal with it.
	if id.Fields["MCL"] != "KODAK 605 Photo Printer" {
		t.Errorf("MCL = %q, and it should be kept whatever it means", id.Fields["MCL"])
	}
}

// Keys the struct has no name for are kept, because something later wants them.
func TestUnnamedFieldsSurvive(t *testing.T) {
	id := deviceid.Parse("MFG:Brother;MDL:HL-1870N;DRV:Dpxlmono,R0,M0,TG;CID:Brother Laser Type1;")

	if id.Fields["DRV"] != "Dpxlmono,R0,M0,TG" {
		t.Errorf("DRV = %q, which foomatic drivers are selected by", id.Fields["DRV"])
	}
	if id.Fields["CID"] != "Brother Laser Type1" {
		t.Errorf("CID = %q", id.Fields["CID"])
	}
}

func TestSpeaks(t *testing.T) {
	id := deviceid.Parse("MFG:Xerox;MDL:Phaser;CMD:Adobe PostScript 3, PCL, PJL;")

	for _, want := range []string{"PCL", "pcl", "Adobe PostScript 3", "adobe postscript 3"} {
		if !id.Speaks(want) {
			t.Errorf("does not claim %q, and it does", want)
		}
	}
	for _, wrong := range []string{"POSTSCRIPT", "ZJS", ""} {
		if id.Speaks(wrong) {
			t.Errorf("claims %q, and it does not", wrong)
		}
	}
}

// Identifying a printer takes both halves. A model with no maker is ambiguous
// across a catalogue this size, and a command set names no hardware at all.
func TestIdentifies(t *testing.T) {
	for in, want := range map[string]bool{
		"MFG:HP;MDL:LaserJet 1018;": true,
		"MFG:HP;":                   false,
		"MDL:LaserJet 1018;":        false,
		"CMD:PCL;":                  false,
		"":                          false,
	} {
		if got := deviceid.Parse(in).Identifies(); got != want {
			t.Errorf("Parse(%q).Identifies() = %v, want %v", in, got, want)
		}
	}
}

// The rebuilt form is the shortest thing that still names the hardware, which
// is what a driver query wants.
func TestStringRebuildsAQueryableForm(t *testing.T) {
	id := deviceid.Parse(
		"MFG:KODAK;CMD:KODAK305;MDL:305 Photo Printer;CLS:PRINTER;DES:KODAK 305 Photo Printer")

	want := "MFG:KODAK;MDL:305 Photo Printer;CMD:KODAK305;"
	if got := id.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	// And it survives a round trip, which is the property that makes it usable
	// as a key.
	if again := deviceid.Parse(id.String()); again.String() != want {
		t.Errorf("re-parsing gives %q, want %q", again.String(), want)
	}
}

// The whole catalogue, as a sweep.
//
// The table above says what each shape should produce. This says that nothing
// in 13,109 real strings makes the parser panic, lose the manufacturer it was
// given, or invent one it was not, which is the sort of thing a hand-written
// table never catches because the author has to think of it first.
func TestTheWholeCatalogue(t *testing.T) {
	file, err := os.Open("testdata/catalogue.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var total, identified, withCommands int

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		total++

		id := deviceid.Parse(line)

		// Whatever came out has to have been in the string. This is the check
		// that would catch a split going wrong in a way that still produces
		// plausible looking output.
		if id.Manufacturer != "" && !strings.Contains(line, id.Manufacturer) {
			t.Fatalf("invented a manufacturer %q from %q", id.Manufacturer, line)
		}
		if id.Model != "" && !strings.Contains(line, id.Model) {
			t.Fatalf("invented a model %q from %q", id.Model, line)
		}
		for _, cmd := range id.Commands {
			if !strings.Contains(line, cmd) {
				t.Fatalf("invented a command %q from %q", cmd, line)
			}
			if strings.TrimSpace(cmd) != cmd {
				t.Fatalf("command %q kept its whitespace, from %q", cmd, line)
			}
		}

		if id.Identifies() {
			identified++
		}
		if len(id.Commands) > 0 {
			withCommands++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if total < 10_000 {
		t.Fatalf("only %d device ids in the fixture, which is not the catalogue", total)
	}
	t.Logf("%d device ids: %d identify a printer, %d declare a command set",
		total, identified, withCommands)

	// Measured at 13,103 of 13,109 when this was written, so all but six name
	// their hardware. The floor is far below that on purpose: it is here to
	// catch the parser quietly stopping, not to pin a number that a driver
	// package update would move.
	if identified*100/total < 90 {
		t.Errorf("only %d%% of real device ids identify a printer, which is a parser fault "+
			"rather than a catalogue one", identified*100/total)
	}
}
