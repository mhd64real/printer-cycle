package ipp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

	// No assertion about the spread between arrivals here, deliberately.
	//
	// An earlier version required them to be spread over time and was flaky:
	// when CUPS finds two devices at nearly the same moment they are delivered
	// at nearly the same moment, which is correct behaviour failing an
	// assertion about it. Progressive delivery is proven deterministically in
	// TestDiscoveryEmitsBeforeTheResponseEnds, against a server that pauses
	// mid-stream on purpose. What this test is for is that discovery works
	// against a real cupsd at all.

	// The collected form is built on the streaming form, so it must also work.
	//
	// Counts are deliberately NOT compared between the two calls. Discovery is
	// not deterministic: mDNS answers arrive when they arrive, and two runs
	// seconds apart legitimately see different numbers of services. An earlier
	// version of this test asserted the counts matched and failed intermittently
	// for that reason, which was the test being wrong rather than the code.
	batch, err := c.Devices(ctx, 10*time.Second)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(batch) == 0 {
		t.Error("Devices returned nothing while DiscoverDevices found devices")
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

// devOut is the queue output directory, bind-mounted from the CUPS container so
// tests can check printed bytes directly.
const devOut = "../../dev/out"

// scratchQueue creates a throwaway queue writing to its own file, and returns
// the queue name and the host path of that file.
func scratchQueue(t *testing.T, c *ipp.Client, name, ppd string) (string, string) {
	t.Helper()

	if _, err := os.Stat(devOut); err != nil {
		t.Skipf("%s is not present; run make dev-up", devOut)
	}

	hostPath := filepath.Join(devOut, name+".out")
	_ = os.Remove(hostPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = c.DeletePrinter(ctx, name)
	err := c.AddPrinter(ctx, ipp.PrinterSpec{
		Name:      name,
		DeviceURI: "file:///var/spool/pc-out/" + name + ".out",
		PPDName:   ppd,
		Info:      "printer-cycle test queue",

		// Shared, which production queues are not.
		//
		// CUPS refuses print jobs submitted from a remote client to a queue
		// that is not shared: "The printer or class is not shared." Production
		// reaches cupsd over its Unix socket, which counts as local, so an
		// unshared queue is both correct and preferable there, since sharing
		// would have CUPS advertise the printer alongside the connector that is
		// already advertising it. The development environment talks over TCP
		// and therefore counts as remote, so these queues must be shared.
		Shared: true,
	})
	if err != nil {
		t.Fatalf("creating scratch queue: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		_ = c.DeletePrinter(cctx, name)
	})
	return name, hostPath
}

// waitForFile waits for a queue to finish writing its output.
func waitForFile(t *testing.T, path string, wantSize int, timeout time.Duration) []byte {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) >= wantSize {
			return data
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no output at %s after %v: %v", path, timeout, err)
	}
	t.Fatalf("output at %s is %d bytes after %v, want at least %d", path, len(data), timeout, wantSize)
	return nil
}

// TestPrintJobThroughTheFilterChain prints ordinary text and lets CUPS do its
// real work: texttopdf, then Ghostscript, then the PostScript driver. That is
// the path an actual user takes, and the path every old printer depends on.
func TestPrintJobThroughTheFilterChain(t *testing.T) {
	c := integrationClient(t)
	queue, hostPath := scratchQueue(t, c, "pc-test-filtered", "drv:///sample.drv/generic.ppd")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	job, err := c.PrintJob(ctx, queue, strings.NewReader("printer-cycle filter chain check\n"), ipp.PrintOptions{
		JobName: "filtered",
		Format:  "text/plain",
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}
	if job.ID == 0 {
		t.Error("no job id returned")
	}

	got := waitForFile(t, hostPath, 1024, 60*time.Second)

	if !bytes.HasPrefix(got, []byte("%!PS")) {
		t.Error("output does not begin with a PostScript header, so the filter chain did not run")
	}
	// Ghostscript names itself in the output it generates, which is direct
	// evidence the rasterisation chain ran rather than bytes being copied.
	if !bytes.Contains(got[:min(2048, len(got))], []byte("gs ")) {
		t.Error("no Ghostscript invocation in the output header")
	}

	// The literal text is deliberately not asserted. It does not survive: the
	// chain embeds a font subset and draws glyphs, so "printer-cycle filter
	// chain check" appears nowhere in the PostScript. Checked, not assumed.
	t.Logf("job %d turned 33 bytes of text into %d bytes of PostScript", job.ID, len(got))
}

// TestPrintJobStreamsWithoutBuffering is the memory claim the whole design rests
// on. A 32MB document must leave this process without ever being resident, or a
// Pi Zero 2 W sharing 512MB between the OS, cupsd and Ghostscript gets an
// out-of-memory kill the first time somebody prints a large scan.
//
// It also confirms every byte arrived, by reading job-k-octets back from CUPS.
// The Print-Job response does not carry that, so it needs a second query.
func TestPrintJobStreamsWithoutBuffering(t *testing.T) {
	c := integrationClient(t)
	queue, _ := scratchQueue(t, c, "pc-test-stream", "drv:///sample.drv/generic.ppd")

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Pause the queue first. CUPS still accepts the whole document, which is
	// what is being measured, but never spends CPU rasterising 32MB of it. An
	// earlier version of this test left Ghostscript grinding away after the
	// test returned and starved a later test into timing out.
	if err := c.PausePrinter(ctx, queue); err != nil {
		t.Fatalf("PausePrinter: %v", err)
	}

	const size = 32 << 20

	// A valid PostScript document padded to 32MB with comment lines, generated
	// as it is read. The full document never exists anywhere, so if the heap
	// grows to its size, this code is what put it there.
	doc := io.LimitReader(io.MultiReader(
		strings.NewReader("%!PS-Adobe-3.0\n%%Pages: 1\n"),
		paddingReader{},
	), size)

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak {
				peak = m.HeapAlloc
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	job, err := c.PrintJob(ctx, queue, doc, ipp.PrintOptions{
		JobName: "streaming check",
		Format:  "application/postscript",
	})
	close(stop)
	<-sampled

	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		_ = c.CancelJob(cctx, job.ID)
	})

	growth := int64(peak) - int64(base.HeapAlloc)
	const budget = 16 << 20

	t.Logf("job %d: sent %d MB, peak heap growth %.1f MB", job.ID, size>>20, float64(growth)/(1<<20))

	if growth > budget {
		t.Errorf("heap grew %.1f MB while sending a %d MB document: it is being buffered, not streamed",
			float64(growth)/(1<<20), size>>20)
	}

	// Small heap use would also be consistent with the document never having
	// been sent, so confirm CUPS actually received all of it.
	stored, err := c.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("reading the job back: %v", err)
	}
	t.Logf("CUPS accounted for %.1f MB of the %d MB sent", float64(stored.SizeBytes)/(1<<20), size>>20)

	if stored.SizeBytes < size {
		t.Errorf("CUPS received %d bytes, %d were sent: the stream was truncated", stored.SizeBytes, size)
	}
}

