package protocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// The LaserJet 1018: a cheap host-based laser from 2005, squarely the hardware
// this project exists to revive.
const laserJet1018 = "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;"

// The stage's done-when: a device becomes a working queue in one call, with the
// driver chosen rather than asked about.
func TestAddingAPrinterInOneCall(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///var/spool/pc-out/paired.out",
		"name":       uniqueName(t),
		"device_id":  laserJet1018,
		"location":   "Upstairs",
	})
	if resp.Error != nil {
		t.Fatalf("pairing failed: %v", resp.Error)
	}

	var added struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		QueueName  string `json:"queue_name"`
		PPD        string `json:"ppd"`
		AutoChosen bool   `json:"driver_chosen_automatically"`
	}
	if err := json.Unmarshal(resp.Result, &added); err != nil {
		t.Fatal(err)
	}
	t.Logf("queue=%s name=%q driver=%s automatic=%v",
		added.QueueName, added.Name, added.PPD, added.AutoChosen)

	// The readable name survives, and the queue name is one CUPS accepts.
	if added.Name != uniqueName(t) {
		t.Errorf("name = %q, want what the user typed", added.Name)
	}
	if strings.Contains(added.QueueName, " ") {
		t.Errorf("queue name %q contains a space, which CUPS will not accept", added.QueueName)
	}
	if !added.AutoChosen {
		t.Error("no driver was chosen automatically, which is the entire point of the pair button")
	}
	// foo2zjs is the open driver for this printer. hpcups also claims it and
	// needs HP's closed plugin, which will never run on ARM.
	if added.PPD == "" {
		t.Error("no driver was recorded")
	}

	// The queue exists in CUPS, not merely in printer-cycle's database. Asked
	// of the container rather than over IPP: printer-cycle creates queues
	// unshared, and CUPS hides those from remote clients.
	if !cupsHasQueue(t, added.QueueName) {
		t.Fatalf("queue %q is recorded but does not exist in CUPS", added.QueueName)
	}

	// And it comes back in a listing.
	list := c.call("printers.list", nil)
	if list.Error != nil {
		t.Fatal(list.Error)
	}
	var listed struct {
		Printers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"printers"`
	}
	if err := json.Unmarshal(list.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Printers) != 1 || listed.Printers[0].ID != added.ID {
		t.Errorf("listing = %+v", listed.Printers)
	}

	// Removing takes it out of both places.
	if resp := c.call("printers.remove", map[string]any{"id": added.ID}); resp.Error != nil {
		t.Fatalf("removing: %v", resp.Error)
	}
	if cupsHasQueue(t, added.QueueName) {
		t.Error("the queue is still in CUPS after being removed")
	}
}

// Two printers can share a name. A household with two identical printers is
// ordinary, and refusing the second would be a strange place to draw a line.
func TestTwoPrintersMayShareAName(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	var queues []string
	for range 2 {
		resp := c.call("printers.add", map[string]any{
			"device_uri": "file:///var/spool/pc-out/dup.out",
			"name":       uniqueName(t),
			"ppd":        "drv:///sample.drv/generic.ppd",
		})
		if resp.Error != nil {
			t.Fatalf("adding: %v", resp.Error)
		}
		var added struct {
			ID        string `json:"id"`
			QueueName string `json:"queue_name"`
		}
		if err := json.Unmarshal(resp.Result, &added); err != nil {
			t.Fatal(err)
		}
		queues = append(queues, added.QueueName)
		t.Cleanup(func() { c.call("printers.remove", map[string]any{"id": added.ID}) })
	}

	if queues[0] == queues[1] {
		t.Fatalf("both printers got the queue name %q", queues[0])
	}
	t.Logf("queues: %v", queues)
}

// A driver CUPS will not accept must leave nothing behind. Otherwise a failed
// attempt consumes the name the user asked for and the second try is called
// "Office Laser 2" for no visible reason.
func TestAFailedAddLeavesNothingBehind(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///var/spool/pc-out/bad.out",
		"name":       uniqueName(t),
		"ppd":        "drv:///no-such.drv/nothing.ppd",
	})
	if resp.Error == nil {
		t.Fatal("a driver that does not exist was accepted")
	}

	printers, err := db.Printers(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(printers) != 0 {
		t.Errorf("a failed add left %d records behind: %+v", len(printers), printers)
	}
}

func TestDriverCandidatesReportsTheSignals(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	resp := c.call("printers.driverCandidates", map[string]any{"device_id": laserJet1018})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}

	var result struct {
		Candidates []struct {
			PPD                       string `json:"ppd"`
			MakeAndModel              string `json:"make_and_model"`
			Recommended               bool   `json:"recommended"`
			RequiresProprietaryPlugin bool   `json:"requires_proprietary_plugin"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("no candidates for a printer the installed drivers cover")
	}

	var sawRecommended, sawPlugin bool
	for _, cand := range result.Candidates {
		t.Logf("%-48s recommended=%v plugin=%v", cand.PPD, cand.Recommended, cand.RequiresProprietaryPlugin)
		sawRecommended = sawRecommended || cand.Recommended
		sawPlugin = sawPlugin || cand.RequiresProprietaryPlugin
	}
	// Both signals are present for this printer, which is why it was chosen as
	// the example: one open driver marked recommended, one needing HP's closed
	// plugin that will never run on ARM.
	if !sawRecommended {
		t.Error("no candidate was marked recommended, so CUPS's own hint is not being read")
	}
	if !sawPlugin {
		t.Error("no candidate was flagged as needing a proprietary plugin")
	}
}

// Choosing must never land on a driver that cannot run here while an open one is
// available.
func TestAutomaticChoiceAvoidsProprietaryPlugins(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///var/spool/pc-out/hp1018.out",
		"name":       uniqueName(t),
		"device_id":  laserJet1018,
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var added struct {
		ID  string `json:"id"`
		PPD string `json:"ppd"`
	}
	if err := json.Unmarshal(resp.Result, &added); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.call("printers.remove", map[string]any{"id": added.ID}) })

	if strings.Contains(added.PPD, "hpcups") {
		t.Errorf("chose %s, which needs a closed vendor plugin that cannot run on ARM", added.PPD)
	}
	t.Logf("chose %s", added.PPD)
}

func TestAddingNeedsPrintersManage(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopePrintersRead})

	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///tmp/x", "name": "X", "ppd": "drv:///sample.drv/generic.ppd",
	})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("printers.read alone was allowed to add a printer: %v", resp.Error)
	}
}

func TestRemovingSomethingThatIsNotThere(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("printers.remove", map[string]any{"id": "prn_nothing"})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeUnknownPrinter {
		t.Errorf("removing a printer that does not exist gave %v", resp.Error)
	}
}
