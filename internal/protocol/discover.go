package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
)

// Discovery timeouts. The floor exists because anything shorter finds nothing
// but USB; the ceiling because a connector should not be able to hold the
// discovery lock for as long as it likes.
const (
	discoverDefault = 8 * time.Second
	discoverMin     = 1 * time.Second
	discoverMax     = 30 * time.Second
)

type discoverParams struct {
	TimeoutMS int `json:"timeout_ms"`
}

// deviceView is what a connector sees.
//
// Note the absence of a "driverless" flag. CUPS cannot say whether a device
// needs a driver, and guessing from the URI scheme would be a fabrication
// dressed as a fact. printers.driverCandidates answers it from real data.
type deviceView struct {
	// Identity is what makes two announcements the same printer.
	//
	// It exists because discovery announces an update, not only an arrival: a
	// printer first seen over ipps is announced again under dnssd once the
	// better description of it turns up. Without this, the only thing a
	// connector could key on is the device uri, which is the very field that
	// changed, so the update read as a second printer and the page showed one
	// machine twice until the final reply collapsed it.
	//
	// Opaque. Its form is not part of the contract, only its stability within
	// a discovery run.
	Identity     string `json:"identity"`
	DeviceURI    string `json:"device_uri"`
	DeviceID     string `json:"device_id"`
	MakeAndModel string `json:"make_and_model"`
	Info         string `json:"info"`
	Location     string `json:"location"`
	Transport    string `json:"transport"`
}

func viewOf(d ipp.Device) deviceView {
	return deviceView{
		DeviceURI:    d.URI,
		DeviceID:     d.ID,
		MakeAndModel: d.MakeAndModel,
		Info:         d.Info,
		Location:     d.Location,
		Transport:    d.Transport,
	}
}

// printersDiscover asks every CUPS backend what it can find, telling the caller
// about each device as it turns up.
//
// Progressive by design. Measured against a real cupsd, the fast backends answer
// in about 30ms and the rest trickle in over the following two and a half
// seconds, because the SNMP backend has to wait out a subnet broadcast. A screen
// that waited for the complete list would show a spinner for three seconds and
// then everything at once, which reads as broken rather than as thorough.
func (c *conn) printersDiscover(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p discoverParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
		}
	}

	timeout := discoverDefault
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	timeout = min(max(timeout, discoverMin), discoverMax)

	// One discovery at a time across the whole box, so a connector in a loop
	// cannot fill the network with SNMP broadcasts.
	locked := make(chan struct{})
	go func() {
		c.server.discovering.Lock()
		close(locked)
	}()
	select {
	case <-locked:
		defer c.server.discovering.Unlock()
	case <-ctx.Done():
		// The caller gave up waiting for its turn. The goroutine above still
		// acquires the lock, so it releases it immediately rather than leaking.
		go func() {
			<-locked
			c.server.discovering.Unlock()
		}()
		return nil, ctx.Err()
	}

	devices := make([]deviceView, 0, 8)
	seen := make(map[string]int, 8)

	err := c.server.cups.DiscoverDevices(ctx, timeout, func(d ipp.Device) {
		view := viewOf(d)

		// One printer, announced once.
		//
		// A modern printer advertises itself several times over: _ipp._tcp,
		// _ipps._tcp and _printer._tcp are the same hardware described three
		// ways. Passing all of them on would offer somebody three things to
		// pair with, identically named, with nothing to choose between them.
		key := identityOf(d)
		view.Identity = key
		if existing, ok := seen[key]; ok {
			if better(view, devices[existing]) {
				devices[existing] = view
				// Announced again so a page updates rather than showing the
				// worse variant it was told about first.
				c.notifyDiscovered(ctx, view)
			}
			return
		}

		seen[key] = len(devices)
		devices = append(devices, view)
		c.notifyDiscovered(ctx, view)
	})
	if err != nil {
		return nil, c.translateIPP(err, subjectPrinter)
	}

	return map[string]any{"devices": devices}, nil
}

func (c *conn) notifyDiscovered(ctx context.Context, view deviceView) {
	// A failed notification is not worth abandoning discovery over: the
	// complete set still comes back in the reply.
	if err := c.rpc.Notify(ctx, "printer.discovered", view); err != nil {
		c.log.Debug("cannot deliver a discovery notification", "error", err)
	}
}

// identityOf is what makes two announcements the same printer.
//
// The DNS-SD service instance name comes first, and the order matters more than
// it looks. A printer announces itself under several service types, and only
// some of those announcements carry a UUID. Keying on the UUID where present
// therefore splits one printer into two groups: the announcement that mentions
// it, and the ones that do not. The service name is the identifier every
// announcement shares, and mDNS already guarantees it is unique on a network.
//
// The UUID is still used for devices that have no service name, such as a
// printer reached directly over IPP.
//
// Conservative beyond that. Two printers of the same model stay separate:
// showing one printer twice is untidy, and hiding one of two is worse.
func identityOf(d ipp.Device) string {
	if name := serviceName(d.URI); name != "" {
		return "service:" + name
	}
	if uuid := uuidFrom(d.URI); uuid != "" {
		return "uuid:" + uuid
	}
	return "uri:" + d.URI
}

// uuidFrom pulls the uuid a DNS-SD device advertises, when it has one.
func uuidFrom(uri string) string {
	_, query, ok := strings.Cut(uri, "?")
	if !ok {
		return ""
	}
	for _, part := range strings.Split(query, "&") {
		if value, ok := strings.CutPrefix(part, "uuid="); ok {
			return value
		}
	}
	return ""
}

// serviceName is the instance name out of a DNS-SD URI, which is the part
// before the service type: "Virtual%20Office%20Printer" out of
// "dnssd://Virtual%20Office%20Printer._ipp._tcp.local/".
func serviceName(uri string) string {
	_, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(rest, "._")
	if !ok {
		return ""
	}
	return name
}

// better reports whether a is the more useful description of a printer.
//
// Three things, in order.
//
// A device id first, because it is what makes automatic driver selection
// possible at all, and an announcement carrying one is worth more than one that
// does not whatever else it says. Then a stated model over none.
//
// Then the transport, as a mild preference rather than a fix for anything. CUPS
// has to reach a printer to work out its capabilities, and dnssd is the form it
// produces and resolves itself, so it is the least that can go wrong.
//
// Recorded honestly because the first version of this comment claimed more: a
// pairing failure was blamed on CUPS refusing a self-signed certificate over
// ipps, and the actual cause turned out to be that the container could discover
// .local names without being able to resolve them. Pairing over ipps works fine
// once it can. The ordering stays because it is still the safer default; the
// explanation for it does not.
func better(a, b deviceView) bool {
	if (a.DeviceID != "") != (b.DeviceID != "") {
		return a.DeviceID != ""
	}
	if (a.MakeAndModel != "") != (b.MakeAndModel != "") {
		return a.MakeAndModel != ""
	}
	return transportRank(a.Transport) > transportRank(b.Transport)
}

// transportRank orders the ways of reaching one printer by how well CUPS copes
// with each.
func transportRank(transport string) int {
	switch transport {
	case "usb":
		// Plugged in. Nothing to resolve and nothing to fail.
		return 5
	case "dnssd":
		// The form CUPS produces itself and resolves at print time.
		return 4
	case "ipp":
		return 3
	case "ipps":
		// Works when a certificate is trusted, which on a home network it
		// generally is not.
		return 2
	case "socket", "lpd":
		return 1
	default:
		return 0
	}
}
