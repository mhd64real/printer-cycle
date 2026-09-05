package driver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/driver"
)

// The two candidates a real cupsd returns for a real printer, verbatim.
//
// This is the stage's own done-when, and it is worth spelling out why it is not
// obvious. Both drivers claim the same model, so the model signal cannot
// separate them. hpcups is Hewlett-Packard's own and would be the natural guess.
// It is the wrong answer here: it needs a closed vendor binary that exists only
// for x86, so on the Raspberry Pi this is built for it does not run at all.
func TestTheLaserJet1018PicksFoo2zjsOverHpcups(t *testing.T) {
	const printerID = "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;"

	candidates := []driver.Candidate{
		// Device ids as cupsd actually reports them, which is not what the
		// names suggest: the foo2zjs PPD declares CMD:ACL, and the hpcups one
		// declares no command set and spells the model in lower case. Neither
		// matches the printer's own CMD:ZJS, so the language signal fires for
		// neither and the choice rests on the plugin and the recommendation.
		{
			PPD:                       "drv:///hpcups.drv/hp-laserjet_1018.ppd",
			MakeAndModel:              "HP LaserJet 1018, hpcups 3.22.10, requires proprietary plugin",
			DeviceID:                  "MFG:Hewlett-Packard;MDL:hp laserjet 1018;DES:hp laserjet 1018;",
			RequiresProprietaryPlugin: true,
		},
		{
			PPD:          "foo2zjs:0/ppd/foo2zjs/HP-LaserJet_1018.ppd",
			MakeAndModel: "HP LaserJet 1018 Foomatic/foo2zjs-z1 (recommended)",
			DeviceID: "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ACL;" +
				"DES:HP LaserJet 1018;DRV:Dfoo2zjs-z1,R1,M0,TF;",
			Recommended: true,
		},
	}

	best, safe := driver.Best(printerID, candidates)
	if !strings.Contains(best.PPD, "foo2zjs") {
		t.Errorf("chose %q, want the foo2zjs driver", best.PPD)
	}
	if !safe {
		t.Error("an open driver for the exact model was not treated as a safe automatic choice")
	}
	t.Logf("chosen because: %s", strings.Join(best.Why, ", "))
}

// CUPS matches loosely. Asking for a LaserJet 4 really does return drivers for
// a Color LaserJet 4610 and a Color LaserJet 4730 MFP, which are other
// printers whose names happen to contain the same characters.
func TestADriverForAnotherPrinterNeverWins(t *testing.T) {
	const printerID = "MFG:HP;MDL:LaserJet 4;"

	candidates := []driver.Candidate{
		{
			PPD:          "wrong-1",
			MakeAndModel: "HP Color LaserJet 4610 Foomatic/Postscript (recommended)",
			DeviceID:     "MFG:HP;MDL:Color LaserJet 4610;DRV:DPostscript,R0,M0,TP;",
			Recommended:  true,
		},
		{
			PPD:          "wrong-2",
			MakeAndModel: "HP Color LaserJet 4730 MFP Foomatic/Postscript",
			DeviceID:     "MFG:HP;MDL:Color LaserJet 4730 MFP;DRV:DPostscript,R0,M0,TP;",
		},
		{
			PPD:          "right",
			MakeAndModel: "HP LaserJet 4 Foomatic/lj4dith",
			DeviceID:     "MFG:HP;MDL:LaserJet 4;DRV:Dlj4dith,R0,M0,TG;",
		},
		{
			PPD:          "wrong-3",
			MakeAndModel: "HP LaserJet 4M Foomatic/ljet4",
			DeviceID:     "MFG:HP;MDL:LaserJet 4M;DRV:Dljet4,R0,M0,Sv,TG;",
		},
	}

	best, safe := driver.Best(printerID, candidates)
	if best.PPD != "right" {
		t.Errorf("chose %q, want the driver for the printer that was actually asked about", best.PPD)
	}
	if !safe {
		t.Error("an exact match was not treated as safe")
	}

	// And the wrong ones sort below it, so a person picking by hand is not
	// offered a Color LaserJet 4610 first either.
	ranked := driver.Rank(printerID, candidates)
	if ranked[0].PPD != "right" {
		t.Errorf("the list starts with %q", ranked[0].PPD)
	}
}

