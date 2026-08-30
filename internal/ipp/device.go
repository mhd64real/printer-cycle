package ipp

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// endOfAttributes terminates the attribute section of an IPP message.
const endOfAttributes = 0x03

func (c *Client) devicesRequest(timeout time.Duration) *goipp.Message {
	req := c.NewRequest(goipp.OpCupsGetDevices)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))

	if timeout > 0 {
		secs := int32(timeout / time.Second)
		if secs < 1 {
			secs = 1
		}
		req.Operation.Add(goipp.MakeAttribute("timeout", goipp.TagInteger, goipp.Integer(secs)))
	}
	return req
}

// DiscoverDevices asks every CUPS backend what it can find, calling fn once per
// device, as each one is found.
//
// Discovery is slow by nature: the SNMP backend broadcasts across the subnet and
// waits out its own timeout, so the whole operation takes seconds. Measured
// against the development environment, cupsd answers the fast backends in 30ms
// and then trickles the rest out over the following two and a half seconds. A
// user interface that waited for the complete list would show a spinner for
// three seconds and then everything at once, which reads as broken.
//
// fn is called from this goroutine, in arrival order, and must not block.
//
// A zero timeout lets CUPS choose its own.
func (c *Client) DiscoverDevices(ctx context.Context, timeout time.Duration, fn func(Device)) error {
	resp, err := c.send(ctx, "/", c.devicesRequest(timeout), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var (
		buf     bytes.Buffer
		emitted int
		chunk   = make([]byte, 4096)
	)

	for {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if msg, ok := decodePrefix(buf.Bytes()); ok {
				// Withhold the last group. A group is only known to be complete
				// once another one begins, and emitting it early would deliver
				// the same device twice with different contents.
				for emitted < len(msg.Groups)-1 {
					emit(msg.Groups[emitted], fn)
					emitted++
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("ipp: reading device stream: %w", readErr)
		}
	}

	final := &goipp.Message{}
	if err := final.DecodeBytes(buf.Bytes()); err != nil {
		return fmt.Errorf("ipp: decoding device stream: %w", err)
	}
	if err := check(goipp.OpCupsGetDevices, final); err != nil {
		return err
	}
	for ; emitted < len(final.Groups); emitted++ {
		emit(final.Groups[emitted], fn)
	}
	return nil
}

// Devices is DiscoverDevices collected into a slice, for callers that genuinely
// want the complete list and can afford to wait for it.
func (c *Client) Devices(ctx context.Context, timeout time.Duration) ([]Device, error) {
	var devices []Device
	err := c.DiscoverDevices(ctx, timeout, func(d Device) {
		devices = append(devices, d)
	})
	if err != nil {
		return nil, err
	}
	return devices, nil
}

// decodePrefix decodes however much of a message has arrived so far.
//
// The trick is in the wire format. An IPP message is a fixed header followed by
// attribute groups and terminated by a single end-of-attributes byte, so any
// prefix of a message plus that byte is itself a valid, shorter message.
// Appending it to whatever has arrived yields every group received so far.
//
// goipp has no incremental decoder, and hand-writing one would mean
// reimplementing IPP attribute parsing, collections included, to gain nothing:
// discovery responses run to a couple of kilobytes, so re-decoding the buffer on
// each read costs nothing measurable.
//
// A read that lands mid-attribute simply fails to decode, and the next read
// fixes it. That is why the failure case is a bool rather than an error.
func decodePrefix(data []byte) (*goipp.Message, bool) {
	// Eight bytes of header, plus at least one delimiter, or there is nothing
	// worth attempting.
	if len(data) < 9 {
		return nil, false
	}

	probe := make([]byte, len(data)+1)
	copy(probe, data)
	probe[len(data)] = endOfAttributes

	msg := &goipp.Message{}
	if err := msg.DecodeBytes(probe); err != nil {
		return nil, false
	}
	return msg, true
}

func emit(g goipp.Group, fn func(Device)) {
	if g.Tag != goipp.TagPrinterGroup {
		return
	}
	if d, ok := parseDevice(g.Attrs); ok {
		fn(d)
	}
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