// paddingReader produces endless PostScript comment lines without allocating.
type paddingReader struct{}

func (paddingReader) Read(b []byte) (int, error) {
	const line = "% padding for the streaming test, skipped cheaply by ghostscript\n"
	for i := range b {
		b[i] = line[i%len(line)]
	}
	return len(b), nil
}

// TestJobLifecycleAgainstCUPS covers reading a job back, finding it in a
// listing, and cancelling it.
//
// The queue is paused throughout, so the job stays put instead of racing to
// completion and making the cancel meaningless.
func TestJobLifecycleAgainstCUPS(t *testing.T) {
	c := integrationClient(t)
	queue, _ := scratchQueue(t, c, "pc-test-jobs", "drv:///sample.drv/generic.ppd")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.PausePrinter(ctx, queue); err != nil {
		t.Fatalf("PausePrinter: %v", err)
	}

	submitted, err := c.PrintJob(ctx, queue, strings.NewReader("a job to cancel\n"), ipp.PrintOptions{
		JobName: "cancel me",
		Format:  "text/plain",
		User:    "test-user",
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}

	got, err := c.Job(ctx, submitted.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if got.ID != submitted.ID {
		t.Errorf("id = %d, want %d", got.ID, submitted.ID)
	}
	if got.Name != "cancel me" {
		t.Errorf("Name = %q, want the job name to survive", got.Name)
	}
	if got.User != "test-user" {
		t.Errorf("User = %q, want test-user: job ownership drives who may see it", got.User)
	}
	if got.Printer != queue {
		t.Errorf("Printer = %q, want %q", got.Printer, queue)
	}
	if got.State.Terminal() {
		t.Errorf("state is already %s on a paused queue", got.State)
	}
	t.Logf("job %d: %q for %s on %s, state %s", got.ID, got.Name, got.User, got.Printer, got.State)

	pending, err := c.Jobs(ctx, queue, ipp.JobsNotCompleted, 0)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	var listed bool
	for _, j := range pending {
		if j.ID == submitted.ID {
			listed = true
		}
	}
	if !listed {
		t.Errorf("job %d is not in the queue listing of %d jobs", submitted.ID, len(pending))
	}

	if err := c.CancelJob(ctx, submitted.ID); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	after, err := c.Job(ctx, submitted.ID)
	if err != nil {
		t.Fatalf("reading the job after cancelling: %v", err)
	}
	if after.State != ipp.JobCanceled {
		t.Errorf("state = %s after cancelling, want cancelled", after.State)
	}
	if !after.State.Terminal() {
		t.Error("a cancelled job must report as terminal so watchers stop watching")
	}
}

func TestUnknownJobIsTypedNotFound(t *testing.T) {
	c := integrationClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := c.Job(ctx, 999999)
	if err == nil {
		t.Fatal("reading a job that does not exist returned no error")
	}
	if !errors.Is(err, ipp.ErrNotFound) {
		t.Errorf("err = %v, want it to satisfy errors.Is(err, ipp.ErrNotFound)", err)
	}
}

func TestPauseAndResumeAgainstCUPS(t *testing.T) {
	c := integrationClient(t)
	queue, _ := scratchQueue(t, c, "pc-test-pause", "drv:///sample.drv/generic.ppd")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.PausePrinter(ctx, queue); err != nil {
		t.Fatalf("PausePrinter: %v", err)
	}
	paused, err := c.Printer(ctx, queue)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != ipp.PrinterStateStopped {
		t.Errorf("state = %s after pausing, want stopped", paused.State)
	}
	// A paused queue must keep accepting jobs, or pausing would mean losing
	// everything printed while it was paused.
	if !paused.AcceptingJobs {
		t.Error("a paused queue stopped accepting jobs, so work submitted while paused would be lost")
	}

	if err := c.ResumePrinter(ctx, queue); err != nil {
		t.Fatalf("ResumePrinter: %v", err)
	}
	resumed, err := c.Printer(ctx, queue)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != ipp.PrinterStateIdle {
		t.Errorf("state = %s after resuming, want idle", resumed.State)
	}
}

// TestWatchDeliversJobEvents is what Stage 19 exists to establish: that core can
// learn about job progress from CUPS rather than interrogating it.
func TestWatchDeliversJobEvents(t *testing.T) {
	c := integrationClient(t)
	queue, _ := scratchQueue(t, c, "pc-test-watch", "drv:///sample.drv/generic.ppd")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type seen struct {
		kind  string
		state ipp.JobState
	}

	var (
		mu      sync.Mutex
		events  []seen
		watchOK = make(chan error, 1)
	)

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()

	go func() {
		watchOK <- c.Watch(watchCtx, ipp.WatchOptions{
			Printer:        queue,
			ActiveInterval: 200 * time.Millisecond,
			IdleInterval:   400 * time.Millisecond,
			LeaseDuration:  60 * time.Second,
		}, func(e ipp.Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, seen{e.Type, e.JobState})
		})
	}()

	// Give the subscription a moment to exist before creating something to
	// report, or the event that matters happens before anyone is listening.
	time.Sleep(1 * time.Second)

	job, err := c.PrintJob(ctx, queue, strings.NewReader("watch me print\n"), ipp.PrintOptions{
		JobName: "watched",
		Format:  "text/plain",
	})
	if err != nil {
		t.Fatalf("PrintJob: %v", err)
	}

	// Wait for the job to reach a terminal state, as reported by the events.
	deadline := time.Now().Add(30 * time.Second)
	var completed bool
	for time.Now().Before(deadline) && !completed {
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		for _, e := range events {
			if e.state.Terminal() {
				completed = true
			}
		}
		mu.Unlock()
	}

	stopWatch()
	if err := <-watchOK; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Watch returned %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	for _, e := range events {
		t.Logf("event %-22s job-state=%s", e.kind, e.state)
	}
	if len(events) == 0 {
		t.Fatalf("job %d printed but no events arrived", job.ID)
	}
	if !completed {
		t.Error("no event reported the job reaching a terminal state")
	}

	var created, changed bool
	for _, e := range events {
		switch e.kind {
		case "job-created":
			created = true
		case "job-state-changed", "job-completed":
			changed = true
		}
	}
	if !created {
		t.Error("no job-created event")
	}
	if !changed {
		t.Error("no job state change event, so progress could not be reported to a connector")
	}
}
