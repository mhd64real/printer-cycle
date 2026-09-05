package firmware_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/firmware"
)

// A stand-in for arm2hpdl: reads stdin, writes it back with a marker.
//
// The real converter prepends an HP download header to an ARM binary. What is
// being tested here is the plumbing around it, so a script that proves the
// bytes went in and came out is enough, and it keeps the test running on a
// machine with no printer drivers installed.
func fakeConverter(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}

	path := filepath.Join(t.TempDir(), "arm2hpdl")
	script := "#!/bin/sh\nprintf 'HPDL'\ncat\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// failingConverter exits non-zero, the way the real one would on a file that is
// not an ARM binary at all.
func failingConverter(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arm2hpdl")
	script := "#!/bin/sh\necho 'not an ARM ELF binary' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func tarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func installer(t *testing.T, handler http.Handler) *firmware.Installer {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &firmware.Installer{
		Mirror:    srv.URL,
		Dir:       t.TempDir(),
		Converter: fakeConverter(t),
		HTTP:      srv.Client(),
	}
}

func TestFetchingFirmware(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sihp1018.tar.gz" {
			t.Errorf("asked for %q", r.URL.Path)
		}
		w.Write(tarball(t, "sihp1018.img", []byte("pretend arm binary")))
	}))

	if in.Installed("sihp1018.dl") {
		t.Fatal("it thinks the firmware is already there")
	}
	if err := in.Install(context.Background(), "sihp1018.dl"); err != nil {
		t.Fatalf("installing: %v", err)
	}
	if !in.Installed("sihp1018.dl") {
		t.Fatal("it installed nothing")
	}

	got, err := os.ReadFile(filepath.Join(in.Dir, "sihp1018.dl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "HPDLpretend arm binary" {
		t.Errorf("installed %q, so it did not go through the converter", got)
	}
}

// Installing what is already there is not an error and not a download.
func TestAlreadyInstalledIsNotFetchedAgain(t *testing.T) {
	var requests int
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write(tarball(t, "sihp1018.img", []byte("arm")))
	}))

	ctx := context.Background()
	if err := in.Install(ctx, "sihp1018.dl"); err != nil {
		t.Fatal(err)
	}
	if err := in.Install(ctx, "sihp1018.dl"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Errorf("downloaded %d times, want 1", requests)
	}
}

// The stage's done-when. A box in a cupboard with no internet has to say so,
// rather than leaving somebody with a printer that prints nothing.
func TestBeingOfflineExplainsItself(t *testing.T) {
	in := &firmware.Installer{
		// A name that cannot resolve, which is what being offline looks like
		// from inside this code.
		Mirror:    "https://printer-cycle-nothing-resolves-here.invalid/printers",
		Dir:       t.TempDir(),
		Converter: fakeConverter(t),
		HTTP:      &http.Client{},
	}

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("being offline was reported as success")
	}

	message := err.Error()
	for _, fragment := range []string{"not connected to the internet", "downloaded once"} {
		if !strings.Contains(message, fragment) {
			t.Errorf("the message does not say %q: %s", fragment, message)
		}
	}
	// And it must not be the raw transport error, which tells a person with a
	// printer in a cupboard nothing at all.
	if strings.Contains(message, "dial tcp") {
		t.Errorf("the message is the transport's, not ours: %s", message)
	}
}

// A mirror that has moved is a different problem from being offline, and saying
// so is the difference between waiting and looking somewhere else.
func TestAMissingFileSaysTheMirrorMoved(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("a 404 was reported as success")
	}
	if !strings.Contains(err.Error(), "mirror") {
		t.Errorf("the message does not mention the mirror: %v", err)
	}
	if in.Installed("sihp1018.dl") {
		t.Error("a failed download left a file behind")
	}
}

// A mirror serving a login page instead of an archive is a real thing that
// happens on captive portals, and it must not look like a corrupt printer.
func TestSomethingThatIsNotAnArchiveSaysSo(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>sign in to the wifi</html>"))
	}))

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("a web page was accepted as firmware")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("the message does not say what was wrong with it: %v", err)
	}
}

// The right archive with the wrong contents.
func TestAnArchiveWithoutTheFirmwareSaysSo(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(tarball(t, "README", []byte("not firmware")))
	}))

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("an archive with no firmware in it was accepted")
	}
	if !strings.Contains(err.Error(), "sihp1018.img") {
		t.Errorf("the message does not name what was missing: %v", err)
	}
}

// Not having the converter is a missing package, not a broken printer.
func TestAMissingConverterNamesTheTool(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(tarball(t, "sihp1018.img", []byte("arm")))
	}))
	in.Converter = "printer-cycle-no-such-tool"

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("a missing converter was reported as success")
	}
	if !strings.Contains(err.Error(), "foo2zjs") {
		t.Errorf("the message does not say where the tool comes from: %v", err)
	}
}

// A converter that refuses the file must not leave a broken one installed. A
// truncated firmware file is worse than none: the printer would take it.
func TestAFailedConversionLeavesNothingBehind(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(tarball(t, "sihp1018.img", []byte("arm")))
	}))
	in.Converter = failingConverter(t)

	if err := in.Install(context.Background(), "sihp1018.dl"); err == nil {
		t.Fatal("a failing conversion was reported as success")
	}
	if in.Installed("sihp1018.dl") {
		t.Error("a half-converted firmware file was left where the printer would find it")
	}

	entries, err := os.ReadDir(in.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("left %q behind", e.Name())
	}
}

// Being unable to write is said before the download rather than after, so a
// slow connection is not spent discovering it.
func TestAnUnwritableDirectorySaysSoFirst(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}

	var requests int
	in := installer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Write(tarball(t, "sihp1018.img", []byte("arm")))
	}))

	dir := t.TempDir()
	readOnly := filepath.Join(dir, "firmware")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	in.Dir = readOnly

	err := in.Install(context.Background(), "sihp1018.dl")
	if err == nil {
		t.Fatal("writing to a read-only directory was reported as success")
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Errorf("the message does not mention permission: %v", err)
	}
	if requests != 0 {
		t.Errorf("downloaded %d times before finding out it could not save the result", requests)
	}
}

func TestAnUnknownFirmwareFileIsRefused(t *testing.T) {
	in := installer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	err := in.Install(context.Background(), "sihp9999.dl")
	if err == nil {
		t.Fatal("a firmware file nobody knows about was accepted")
	}
	if !strings.Contains(err.Error(), "sihp9999.dl") {
		t.Errorf("the message does not name the file: %v", err)
	}
}