// An exact model needing a closed binary beats a different model that does not.
// A driver that cannot run is a problem to be reported; a driver for the wrong
// printer is a wrong answer that looks like a right one.
func TestTheRightModelBeatsAWorkingWrongOne(t *testing.T) {
	const printerID = "MFG:HP;MDL:LaserJet 4;"

	candidates := []driver.Candidate{
		{
			PPD:          "other-model",
			MakeAndModel: "HP LaserJet 4M Foomatic/ljet4 (recommended)",
			DeviceID:     "MFG:HP;MDL:LaserJet 4M;",
			Recommended:  true,
		},
		{
			PPD:                       "this-model",
			MakeAndModel:              "HP LaserJet 4, hpcups, requires proprietary plugin",
			DeviceID:                  "MFG:HP;MDL:LaserJet 4;",
			RequiresProprietaryPlugin: true,
		},
	}

	best, safe := driver.Best(printerID, candidates)
	if best.PPD != "this-model" {
		t.Errorf("chose %q, want the driver for this model", best.PPD)
	}
	if safe {
		t.Error("a driver needing a closed vendor binary was chosen silently")
	}
}

// Nothing matching the model at all is offered, not applied. CUPS returning
// something means the strings overlapped, not that it will drive the hardware.
func TestNoModelMatchIsOfferedRatherThanChosen(t *testing.T) {
	best, safe := driver.Best("MFG:HP;MDL:LaserJet 4;", []driver.Candidate{
		{PPD: "close-enough", DeviceID: "MFG:HP;MDL:LaserJet 4000;", Recommended: true},
	})
	if best.PPD != "close-enough" {
		t.Errorf("offered %q, and there was something to offer", best.PPD)
	}
	if safe {
		t.Error("a driver for a different model was treated as a safe automatic choice")
	}
}

// A printer whose maker calls itself two things is still one maker.
func TestManufacturerNamesThatDisagree(t *testing.T) {
	ranked := driver.Rank("MFG:Hewlett-Packard;MDL:LaserJet 1018;", []driver.Candidate{
		{PPD: "hp", DeviceID: "MFG:HP;MDL:LaserJet 1018;"},
	})
	if len(ranked[0].Why) == 0 {
		t.Fatal("nothing matched at all")
	}
	var sameMaker bool
	for _, why := range ranked[0].Why {
		if strings.Contains(why, "manufacturer") {
			sameMaker = true
		}
	}
	if !sameMaker {
		t.Errorf("HP and Hewlett-Packard were treated as different companies: %v", ranked[0].Why)
	}
}

// Same score, same order as CUPS gave them. A pairing screen that suggested a
// different driver on each visit would be worse than one suggesting nothing.
func TestRankingIsStable(t *testing.T) {
	candidates := []driver.Candidate{
		{PPD: "a", DeviceID: "MFG:HP;MDL:LaserJet 4;"},
		{PPD: "b", DeviceID: "MFG:HP;MDL:LaserJet 4;"},
		{PPD: "c", DeviceID: "MFG:HP;MDL:LaserJet 4;"},
	}

	for range 5 {
		ranked := driver.Rank("MFG:HP;MDL:LaserJet 4;", candidates)
		if ranked[0].PPD != "a" || ranked[1].PPD != "b" || ranked[2].PPD != "c" {
			t.Fatalf("order moved: %s %s %s", ranked[0].PPD, ranked[1].PPD, ranked[2].PPD)
		}
	}
}

func TestNoCandidates(t *testing.T) {
	if _, safe := driver.Best("MFG:HP;MDL:LaserJet 4;", nil); safe {
		t.Error("nothing at all was reported as a safe choice")
	}
}

