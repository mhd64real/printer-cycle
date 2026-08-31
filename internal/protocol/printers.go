package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// candidateView is one driver a device could use.
type candidateView struct {
	PPD          string `json:"ppd"`
	MakeAndModel string `json:"make_and_model"`

	// Recommended is CUPS's own hint, which foomatic PPDs carry inside their
	// model string. Discovered by measurement: the design session had concluded
	// CUPS offers no ranking signal at all.
	Recommended bool `json:"recommended"`

	// RequiresProprietaryPlugin marks a driver depending on a closed vendor
	// binary. Those are x86 only and will never run on an ARM board, so on a
	// Raspberry Pi such a driver must never be the automatic choice. Distinct
	// from needing firmware, which is a wait rather than a wall.
	RequiresProprietaryPlugin bool `json:"requires_proprietary_plugin"`
}

type driverCandidatesParams struct {
	DeviceID string `json:"device_id"`
}

// printersDriverCandidates lists the drivers that claim a device.
//
// CUPS narrows for free: PPDs carry a 1284DeviceID and CUPS-Get-PPDs filters on
// it. Narrowing is not selecting, though. Measured against a full installation,
// a LaserJet 4 device id returns 33 candidates including a Color LaserJet 4730
// MFP, which is a different printer.
//
// Ordering here is still CUPS's own, with the two signals below merely reported.
// Real ranking, an override list for matches that print garbage, and a cache
// arrive in Stage 54; a filtered query costs two to five seconds because CUPS
// scans the whole catalogue regardless of the filter.
func (c *conn) printersDriverCandidates(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p driverCandidatesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.DeviceID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no device id given")
	}

	candidates, err := c.driverCandidates(ctx, p.DeviceID)
	if err != nil {
		return nil, c.translateIPP(err, subjectPrinter)
	}
	return map[string]any{"candidates": candidates}, nil
}

func (c *conn) driverCandidates(ctx context.Context, deviceID string) ([]candidateView, error) {
	ppds, err := c.server.cups.PPDs(ctx, ipp.PPDFilter{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}

	out := make([]candidateView, 0, len(ppds))
	for _, p := range ppds {
		lower := strings.ToLower(p.MakeAndModel)
		out = append(out, candidateView{
			PPD:                       p.Name,
			MakeAndModel:              p.MakeAndModel,
			Recommended:               strings.Contains(lower, "(recommended)"),
			RequiresProprietaryPlugin: strings.Contains(lower, "proprietary plugin"),
		})
	}
	return out, nil
}

// chooseDriver picks the driver to use when the caller did not name one.
//
// Interim, and deliberately simple: prefer a driver CUPS marks recommended,
// never choose one needing a closed vendor binary while an open alternative
// exists, and otherwise take the first. Stage 54 replaces this with a real
// ranking.
func chooseDriver(candidates []candidateView) (string, bool) {
	var fallback string
	for _, c := range candidates {
		if c.RequiresProprietaryPlugin {
			continue
		}
		if c.Recommended {
			return c.PPD, true
		}
		if fallback == "" {
			fallback = c.PPD
		}
	}
	if fallback != "" {
		return fallback, true
	}
	// Everything left needs a proprietary plugin. Better to offer it than to
	// pretend the printer is unsupported, but it is not chosen silently.
	if len(candidates) > 0 {
		return candidates[0].PPD, false
	}
	return "", false
}

type addPrinterParams struct {
	DeviceURI string  `json:"device_uri"`
	Name      string  `json:"name"`
	PPD       *string `json:"ppd"`
	DeviceID  string  `json:"device_id"`
	Location  string  `json:"location"`
}

type printerView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	QueueName  string `json:"queue_name"`
	DeviceURI  string `json:"device_uri"`
	PPD        string `json:"ppd"`
	Location   string `json:"location"`
	Restricted bool   `json:"restricted"`
	AutoChosen bool   `json:"driver_chosen_automatically"`
}

