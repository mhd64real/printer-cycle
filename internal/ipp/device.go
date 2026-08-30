package ipp

import (
	"context"
	"strings"
	"time"

	"github.com/OpenPrinting/goipp"
)

// Device is something a CUPS backend found that could be turned into a queue.
type Device struct {
	// URI is what gets passed back to add this device as a printer.
	URI string

	// ID is the IEEE 1284 device id the hardware reported, the string that makes
	// automatic driver selection possible. It is often empty, and empty is not a
	// fault: driverless IPP Everywhere printers have no need of one, and some
	// backends simply do not ask.
	ID string

	// MakeAndModel is empty when the backend could not identify the hardware. See
	// parseDevice for why it is empty rather than the word CUPS actually sends.
	MakeAndModel string

	Info     string
	Location string

	// Class is the CUPS device-class: network, direct, file, or serial.
	Class string

	// Transport is the URI scheme, which is how printer-cycle will reach this
	// device: usb, dnssd, ipp, ipps, socket, lpd.
	//
	// Note it says how to TALK to the device, not how it was FOUND. There is no
	// snmp:// scheme; a printer the SNMP backend discovered comes back as socket
	// or lpd, because that is how you print to it.
	Transport string
}

// Devices asks every CUPS backend what it can find.
//
// This is slow by nature. The SNMP backend broadcasts across the subnet and has
// to wait out its own timeout, so a call takes seconds rather than
// milliseconds. Nothing user-facing should block on it; Stage 14 delivers
// results progressively instead.
//
// A zero timeout lets CUPS use its own default.
func (c *Client) Devices(ctx context.Context, timeout time.Duration) ([]Device, error) {
	req := c.NewRequest(goipp.OpCupsGetDevices)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))

	if timeout > 0 {
		secs := int32(timeout / time.Second)
		if secs < 1 {
			secs = 1
		}
		req.Operation.Add(goipp.MakeAttribute("timeout", goipp.TagInteger, goipp.Integer(secs)))
	}

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return nil, err
	}
	if err := check(goipp.OpCupsGetDevices, resp); err != nil {
		return nil, err
	}

	var devices []Device
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagPrinterGroup {
			continue
		}
		if d, ok := parseDevice(g.Attrs); ok {
			devices = append(devices, d)
		}
	}
	return devices, nil
}

func parseDevice(attrs goipp.Attributes) (Device, bool) {
	uri := str(attrs, "device-uri")
	if !pairable(uri) {
		return Device{}, false
	}

	model := str(attrs, "device-make-and-model")
	// CUPS sends the literal string "Unknown" when a backend cannot identify the
	// hardware. Passing that through would put the word "Unknown" in a column
	// headed Model, as though the printer were made by Unknown. Absent is the
	// honest representation, and it lets the dashboard decide how to say so.
	if strings.EqualFold(model, "unknown") {
		model = ""
	}

	scheme, _, _ := strings.Cut(uri, ":")

	return Device{
		URI:          uri,
		ID:           str(attrs, "device-id"),
		MakeAndModel: model,
		Info:         str(attrs, "device-info"),
		Location:     str(attrs, "device-location"),
		Class:        str(attrs, "device-class"),
		Transport:    scheme,
	}, true
}

// pairable separates real devices from the pseudo-devices CUPS mixes in with
// them.
//
// Every backend advertises itself as a device whose uri is the bare scheme:
// "ipp", "lpd", "socket", "beh". Those are not hardware anyone can pair with,
// they are an offer to accept a URI of that shape. They arrive carrying
// class=network, identical to a real network printer, so class cannot tell them
// apart and the URI is the only thing that can. A real device URI has a host, or
// a path with something in it.
//
// Deliberately not using net/url: it rejects percent-encoding in the host, and
// CUPS puts the DNS-SD service name in the host position, so a printer called
// "Virtual Office Printer" arrives as dnssd://Virtual%20Office%20Printer...
// Parsing that with url.Parse silently yields an empty host, which would have
// dropped every printer whose name contains a space. That is most printers.
func pairable(uri string) bool {
	scheme, rest, ok := strings.Cut(uri, ":")
	if !ok || scheme == "" {
		return false
	}
	return strings.Trim(rest, "/") != ""
}
