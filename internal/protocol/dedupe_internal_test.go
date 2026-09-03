package protocol

import (
	"testing"

	"github.com/mhd64real/printer-cycle/internal/ipp"
)

// A modern printer advertises itself several times over. Passing every
// announcement on would offer somebody three identically named things to pair
// with and nothing to choose between them.
func TestTheSamePrinterIsRecognisedAcrossItsAnnouncements(t *testing.T) {
	// Note the uuid on one of them and not the others. A printer announces
	// itself several times and only some announcements carry it, so keying on
	// the uuid where present would split this one printer into two groups.
	// Found by looking at the screen rather than by a test.
	same := []string{
		"dnssd://Virtual%20Office%20Printer._ipp._tcp.local/?uuid=e2e12bda-1206-3b67-4a58-0c73db553bae",
		"dnssd://Virtual%20Office%20Printer._printer._tcp.local/",
		"ipps://Virtual%20Office%20Printer._ipps._tcp.local/",
	}

	first := identityOf(ipp.Device{URI: same[0]})
	for _, uri := range same[1:] {
		if got := identityOf(ipp.Device{URI: uri}); got != first {
			t.Errorf("%s identified as %q, want %q", uri, got, first)
		}
	}

	// A uuid identifies a device that announces no service name, such as a
	// printer reached directly over IPP.
	withUUID := identityOf(ipp.Device{URI: "ipp://192.168.1.50/ipp/print?uuid=abc-123"})
	if withUUID != "uuid:abc-123" {
		t.Errorf("identity = %q, want the uuid to be used", withUUID)
	}
}

// Two genuinely different printers must stay separate. Showing one printer twice
// is untidy; hiding one of two is worse.
func TestDifferentPrintersStayApart(t *testing.T) {
	distinct := []string{
		"dnssd://Office%20Laser._ipp._tcp.local/",
		"dnssd://Kitchen%20Printer._ipp._tcp.local/",
		"usb://HP/LaserJet%201018?serial=KP123",
		"usb://HP/LaserJet%201018?serial=ZZ999",
		"socket://192.168.1.50:9100",
		"socket://192.168.1.51:9100",
	}

	seen := map[string]string{}
	for _, uri := range distinct {
		id := identityOf(ipp.Device{URI: uri})
		if other, clash := seen[id]; clash {
			t.Errorf("%s and %s were treated as the same printer", uri, other)
		}
		seen[id] = uri
	}
}

// When the same printer is announced twice, the announcement that carries a
// device id wins: it is the one that makes automatic driver selection possible.
func TestTheMoreUsefulAnnouncementWins(t *testing.T) {
	withID := deviceView{DeviceURI: "a", DeviceID: "MFG:HP;MDL:LaserJet 1018;", MakeAndModel: ""}
	withModel := deviceView{DeviceURI: "b", MakeAndModel: "HP LaserJet 1018"}
	bare := deviceView{DeviceURI: "c"}

	if !better(withID, withModel) {
		t.Error("an announcement with a device id lost to one with only a model")
	}
	if !better(withModel, bare) {
		t.Error("an announcement naming a model lost to one naming nothing")
	}
	if better(bare, withID) {
		t.Error("an announcement with nothing beat one with a device id")
	}
	if better(withModel, withModel) {
		t.Error("an announcement beat an equally good one, which would make the result depend on arrival order")
	}
}

// Where one printer is announced several ways, the one CUPS copes with best
// wins. A mild preference: dnssd is the form CUPS produces and resolves itself,
// so it is the least that can go wrong.
func TestTheTransportCUPSCopesWithWins(t *testing.T) {
	dnssd := deviceView{DeviceURI: "dnssd://P._ipp._tcp.local/", Transport: "dnssd"}
	ipps := deviceView{DeviceURI: "ipps://P._ipps._tcp.local/", Transport: "ipps"}
	usb := deviceView{DeviceURI: "usb://HP/X", Transport: "usb"}

	if !better(dnssd, ipps) {
		t.Error("the ipps announcement won over the form CUPS resolves itself")
	}
	if !better(usb, dnssd) {
		t.Error("a printer plugged in lost to one on the network")
	}

	// A device id still outranks all of it: without one there is no automatic
	// driver selection, which is the point of the feature.
	withID := deviceView{DeviceURI: "ipps://P/", Transport: "ipps", DeviceID: "MFG:HP;MDL:X;"}
	if !better(withID, dnssd) {
		t.Error("an announcement carrying a device id lost on transport alone")
	}
}