// Why is what makes an automatic choice checkable rather than merely confident.
//
// The candidate here is constructed rather than taken from the catalogue: it is
// checking that every signal reports itself, which needs one that fires them
// all, and no real PPD in hand does. The real LaserJet 1018 pair is
// TestTheLaserJet1018PicksFoo2zjsOverHpcups, verbatim from cupsd.
func TestTheReasonsAreReported(t *testing.T) {
	best, _ := driver.Best("MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;", []driver.Candidate{
		{
			PPD:          "foo2zjs",
			DeviceID:     "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;",
			Recommended:  true,
			MakeAndModel: "HP LaserJet 1018 Foomatic/foo2zjs-z1 (recommended)",
		},
	})

	want := []string{"exact model", "proprietary plugin", "manufacturer", "recommends", "speaks"}
	joined := strings.Join(best.Why, " | ")
	for _, fragment := range want {
		if !strings.Contains(joined, fragment) {
			t.Errorf("the reasons do not mention %q: %s", fragment, joined)
		}
	}
	if best.Score == 0 {
		t.Error("everything matched and the score is zero")
	}
}

// The cache is the whole reason this type exists: a filtered PPD query costs
// between 2.7 and 5.2 seconds against a real cupsd, every time, because CUPS
// does not cache it either.
func TestASecondLookupDoesNotAskAgain(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		return []driver.Candidate{{PPD: "one", DeviceID: "MFG:HP;MDL:LaserJet 4;"}}, nil
	})

	for range 5 {
		if _, err := f.Candidates(context.Background(), "MFG:HP;MDL:LaserJet 4;"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("asked CUPS %d times for the same printer, want 1", calls)
	}
}

// The same printer described two ways is one printer.
func TestTheCacheKeyIsTheCanonicalDeviceID(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		return nil, nil
	})

	for _, spelling := range []string{
		"MFG:HP;MDL:LaserJet 4;",
		"MFG:HP;MDL:LaserJet 4",
		"MANUFACTURER:HP;MODEL:LaserJet 4;",
		"  MFG : HP ; MDL : LaserJet 4 ;",
	} {
		if _, err := f.Candidates(context.Background(), spelling); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("asked CUPS %d times for one printer spelled four ways, want 1", calls)
	}
}

// A device id naming no hardware must not become a cache entry, or every such
// device shares one answer, which is the worst possible thing to cache.
func TestADeviceThatNamesNothingIsNotCached(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		return []driver.Candidate{{PPD: "something"}}, nil
	})

	got, err := f.Candidates(context.Background(), "CMD:PCL;")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("returned %d candidates for a device that named no hardware", len(got))
	}
	if calls != 0 {
		t.Errorf("asked CUPS %d times about a printer it could not describe", calls)
	}
}

// A failed lookup is not an answer, so it must not be remembered as one.
func TestAFailedLookupIsNotCached(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("cups is not answering")
		}
		return []driver.Candidate{{PPD: "one", DeviceID: "MFG:HP;MDL:LaserJet 4;"}}, nil
	})

	if _, err := f.Candidates(context.Background(), "MFG:HP;MDL:LaserJet 4;"); err == nil {
		t.Fatal("a failing lookup reported success")
	}
	got, err := f.Candidates(context.Background(), "MFG:HP;MDL:LaserJet 4;")
	if err != nil {
		t.Fatalf("the retry failed too: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("the retry returned %d candidates, so the failure was cached", len(got))
	}
}

func TestForgetting(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		return nil, nil
	})

	ctx := context.Background()
	_, _ = f.Candidates(ctx, "MFG:HP;MDL:LaserJet 4;")
	f.Forget()
	_, _ = f.Candidates(ctx, "MFG:HP;MDL:LaserJet 4;")

	if calls != 2 {
		t.Errorf("asked CUPS %d times across a Forget, want 2", calls)
	}
}

// Held for minutes, not for the life of the process. The only thing this can be
// wrong about is a driver installed while an entry is held, and self-healing in
// minutes beats explaining why a newly installed driver is not offered.
func TestEntriesExpire(t *testing.T) {
	var calls int
	f := driver.New(func(context.Context, string) ([]driver.Candidate, error) {
		calls++
		return nil, nil
	})

	ctx := context.Background()
	_, _ = f.Candidates(ctx, "MFG:HP;MDL:LaserJet 4;")

	driver.SetClock(f, func() time.Time { return time.Now().Add(11 * time.Minute) })
	_, _ = f.Candidates(ctx, "MFG:HP;MDL:LaserJet 4;")

	if calls != 2 {
		t.Errorf("asked CUPS %d times across an expiry, want 2", calls)
	}
}
