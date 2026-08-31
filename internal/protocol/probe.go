package protocol

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
)

// probeCandidate is one way a printer might be listening.
//
// Ordered by preference rather than by port number. IPP is tried first because
// a printer that speaks it can say what it is, which is what turns a typed
// address into a one-click pairing rather than a driver list to scroll through.
// The other two work but say nothing about themselves.
var probeCandidates = []struct {
	Port   int
	Scheme string
	Name   string
}{
	{631, "ipp", "IPP"},
	{9100, "socket", "JetDirect"},
	{515, "lpd", "LPD"},
}

const probeTimeout = 2 * time.Second

type probeParams struct {
	Address string `json:"address"`
}

type probeResult struct {
	DeviceURI    string `json:"device_uri"`
	MakeAndModel string `json:"make_and_model"`
	DeviceID     string `json:"device_id"`
	Info         string `json:"info"`
	Location     string `json:"location"`
	Transport    string `json:"transport"`
	Port         int    `json:"port"`
}

// printersProbe works out how to reach a printer from an address somebody typed.
//
// The manual path, for hardware discovery does not find: a printer on another
// subnet, one with mDNS switched off, or a network where broadcast traffic is
// filtered. All the user should need to know is the address.
//
// An address with a port probes only that port. Without one, the three ports a
// network printer might answer on are tried at once rather than in turn, so an
// address with nothing behind it costs one timeout instead of three.
func (c *conn) printersProbe(ctx context.Context, params json.RawMessage) (any, error) {
	var p probeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	address := strings.TrimSpace(p.Address)
	if address == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no address given")
	}

	candidates := probeCandidates
	if host, portText, err := net.SplitHostPort(address); err == nil {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%q is not a valid port", portText)
		}
		address = host
		// A port was named, so honour it. Assume IPP, since anything else needs
		// the user to say what it is anyway.
		candidates = []struct {
			Port   int
			Scheme string
			Name   string
		}{{port, "ipp", "IPP"}}
	}

	reachable := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, cand := range candidates {
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			reachable[i] = ipp.Reachable(ctx, address, port, probeTimeout)
		}(i, cand.Port)
	}
	wg.Wait()

	for i, cand := range candidates {
		if !reachable[i] {
			continue
		}

		result := probeResult{
			DeviceURI: cand.Scheme + "://" + net.JoinHostPort(address, strconv.Itoa(cand.Port)),
			Transport: cand.Scheme,
			Port:      cand.Port,
		}

		if cand.Scheme == "ipp" {
			if id, err := ipp.Identify(ctx, address, cand.Port); err == nil {
				result.MakeAndModel = id.MakeAndModel
				result.DeviceID = id.DeviceID
				result.Info = id.Info
				result.Location = id.Location
				// IPP Everywhere printers answer here. The resource path matters
				// to CUPS, so it is kept rather than dropped.
				result.DeviceURI += "/ipp/print"
			} else {
				// Something is listening on the IPP port but will not describe
				// itself. Still usable: the user picks a driver by hand.
				c.log.Debug("a printer answered on the IPP port but did not identify itself",
					"address", address, "error", err)
			}
		}

		c.log.Info("probed an address", "address", address,
			"found", cand.Name, "model", result.MakeAndModel)
		return result, nil
	}

	return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownPrinter,
		"nothing is listening for print jobs at %s", address)
}