// printersAdd turns a device into a working queue.
//
// The one-click pair button. A null ppd means "choose one", which is the whole
// point: the alternative is handing somebody a list of eighteen thousand drivers
// and asking them to recognise their own printer in it.
func (c *conn) printersAdd(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p addPrinterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.DeviceURI) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no device uri given")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no name given")
	}

	ppd := ""
	autoChosen := false
	switch {
	case p.PPD != nil:
		ppd = *p.PPD
	case p.DeviceID != "":
		candidates, err := c.driverCandidates(ctx, p.DeviceID)
		if err != nil {
			return nil, c.translateIPP(err, subjectPrinter)
		}
		chosen, clean := chooseDriver(candidates)
		if chosen == "" {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams,
				"no driver claims this printer, so one has to be chosen by hand")
		}
		ppd = chosen
		autoChosen = clean
	default:
		// No driver and nothing to choose from. A driverless IPP printer needs
		// none, so this is not an error: CUPS is told to work it out.
		ppd = "everywhere"
	}

	// The database row first, which reserves the queue name. A failure here
	// leaves nothing behind; the other order would leave a queue in CUPS that
	// printer-cycle does not know about.
	record, err := c.db.CreatePrinter(ctx, store.PrinterSpec{
		DisplayName: name,
		DeviceURI:   p.DeviceURI,
		PPDName:     ppd,
		Location:    p.Location,
	})
	if err != nil {
		return nil, err
	}

	err = c.server.cups.AddPrinter(ctx, ipp.PrinterSpec{
		Name:      record.QueueName,
		DeviceURI: record.DeviceURI,
		PPDName:   record.PPDName,
		Info:      record.DisplayName,
		Location:  record.Location,

		// Sharing is derived, not configured.
		//
		// Unshared is what we want: printer-cycle publishes printers through
		// connectors, and letting CUPS advertise them too would put one
		// physical printer on the network twice, under two identities, from the
		// same box.
		//
		// But CUPS refuses print jobs from a remote client to an unshared
		// queue, so a core reaching CUPS across a network must share or nothing
		// it creates can ever be printed to. Core knows which situation it is
		// in, so it decides rather than asking an operator to know this.
		Shared: !c.server.cups.Local(),
	})
	if err != nil {
		// Undo the reservation so a failed attempt does not consume the name the
		// user asked for.
		if rollback := c.db.DeletePrinter(ctx, record.ID); rollback != nil {
			c.log.Error("cannot undo a printer record after CUPS refused it",
				"printer", record.ID, "error", rollback)
		}
		return nil, c.translateIPP(err, subjectPrinter)
	}

	c.log.Info("printer added", "queue", record.QueueName, "driver", ppd, "automatic", autoChosen)

	return viewOfPrinter(record, autoChosen), nil
}

type removePrinterParams struct {
	ID string `json:"id"`
}

// printersRemove deletes a queue and printer-cycle's record of it.
func (c *conn) printersRemove(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p removePrinterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	record, err := c.db.Printer(ctx, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownPrinter, "no such printer")
	}
	if err != nil {
		return nil, err
	}

	// CUPS first. A queue already gone is a success, not a failure: the
	// intended state is that it does not exist.
	if err := c.server.cups.DeletePrinter(ctx, record.QueueName); err != nil &&
		!errors.Is(err, ipp.ErrNotFound) {
		return nil, c.translateIPP(err, subjectPrinter)
	}

	if err := c.db.DeletePrinter(ctx, record.ID); err != nil {
		return nil, err
	}

	c.log.Info("printer removed", "queue", record.QueueName)
	return map[string]any{"removed": record.ID}, nil
}

// printersList returns the printers on this box.
func (c *conn) printersList(ctx context.Context) (any, error) {
	records, err := c.db.Printers(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]printerView, 0, len(records))
	for _, r := range records {
		out = append(out, viewOfPrinter(r, false))
	}
	return map[string]any{"printers": out}, nil
}

func viewOfPrinter(p store.Printer, autoChosen bool) printerView {
	return printerView{
		ID:         p.ID,
		Name:       p.DisplayName,
		QueueName:  p.QueueName,
		DeviceURI:  p.DeviceURI,
		PPD:        p.PPDName,
		Location:   p.Location,
		Restricted: p.Restricted,
		AutoChosen: autoChosen,
	}
}
