package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/driver"
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

	// Score and Why are the ranking, shown rather than hidden. CUPS matches
	// loosely enough that a list for a LaserJet 4 contains drivers for a Color
	// LaserJet 4610, so somebody choosing by hand needs to see which of them is
	// actually written for their printer, and somebody trusting the automatic
	// choice deserves to be able to check it.
	Score int      `json:"score"`
	Why   []string `json:"why,omitempty"`

	// DeviceID is the driver's own claim about what it drives. Worth showing
	// next to a printer's own: when CUPS offers a driver for a Color LaserJet
	// 4610 to somebody with a LaserJet 4, this is the field that says so.
	DeviceID string `json:"driver_device_id,omitempty"`
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

// driversParams asks for part of the driver catalogue.
type driversParams struct {
	// Make narrows to one manufacturer. Empty with no query asks for the list
	// of manufacturers instead of drivers, because the catalogue is far too
	// large to hand over whole.
	Make string `json:"make"`

	// Query is matched against make-and-model, case insensitively, anywhere in
	// the string.
	Query string `json:"query"`

	Limit int `json:"limit"`
}

// driverLimit bounds a driver search.
//
// Measured, not guessed: a full driver installation is close to eighteen
// thousand PPDs, and one manufacturer alone can be three thousand of them.
// Sending that to a page would be a multi-megabyte response built and decoded
// on a machine with 512MB of RAM, to render a list nobody can read.
const (
	driverLimitDefault = 200
	driverLimitMax     = 1000
)

// printersDrivers browses the driver catalogue, for a printer whose model could
// not be worked out automatically.
//
// Two shapes, because picking from eighteen thousand needs narrowing before it
// needs listing: with no make and no query it answers with manufacturers, and
// with either it answers with drivers.
func (c *conn) printersDrivers(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p driversParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
		}
	}

	// Named manufacturer rather than make, because make is a builtin and
	// shadowing it here breaks the slice allocation further down.
	manufacturer := strings.TrimSpace(p.Make)
	query := strings.TrimSpace(p.Query)

	if manufacturer == "" && query == "" {
		makes, err := c.server.cups.PPDMakes(ctx)
		if err != nil {
			return nil, c.translateIPP(err, subjectPrinter)
		}
		return map[string]any{"makes": makes}, nil
	}

	limit := p.Limit
	if limit <= 0 {
		limit = driverLimitDefault
	}
	limit = min(limit, driverLimitMax)

	// Which filter goes to CUPS matters, and not for the reason it looks.
	//
	// CUPS honours ppd-make or ppd-make-and-model, not both: sending both
	// returns everything by that manufacturer, with the model filter silently
	// ignored. Asking for make=HP and model="LaserJet 4" gives all 2904 HP
	// drivers rather than the 147 that match.
	//
	// So the more selective one is sent and the other applied here. A query is
	// almost always the narrower of the two, which also keeps the response
	// small, which is the point.
	filter := ipp.PPDFilter{Make: manufacturer}
	if query != "" {
		filter = ipp.PPDFilter{MakeAndModel: query}
	}

	ppds, err := c.server.cups.PPDs(ctx, filter)
	if err != nil {
		return nil, c.translateIPP(err, subjectPrinter)
	}

	out := make([]candidateView, 0, min(len(ppds), limit))
	truncated := false
	for _, ppd := range ppds {
		if query != "" && manufacturer != "" && !strings.EqualFold(ppd.Make, manufacturer) {
			continue
		}
		if len(out) == limit {
			truncated = true
			break
		}
		lower := strings.ToLower(ppd.MakeAndModel)
		out = append(out, candidateView{
			PPD:                       ppd.Name,
			MakeAndModel:              ppd.MakeAndModel,
			Recommended:               strings.Contains(lower, "(recommended)"),
			RequiresProprietaryPlugin: strings.Contains(lower, "proprietary plugin"),
		})
	}

	// Said out loud rather than left to look like the whole answer. A list
	// silently cut at 200 reads as "these are all of them", and somebody whose
	// printer is number 201 concludes it is not supported.
	return map[string]any{"drivers": out, "truncated": truncated}, nil
}

