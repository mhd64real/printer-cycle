package protocol_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/store"
)

// TestDiscoveryArrivesProgressively is the point of the stage: a connector must
// learn about devices as they are found, not in one batch at the end.
func TestDiscoveryArrivesProgressively(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	type arrival struct {
		identity string
		uri      string
		at       time.Duration
	}

	start := time.Now()

	// Send the request, then read frames until the reply to it arrives.
	// Everything before that is a notification.
	c.next++
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": c.next, "method": "printers.discover",
		"params": map[string]any{"timeout_ms": 10000},
	})
	if err := c.ws.Write(c.ctx, websocket.MessageText, req); err != nil {
		t.Fatal(err)
	}

	var notified []arrival
	var reply struct {
		ID     int `json:"id"`
		Result struct {
			Devices []map[string]any `json:"devices"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	for {
		_, raw, err := c.ws.Read(c.ctx)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}

		var probe struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     *int            `json:"id"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("frame is not JSON: %s", raw)
		}

		if probe.Method == "printer.discovered" {
			var d struct {
				Identity  string `json:"identity"`
				DeviceURI string `json:"device_uri"`
				Transport string `json:"transport"`
			}
			if err := json.Unmarshal(probe.Params, &d); err != nil {
				t.Fatal(err)
			}
			if d.DeviceURI == "" {
				t.Error("a discovery notification carried no device uri")
			}
			if d.Identity == "" {
				t.Error("a discovery notification carried no identity, so a page " +
					"has nothing to key on but the uri, which is what changes")
			}
			notified = append(notified, arrival{d.Identity, d.DeviceURI, time.Since(start)})
			continue
		}

		if probe.ID != nil && *probe.ID == c.next {
			if err := json.Unmarshal(raw, &reply); err != nil {
				t.Fatal(err)
			}
			break
		}
	}

	if reply.Error != nil {
		t.Fatalf("discovery failed: %d %s", reply.Error.Code, reply.Error.Message)
	}

	for _, a := range notified {
		t.Logf("t=%-8v %s", a.at.Round(10*time.Millisecond), a.uri)
	}

	if len(notified) == 0 {
		t.Fatal("no printer.discovered notifications arrived. Is the virtual printer container up?")
	}

	// Compared by identity rather than by count, because one printer is
	// legitimately announced more than once: a better description of it
	// replaces the first. What must hold is that the announcements and the
	// reply describe the same set of printers, so a page that applied every
	// announcement ends up with exactly what the reply says.
	announced := make(map[string]bool, len(notified))
	for _, a := range notified {
		announced[a.identity] = true
	}
	listed := make(map[string]bool, len(reply.Result.Devices))
	for _, d := range reply.Result.Devices {
		identity, _ := d["identity"].(string)
		if identity == "" {
			t.Errorf("a listed device carried no identity: %v", d)
		}
		listed[identity] = true
	}
	if len(announced) != len(listed) {
		t.Errorf("the reply lists %d printers but %d distinct ones were announced; "+
			"the two must agree", len(listed), len(announced))
	}
	for identity := range announced {
		if !listed[identity] {
			t.Errorf("printer %q was announced but is missing from the reply, so a page "+
				"would show it and then lose it", identity)
		}
	}

	// No assertion about the spread between announcements, deliberately.
	//
	// It depends on how quickly CUPS happens to find things, and two devices
	// found at nearly the same moment are announced at nearly the same moment,
	// which is right behaviour failing an assertion about it. That progressive
	// delivery happens at all is proven deterministically by
	// TestDiscoveryEmitsBeforeTheResponseEnds in the ipp package, against a
	// server that pauses mid-stream on purpose. What matters here is that every
	// device found is announced, and that the announcements and the reply agree.
}

func TestDiscoveryNeedsThePrintersReadScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsSubmit})

	resp := c.call("printers.discover", map[string]any{"timeout_ms": 1000})
	if resp.Error == nil {
		t.Fatal("a connector without printers.read discovered devices")
	}
}

// Without a printing system, discovery has to fail cleanly rather than panic.
func TestDiscoveryWithoutCUPSFailsCleanly(t *testing.T) {
	url, db := testServer(t)
	c := authedClient(t, url, db, "dashboard", []string{store.ScopePrintersRead})

	resp := c.call("printers.discover", map[string]any{"timeout_ms": 1000})
	if resp.Error == nil {
		t.Fatal("discovery succeeded with no printing system configured")
	}
}

func skipWithoutCUPS(t *testing.T) string {
	t.Helper()
	endpoint := os.Getenv("PRINTER_CYCLE_TEST_CUPS")
	if endpoint == "" {
		t.Skip("set PRINTER_CYCLE_TEST_CUPS to run this: make dev-up, then make test-integration")
	}
	return endpoint
}
