package ipp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/OpenPrinting/goipp"
)

// Identity is what a printer says about itself when asked directly.
//
// Deliberately not a [Printer], which represents a queue configured in CUPS.
// This is hardware on a network that has not been added to anything yet, and
// conflating the two would mean a struct where half the fields are meaningless
// depending on where it came from.
type Identity struct {
	MakeAndModel string
	Name         string
	Info         string
	Location     string

	// DeviceID is the IEEE 1284 string, when the printer reports one. It is the
	// input automatic driver selection needs, so getting it from nothing but a
	// typed address is worth the extra request.
	DeviceID string

	State PrinterState
}

// Identify asks a printer directly what it is.
//
// Used when somebody types an address rather than picking a discovered device.
// A printer that speaks IPP will name its own make and model, and often its
// IEEE 1284 device id, which is what makes automatic driver selection possible
// from an address alone.
//
// Two resource paths are tried: /ipp/print, which IPP Everywhere standardised
// and most printers made this decade answer on, then / for the ones that do not.
func Identify(ctx context.Context, address string, port int) (Identity, error) {
	hostPort := net.JoinHostPort(address, strconv.Itoa(port))

	client, err := New("http://" + hostPort)
	if err != nil {
		return Identity{}, err
	}

	var lastErr error
	for _, resource := range []string{"/ipp/print", "/"} {
		req := client.NewRequest(goipp.OpGetPrinterAttributes)
		req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
			goipp.String("ipp://"+hostPort+resource)))
		req.Operation.Add(requestedAttributes(
			"printer-make-and-model", "printer-name", "printer-info",
			"printer-location", "printer-device-id", "printer-state"))

		resp, err := client.Do(ctx, resource, req, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if err := check(goipp.OpGetPrinterAttributes, resp); err != nil {
			lastErr = err
			continue
		}

		for _, g := range resp.Groups {
			if g.Tag != goipp.TagPrinterGroup {
				continue
			}

			state, _ := integer(g.Attrs, "printer-state")
			id := Identity{
				MakeAndModel: str(g.Attrs, "printer-make-and-model"),
				Name:         str(g.Attrs, "printer-name"),
				Info:         str(g.Attrs, "printer-info"),
				Location:     str(g.Attrs, "printer-location"),
				DeviceID:     str(g.Attrs, "printer-device-id"),
				State:        PrinterState(state),
			}
			if id.MakeAndModel != "" || id.Name != "" {
				return id, nil
			}
		}
		lastErr = fmt.Errorf("ipp: %s answered but described no printer", hostPort)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("ipp: %s did not identify itself", hostPort)
	}
	return Identity{}, lastErr
}

// Reachable reports whether something is listening at address and port.
//
// A plain TCP connect, sending nothing. The point is only to find which of a
// printer's possible ports is open before deciding how to talk to it.
func Reachable(ctx context.Context, address string, port int, timeout time.Duration) bool {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
