package ipp_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/mhd64real/printer-cycle/internal/ipp"
)

func TestNewAcceptsSupportedEndpointForms(t *testing.T) {
	valid := []string{
		"unix:///run/cups/cups.sock",
		"http://127.0.0.1:6631",
		"https://cups.example:631",
	}
	for _, ep := range valid {
		if _, err := ipp.New(ep); err != nil {
			t.Errorf("New(%q) = %v, want no error", ep, err)
		}
	}

	invalid := []string{
		"",                    // no scheme at all
		"ipp://host/",         // ipp is the protocol, not the transport
		"unix://",             // no socket path
		"http://",             // no host
		"/run/cups/cups.sock", // a bare path is not an endpoint
	}
	for _, ep := range invalid {
		if _, err := ipp.New(ep); err == nil {
			t.Errorf("New(%q) returned no error, want one", ep)
		}
	}
}

func TestURIsAreBuiltForCUPS(t *testing.T) {
	c, err := ipp.New("http://127.0.0.1:6631")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := c.RootURI(), "ipp://127.0.0.1:6631/"; got != want {
		t.Errorf("RootURI() = %q, want %q", got, want)
	}
	if got, want := c.PrinterURI("file-ps"), "ipp://127.0.0.1:6631/printers/file-ps"; got != want {
		t.Errorf("PrinterURI() = %q, want %q", got, want)
	}
	// Users name printers things like "Office Laser". If escaping is wrong the
	// resulting URI is invalid and CUPS rejects the whole operation.
	if got, want := c.PrinterURI("Office Laser"), "ipp://127.0.0.1:6631/printers/Office%20Laser"; got != want {
		t.Errorf("PrinterURI() with a space = %q, want %q", got, want)
	}
}

func TestNewRequestMeetsRFC8011(t *testing.T) {
	c, err := ipp.New("http://127.0.0.1:6631")
	if err != nil {
		t.Fatal(err)
	}

	first := c.NewRequest(goipp.OpCupsGetDefault)
	second := c.NewRequest(goipp.OpCupsGetDefault)

	// RFC 8011 requires a request-id greater than zero, and CUPS matches
	// responses to requests by it, so repeats would be silently confusing.
	if first.RequestID == 0 {
		t.Error("request id is zero, RFC 8011 requires greater than zero")
	}
	if first.RequestID == second.RequestID {
		t.Errorf("two requests share id %d, they must be distinct", first.RequestID)
	}

	// RFC 8011 requires these two attributes, in this order, before any other
	// operation attribute. CUPS is lenient about it; other IPP servers are not.
	if len(first.Operation) < 2 {
		t.Fatalf("operation group has %d attributes, want at least 2", len(first.Operation))
	}
	if got, want := first.Operation[0].Name, "attributes-charset"; got != want {
		t.Errorf("first operation attribute = %q, want %q", got, want)
	}
	if got, want := first.Operation[1].Name, "attributes-natural-language"; got != want {
		t.Errorf("second operation attribute = %q, want %q", got, want)
	}
}

