package protocol

import (
	"context"
	"encoding/json"
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
	err := c.server.cups.DiscoverDevices(ctx, timeout, func(d ipp.Device) {
		view := viewOf(d)
		devices = append(devices, view)

		// Sent as it is found. A failed notification is not worth abandoning
		// the discovery over: the complete set still comes back in the reply.
		if err := c.rpc.Notify(ctx, "printer.discovered", view); err != nil {
			c.log.Debug("cannot deliver a discovery notification", "error", err)
		}
	})
	if err != nil {
		return nil, c.translateIPP(err, subjectPrinter)
	}

	return map[string]any{"devices": devices}, nil
}
