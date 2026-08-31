package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// The stage's done-when: an address alone becomes something printable.
func TestProbeIdentifiesAPrinterFromAnAddress(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	// The virtual printer container, published on the host. A genuine IPP
	// printer rather than a print server, which is what a user would be typing
	// the address of.
	resp := c.call("printers.probe", map[string]any{"address": "127.0.0.1:8632"})
	if resp.Error != nil {
		t.Fatalf("probe failed: %v", resp.Error)
	}

	var result struct {
		DeviceURI    string `json:"device_uri"`
		MakeAndModel string `json:"make_and_model"`
		Transport    string `json:"transport"`
		Port         int    `json:"port"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}

	t.Logf("uri=%s model=%q transport=%s", result.DeviceURI, result.MakeAndModel, result.Transport)

	if result.DeviceURI == "" {
		t.Error("no device uri, so nothing could be added from this")
	}
	if result.Transport != "ipp" {
		t.Errorf("transport = %q, want ipp", result.Transport)
	}
	if result.Port != 8632 {
		t.Errorf("port = %d, want the one that was asked for", result.Port)
	}
	// The printer named itself, which is what turns a typed address into a
	// one-click pairing instead of a driver list to scroll through.
	if result.MakeAndModel == "" {
		t.Error("the printer did not identify itself, so no driver could be chosen automatically")
	}
}

// An address with nothing behind it must fail as "no printer here", not as an
// internal error, and not by hanging.
func TestProbingNothingSaysSo(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	// Port 9 is discard: reserved, and nothing serves print jobs on it.
	resp := c.call("printers.probe", map[string]any{"address": "127.0.0.1:9"})
	if resp.Error == nil {
		t.Fatal("probing a port with nothing behind it succeeded")
	}
	if resp.Error.Code != jsonrpc.CodeUnknownPrinter {
		t.Errorf("code = %d, want unknown printer", resp.Error.Code)
	}
}

// Something listening on a print port that refuses to describe itself is still
// usable: the user picks a driver by hand. Refusing it outright would rule out
// every old printer, which is the hardware this project exists for.
func TestAnUnidentifiedListenerIsStillUsable(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	// The CUPS container: something answers IPP there, but it is a print server
	// and will not describe itself as a printer.
	resp := c.call("printers.probe", map[string]any{"address": "127.0.0.1:6631"})
	if resp.Error != nil {
		t.Fatalf("probe failed where something is definitely listening: %v", resp.Error)
	}

	var result struct {
		DeviceURI    string `json:"device_uri"`
		MakeAndModel string `json:"make_and_model"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.DeviceURI == "" {
		t.Error("no device uri returned for something that is listening")
	}
	t.Logf("uri=%s model=%q", result.DeviceURI, result.MakeAndModel)
}

func TestProbeRejectsNonsense(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	for _, address := range []string{"", "   ", "127.0.0.1:notaport", "127.0.0.1:99999"} {
		resp := c.call("printers.probe", map[string]any{"address": address})
		if resp.Error == nil {
			t.Errorf("probing %q succeeded", address)
			continue
		}
		if resp.Error.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("probing %q gave code %d, want invalid params", address, resp.Error.Code)
		}
	}
}

func TestProbeNeedsThePrintersReadScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})

	if resp := c.call("printers.probe", map[string]any{"address": "127.0.0.1:8632"}); resp.Error == nil {
		t.Fatal("a connector without printers.read probed an address")
	}
}