// TestRoundTripAgainstCUPS is the point of this stage: proving a request built
// here reaches a real cupsd and a real response comes back.
//
// It needs the development environment, so it is skipped unless
// PRINTER_CYCLE_TEST_CUPS is set. That keeps CI green without a container.
// integrationClient returns a client pointed at the development CUPS, or skips
// the test when there is not one. CI has no container, so every test that needs
// a real cupsd goes through here.
func integrationClient(t *testing.T) *ipp.Client {
	t.Helper()

	endpoint := os.Getenv("PRINTER_CYCLE_TEST_CUPS")
	if endpoint == "" {
		t.Skip("set PRINTER_CYCLE_TEST_CUPS to run this: make dev-up, then make test-integration")
	}

	c, err := ipp.New(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTripAgainstCUPS(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := c.NewRequest(goipp.OpCupsGetDefault)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	// The transport is what is under test. A well formed IPP response carrying
	// the request id back proves the exchange worked end to end. Whether a
	// default printer happens to be configured is not this test\'s business, so
	// the status code is logged rather than asserted.
	if resp.RequestID != req.RequestID {
		t.Errorf("response request id = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Version != req.Version {
		t.Errorf("response version = %v, want %v", resp.Version, req.Version)
	}
	t.Logf("cupsd answered, status %s", goipp.Status(resp.Code))
}

// TestPrintersAgainstCUPS is the point of this stage: the two virtual queues the
// development environment creates should come back as typed Go values, not as
// text to be picked apart.
func TestPrintersAgainstCUPS(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	printers, err := c.Printers(ctx)
	if err != nil {
		t.Fatalf("Printers: %v", err)
	}

	byName := make(map[string]ipp.Printer, len(printers))
	names := make([]string, 0, len(printers))
	for _, p := range printers {
		byName[p.Name] = p
		names = append(names, p.Name)
	}

	for _, want := range []string{"file-ps", "file-pcl"} {
		p, ok := byName[want]
		if !ok {
			t.Fatalf("queue %q missing, got %v. Run: make dev-printers", want, names)
		}

		if p.State != ipp.PrinterStateIdle {
			t.Errorf("%s: state = %s, want idle", want, p.State)
		}
		if !p.AcceptingJobs {
			t.Errorf("%s: not accepting jobs", want)
		}
		if p.DeviceURI == "" {
			t.Errorf("%s: device-uri is empty", want)
		}
		if p.MakeAndModel == "" {
			t.Errorf("%s: printer-make-and-model is empty", want)
		}
		if p.URI == "" {
			t.Errorf("%s: printer-uri-supported is empty", want)
		}

		t.Logf("%-9s state=%-10s model=%-28q device=%s",
			p.Name, p.State, p.MakeAndModel, p.DeviceURI)
	}
}

func TestStatusesMapToSentinels(t *testing.T) {
	cases := []struct {
		status goipp.Status
		want   error
	}{
		{goipp.StatusErrorNotFound, ipp.ErrNotFound},
		{goipp.StatusErrorForbidden, ipp.ErrForbidden},
		{goipp.StatusErrorNotAuthenticated, ipp.ErrNotAuthenticated},
		{goipp.StatusErrorDocumentFormatNotSupported, ipp.ErrFormatUnsupported},
		{goipp.StatusErrorConflicting, ipp.ErrConflict},
		// Anything from 0x0500 up is the server's problem rather than the
		// request's, and is the category worth retrying.
		{goipp.Status(0x0501), ipp.ErrServer},
		{goipp.Status(0x050b), ipp.ErrServer},
	}

	for _, tc := range cases {
		err := &ipp.Error{Op: goipp.OpCupsGetPrinters, Status: tc.status}
		if !errors.Is(err, tc.want) {
			t.Errorf("status %s does not satisfy errors.Is for %v", tc.status, tc.want)
		}
	}

	// A status with no sentinel must not accidentally match one.
	odd := &ipp.Error{Status: goipp.Status(0x0499)}
	if errors.Is(odd, ipp.ErrNotFound) {
		t.Error("an unmapped status matched ErrNotFound")
	}
}

// TestMissingPrinterIsTypedNotFound is the point of this stage: a queue that
// does not exist has to come back as a value callers can branch on, not as a
// string they have to match against.
func TestMissingPrinterIsTypedNotFound(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := c.Printer(ctx, "this-queue-does-not-exist")
	if err == nil {
		t.Fatal("Printer on a missing queue returned no error")
	}
	if !errors.Is(err, ipp.ErrNotFound) {
		t.Fatalf("err = %v, want it to satisfy errors.Is(err, ipp.ErrNotFound)", err)
	}

	status, ok := ipp.StatusOf(err)
	if !ok {
		t.Error("StatusOf found no IPP status on the error")
	}
	t.Logf("typed error: %v (status %s)", err, status)
}

func TestPrinterByNameAgainstCUPS(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p, err := c.Printer(ctx, "file-ps")
	if err != nil {
		t.Fatalf("Printer(file-ps): %v. Run: make dev-printers", err)
	}
	if p.Name != "file-ps" {
		t.Errorf("Name = %q, want file-ps", p.Name)
	}
	if p.State != ipp.PrinterStateIdle {
		t.Errorf("State = %s, want idle", p.State)
	}
	t.Logf("%s: %s, %s", p.Name, p.MakeAndModel, p.State)
}

// TestDevicesAgainstCUPS is what the second dev container exists for: proving
// discovery returns real hardware-shaped results rather than an empty list.
func TestDevicesAgainstCUPS(t *testing.T) {
	c := integrationClient(t)

	// Generous: the SNMP backend waits out a subnet broadcast before CUPS
	// answers at all.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	devices, err := c.Devices(ctx, 10*time.Second)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	var found bool
	for _, d := range devices {
		t.Logf("%-8s class=%-8s uri=%s", d.Transport, d.Class, d.URI)

		// Every backend advertises itself with a bare scheme as its URI. If one
		// of those survived the filter, the dashboard would offer users
		// "printers" that are really connection types.
		if !strings.Contains(d.URI, ":") {
			t.Errorf("a pseudo-device got through the filter: %q", d.URI)
		}
		if d.Transport == "dnssd" {
			found = true
		}
	}

	if !found {
		t.Fatalf("no dnssd device discovered among %d results. Is the virtual printer container up, and did mDNS have a few seconds? Run: make dev-up", len(devices))
	}
}

// TestDiscoveryIsProgressive is the point of this stage. Devices must reach the
// caller as CUPS finds them, not in one batch at the end.
//
// The property being checked is the spread between arrivals. If the response
// were buffered and decoded once at the end, every device would land within
// microseconds of every other. Measured against the development environment,
// cupsd answers the fast backends immediately and then trickles devices out over
// the next couple of seconds.
func TestDiscoveryIsProgressive(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type arrival struct {
		device ipp.Device
		at     time.Duration
	}

	start := time.Now()
	var arrivals []arrival

	err := c.DiscoverDevices(ctx, 10*time.Second, func(d ipp.Device) {
		arrivals = append(arrivals, arrival{d, time.Since(start)})
	})
	if err != nil {
		t.Fatalf("DiscoverDevices: %v", err)
	}
	total := time.Since(start)

	if len(arrivals) == 0 {
		t.Fatal("no devices discovered. Run: make dev-up")
	}
	for _, a := range arrivals {
		t.Logf("t=%-8v %-8s %s", a.at.Round(10*time.Millisecond), a.device.Transport, a.device.URI)
	}
	t.Logf("discovery finished in %v", total.Round(10*time.Millisecond))

	if len(arrivals) > 1 {
		spread := arrivals[len(arrivals)-1].at - arrivals[0].at
		if spread < 100*time.Millisecond {
			t.Errorf("all %d devices arrived within %v of one another; that is a batch, not a stream",
				len(arrivals), spread.Round(time.Millisecond))
		}
	}

	// The collected form must agree with the streaming form, since one is now
	// built on the other.
	batch, err := c.Devices(ctx, 10*time.Second)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(batch) != len(arrivals) {
		t.Errorf("Devices returned %d, DiscoverDevices delivered %d", len(batch), len(arrivals))
	}
}

// The IEEE 1284 id of an HP LaserJet 1018: a cheap host-based laser from 2005,
// squarely the kind of printer this project exists to revive.
const laserJet1018 = "MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;"

// TestPPDsNarrowByDeviceID is the point of this stage. A full installation
// carries close to eighteen thousand drivers. Asking a user to find theirs in
// that list is the experience printer-cycle exists to replace, so narrowing by
// what the hardware reports about itself has to work.
func TestPPDsNarrowByDeviceID(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	got, err := c.PPDs(ctx, ipp.PPDFilter{DeviceID: laserJet1018})
	if err != nil {
		t.Fatalf("PPDs: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no candidates for a printer the installed drivers definitely cover")
	}
	if len(got) > 50 {
		t.Errorf("%d candidates: that is not narrowing, that is still a haystack", len(got))
	}

	var foo2zjs bool
	for _, p := range got {
		t.Logf("%-52s %s", p.Name, p.MakeAndModel)
		if strings.Contains(p.Name, "foo2zjs") {
			foo2zjs = true
		}
	}
	if !foo2zjs {
		t.Error("the LaserJet 1018 is a foo2zjs printer; that driver should be among the candidates")
	}
}

// A filter matching nothing has to be an empty result, not a failure. Callers
// need to tell "no driver claims this printer" apart from "the query broke",
// and the first will happen often enough to be routine.
func TestPPDsUnmatchedFilterIsEmptyNotAnError(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	got, err := c.PPDs(ctx, ipp.PPDFilter{DeviceID: "MFG:NoSuchCompany;MDL:No Such Printer 9000;"})
	if err != nil {
		t.Fatalf("an unmatched filter returned an error, want an empty result: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candidates for a printer that does not exist", len(got))
	}
}

func TestPPDsLimitIsHonoured(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	got, err := c.PPDs(ctx, ipp.PPDFilter{Limit: 5})
	if err != nil {
		t.Fatalf("PPDs: %v", err)
	}
	if len(got) == 0 || len(got) > 5 {
		t.Errorf("got %d drivers, want between 1 and 5", len(got))
	}
}

// Records the real size and cost of the unfiltered catalogue, which is the
// argument for always filtering. Logs rather than asserts, since the number
// depends on which driver packages are installed.
func TestPPDCatalogueSize(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	all, err := c.PPDs(ctx, ipp.PPDFilter{})
	if err != nil {
		t.Fatalf("PPDs: %v", err)
	}
	t.Logf("unfiltered catalogue: %d drivers, fetched and decoded in %v",
		len(all), time.Since(start).Round(10*time.Millisecond))
}

func TestPrinterNameValidation(t *testing.T) {
	valid := []string{"file-ps", "Office_Laser", "hp.laserjet.1018", "a", strings.Repeat("x", 127)}
	for _, n := range valid {
		if err := ipp.ValidPrinterName(n); err != nil {
			t.Errorf("ValidPrinterName(%q) = %v, want nil", n, err)
		}
	}

	invalid := map[string]string{
		"":                       "empty",
		"Office Laser":           "a space, which is what users type first",
		"floor/2":                "a slash",
		"printer#1":              "a hash",
		"bad\tname":              "a control character",
		strings.Repeat("x", 128): "one character too long",
	}
	for n, why := range invalid {
		if err := ipp.ValidPrinterName(n); err == nil {
			t.Errorf("ValidPrinterName(%q) accepted %s", n, why)
		}
	}
}

// Users type readable names. CUPS refuses most of them. Sanitising has to
// produce something legal without producing something unrecognisable.
func TestSanitiseName(t *testing.T) {
	cases := map[string]string{
		"Office Laser":             "Office_Laser",
		"Office Laser (2nd floor)": "Office_Laser_2nd_floor",
		"  HP  LaserJet  1018  ":   "HP_LaserJet_1018",
		"hp.laserjet-1018":         "hp.laserjet-1018",
		"///":                      "",
		"":                         "",
	}
	for in, want := range cases {
		if got := ipp.SanitiseName(in); got != want {
			t.Errorf("SanitiseName(%q) = %q, want %q", in, got, want)
		}
	}

	// Whatever comes out has to be something CUPS will actually accept, or the
	// sanitiser has simply moved the failure further down the line.
	for in := range cases {
		got := ipp.SanitiseName(in)
		if got == "" {
			continue
		}
		if err := ipp.ValidPrinterName(got); err != nil {
			t.Errorf("SanitiseName(%q) produced %q, which CUPS rejects: %v", in, got, err)
		}
	}
}

// TestAddAndDeletePrinterAgainstCUPS is the first operation that changes state
// rather than reading it.
func TestAddAndDeletePrinterAgainstCUPS(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const name = "printer-cycle-test-queue"

	// Leave nothing behind, whatever happens in the middle.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = c.DeletePrinter(cleanup, name)
	})
	_ = c.DeletePrinter(ctx, name)

	err := c.AddPrinter(ctx, ipp.PrinterSpec{
		Name:      name,
		DeviceURI: "file:///var/spool/pc-out/test-queue.out",
		PPDName:   "drv:///sample.drv/generic.ppd",
		Info:      "Created by the printer-cycle test suite",
		Location:  "Nowhere",
	})
	if err != nil {
		t.Fatalf("AddPrinter: %v", err)
	}

	got, err := c.Printer(ctx, name)
	if err != nil {
		t.Fatalf("the queue was created but cannot be read back: %v", err)
	}
	if got.Info != "Created by the printer-cycle test suite" {
		t.Errorf("Info = %q, the human-readable name did not survive", got.Info)
	}
	if got.State != ipp.PrinterStateIdle {
		t.Errorf("State = %s, want idle: a freshly paired queue must be usable immediately", got.State)
	}
	if !got.AcceptingJobs {
		t.Error("the new queue is not accepting jobs, so the first print would vanish")
	}
	t.Logf("created %s: %s, %s, %s", got.Name, got.MakeAndModel, got.State, got.DeviceURI)

	if err := c.DeletePrinter(ctx, name); err != nil {
		t.Fatalf("DeletePrinter: %v", err)
	}

	if _, err := c.Printer(ctx, name); !errors.Is(err, ipp.ErrNotFound) {
		t.Errorf("after deletion, Printer returned %v, want ErrNotFound", err)
	}
}