// driverCandidates asks what could drive a printer, best first.
//
// Ranked and cached by the driver package. The cache is not an optimisation
// somebody thought would be nice: a filtered PPD query against a real cupsd
// with a full driver installation takes between 2.7 and 5.2 seconds, every
// time, because CUPS does not cache it either.
func (c *conn) driverCandidates(ctx context.Context, deviceID string) ([]candidateView, error) {
	ranked, err := c.server.drivers.Candidates(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	out := make([]candidateView, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, candidateView{
			PPD:                       r.PPD,
			MakeAndModel:              r.MakeAndModel,
			Recommended:               r.Recommended,
			RequiresProprietaryPlugin: r.RequiresProprietaryPlugin,
			Score:                     r.Score,
			Why:                       r.Why,
			DeviceID:                  r.DeviceID,
		})
	}
	return out, nil
}

// lookupPPDs is the driver package's window onto CUPS.
func (s *Server) lookupPPDs(ctx context.Context, deviceID string) ([]driver.Candidate, error) {
	if s.cups == nil {
		return nil, errors.New("protocol: no printing system")
	}
	ppds, err := s.cups.PPDs(ctx, ipp.PPDFilter{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}

	out := make([]driver.Candidate, 0, len(ppds))
	for _, p := range ppds {
		// The two hints CUPS gives, both of them inside the model string
		// because there is nowhere else for them to be. Found by measurement at
		// Stage 15, against a design session that had concluded CUPS offers no
		// ranking signal at all.
		lower := strings.ToLower(p.MakeAndModel)
		out = append(out, driver.Candidate{
			PPD:                       p.Name,
			MakeAndModel:              p.MakeAndModel,
			DeviceID:                  p.DeviceID,
			Recommended:               strings.Contains(lower, "(recommended)"),
			RequiresProprietaryPlugin: strings.Contains(lower, "proprietary plugin"),
		})
	}
	return out, nil
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
		chosen, safe, err := c.server.drivers.Best(ctx, p.DeviceID)
		if err != nil {
			return nil, c.translateIPP(err, subjectPrinter)
		}
		if chosen.PPD == "" {
			return nil, jsonrpc.Errorf(jsonrpc.CodeDriverRequired,
				"no driver claims this printer, so one has to be chosen by hand")
		}
		if !safe {
			// Offered, not applied. Either it needs a closed vendor binary that
			// will not run here, or it is written for a different model and
			// CUPS matched on a substring. Both are worth naming; neither is
			// worth applying behind somebody's back.
			return nil, jsonrpc.Errorf(jsonrpc.CodeDriverRequired,
				"no driver is certain enough for this printer, so one has to be chosen by hand")
		}
		ppd = chosen.PPD
		autoChosen = true
	case canDeriveDriver(p.DeviceURI):
		// A driverless IPP printer needs no driver, so a device that says
		// nothing about itself is not an error: CUPS is told to ask it.
		ppd = "everywhere"
	default:
		// Anything else, refused here rather than attempted.
		//
		// "everywhere" is CUPS interrogating the printer over IPP to build a
		// driver from what it answers. A socket, LPD or file device cannot
		// answer that, so sending them down this path produced a queue CUPS
		// then failed to configure, and an error saying only that the printing
		// system was not answering. That is precisely the old printer this
		// project exists for: found by SNMP, no device id, nothing else to go
		// on. Saying what is actually needed beats failing obscurely.
		return nil, jsonrpc.Errorf(jsonrpc.CodeDriverRequired,
			"this printer did not say what model it is, so a driver has to be chosen by hand")
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

		// And undo it in CUPS, which is not as redundant as it looks.
		//
		// CUPS-Add-Modify-Printer can fail *after* creating the queue: it makes
		// the queue, then tries to reach the printer to work out its
		// capabilities, and reports an error while leaving the queue in place.
		// Without this, every failed attempt leaves a queue printer-cycle does
		// not know about, invisible in the interface and impossible to remove
		// through it, while still being advertised by CUPS.
		//
		// Best effort: if the queue was never created this is a no-op, and if
		// this fails too there is nothing further to try.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if orphan := c.server.cups.DeletePrinter(cleanup, record.QueueName); orphan != nil &&
			!errors.Is(orphan, ipp.ErrNotFound) {
			c.log.Warn("a queue may have been left behind by a failed add",
				"queue", record.QueueName, "error", orphan)
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

// canDeriveDriver reports whether CUPS can build a driver by asking the device.
//
// Only the transports that speak IPP. CUPS derives an "everywhere" driver by
// querying the printer for its capabilities, which a socket, LPD, serial or
// file device has no way to answer.
func canDeriveDriver(uri string) bool {
	scheme, _, ok := strings.Cut(uri, "://")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "ipp", "ipps", "dnssd":
		return true
	}
	return false
}
