package dashboard_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// field is one part of a multipart upload, in the order it is sent.
type field struct {
	name     string
	filename string
	mime     string
	value    string
}

func postPrint(t *testing.T, srv *httptest.Server, jar http.CookieJar, fields []field) *http.Response {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, f := range fields {
		if f.filename == "" {
			if err := form.WriteField(f.name, f.value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		header := make(map[string][]string)
		header["Content-Disposition"] = []string{
			`form-data; name="` + f.name + `"; filename="` + f.filename + `"`,
		}
		if f.mime != "" {
			header["Content-Type"] = []string{f.mime}
		}
		part, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, f.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Jar: jar}
	resp, err := client.Post(srv.URL+"/api/print", form.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST /api/print: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func openStream(core *fakeCore, id uint32) {
	core.reply["jobs.submit"] = map[string]any{"job_id": "job_1", "stream_id": id}
	core.reply["jobs.commit"] = map[string]any{"job_id": "job_1", "state": "queued"}
}

// The whole point of the endpoint: a document goes in, and the same bytes come
// out the other side into core.
func TestPrintingStreamsTheDocumentToCore(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 7)

	document := "%PDF-1.4 a document that has to arrive unchanged"
	resp := postPrint(t, srv, jar, []field{
		{name: "printer_id", value: "prn_1"},
		{name: "copies", value: "2"},
		{name: "duplex", value: "true"},
		{name: "file", filename: "report.pdf", mime: "application/pdf", value: document},
	})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("printing returned %d: %s", resp.StatusCode, body)
	}

	submitted, ok := core.lastCall("jobs.submit")
	if !ok {
		t.Fatal("nothing asked core to start a job")
	}
	if submitted.params["printer_id"] != "prn_1" {
		t.Errorf("printer reached core as %v", submitted.params["printer_id"])
	}

	doc, _ := submitted.params["document"].(map[string]any)
	if doc["filename"] != "report.pdf" {
		t.Errorf("filename reached core as %v", doc["filename"])
	}
	if doc["mime"] != "application/pdf" {
		t.Errorf("format reached core as %v", doc["mime"])
	}

	opts, _ := submitted.params["options"].(map[string]any)
	if opts["copies"] != float64(2) {
		t.Errorf("copies reached core as %v", opts["copies"])
	}
	if opts["duplex"] != true {
		t.Errorf("duplex reached core as %v", opts["duplex"])
	}

	core.mu.Lock()
	got := string(core.documents[7])
	core.mu.Unlock()
	if got != document {
		t.Errorf("core received %q, want %q", got, document)
	}

	// Commit has to describe what was actually sent, because that is the only
	// thing letting core tell a whole document from a truncated one.
	committed, ok := core.lastCall("jobs.commit")
	if !ok {
		t.Fatal("the job was never committed")
	}
	if committed.params["bytes"] != float64(len(document)) {
		t.Errorf("commit claimed %v bytes, want %d", committed.params["bytes"], len(document))
	}
	if digest, _ := committed.params["sha256"].(string); len(digest) < 8 {
		t.Errorf("commit carried no usable checksum: %q", digest)
	}
}

// An option left unset must stay unset. Sending false for a printer that
// defaults to duplex would quietly change what somebody gets.
func TestUnsetOptionsAreNotSent(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	postPrint(t, srv, jar, []field{
		{name: "printer_id", value: "prn_1"},
		{name: "file", filename: "a.pdf", mime: "application/pdf", value: "%PDF-1.4"},
	})

	submitted, _ := core.lastCall("jobs.submit")
	opts, _ := submitted.params["options"].(map[string]any)
	for _, name := range []string{"duplex", "color", "media", "copies"} {
		if _, present := opts[name]; present {
			t.Errorf("%s was sent to core as %v when the page never set it", name, opts[name])
		}
	}
}

// The page must not be able to print as somebody else, the same way it cannot
// through the relay.
func TestPrintingUsesTheSessionFromTheCookie(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	postPrint(t, srv, jar, []field{
		{name: "session", value: "somebody-elses-session"},
		{name: "printer_id", value: "prn_1"},
		{name: "file", filename: "a.pdf", mime: "application/pdf", value: "%PDF-1.4"},
	})

	submitted, _ := core.lastCall("jobs.submit")
	if submitted.params["session"] != "core-session-token" {
		t.Errorf("session reached core as %v, want the one from the cookie",
			submitted.params["session"])
	}
}

func TestPrintingRequiresSigningIn(t *testing.T) {
	srv, core := testServer(t)
	openStream(core, 1)

	resp := postPrint(t, srv, newJar(t), []field{
		{name: "printer_id", value: "prn_1"},
		{name: "file", filename: "a.pdf", mime: "application/pdf", value: "%PDF-1.4"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unauthenticated print returned %d, want 401", resp.StatusCode)
	}
	if _, ok := core.lastCall("jobs.submit"); ok {
		t.Error("an unauthenticated print reached core")
	}
}

// The document is read as a stream, so the fields describing it have to arrive
// first. A request that breaks that is refused by name rather than printed with
// silent defaults, which would send somebody's document to the wrong printer.
func TestADocumentBeforeThePrinterIsRefused(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	resp := postPrint(t, srv, jar, []field{
		{name: "file", filename: "a.pdf", mime: "application/pdf", value: "%PDF-1.4"},
		{name: "printer_id", value: "prn_1"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a document sent before the printer returned %d, want 400", resp.StatusCode)
	}
	if _, ok := core.lastCall("jobs.submit"); ok {
		t.Error("a job was started for a request that named no printer")
	}
}

func TestAnUploadWithNoDocumentIsRefused(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)

	resp := postPrint(t, srv, jar, []field{{name: "printer_id", value: "prn_1"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an upload with no document returned %d, want 400", resp.StatusCode)
	}

	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error == "" {
		t.Error("the refusal said nothing about what was wrong")
	}
}

// Browsers send application/octet-stream for anything they do not recognise,
// which says nothing about the format. The extension answers those, because a
// queue that cannot print octet-stream would otherwise refuse a perfectly
// ordinary PDF.
func TestAnUnhelpfulBrowserContentTypeFallsBackToTheExtension(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	postPrint(t, srv, jar, []field{
		{name: "printer_id", value: "prn_1"},
		{name: "file", filename: "notes.pdf", mime: "application/octet-stream", value: "%PDF-1.4"},
	})

	submitted, _ := core.lastCall("jobs.submit")
	doc, _ := submitted.params["document"].(map[string]any)
	if doc["mime"] != "application/pdf" {
		t.Errorf("format reached core as %v, want application/pdf from the extension", doc["mime"])
	}
}

// A filename is not a path. A part claiming one must not be able to say
// anything about where it came from.
func TestAFilenameCannotCarryAPath(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	postPrint(t, srv, jar, []field{
		{name: "printer_id", value: "prn_1"},
		{name: "file", filename: "../../etc/passwd.pdf", mime: "application/pdf", value: "%PDF"},
	})

	submitted, _ := core.lastCall("jobs.submit")
	doc, _ := submitted.params["document"].(map[string]any)
	if doc["filename"] != "passwd.pdf" {
		t.Errorf("filename reached core as %v, want just the base name", doc["filename"])
	}
}

func TestNonsenseOptionsAreRefused(t *testing.T) {
	srv, core := testServer(t)
	jar := signInAndKeepCookie(t, srv, core)
	openStream(core, 1)

	for _, bad := range []field{
		{name: "copies", value: "0"},
		{name: "copies", value: "-3"},
		{name: "copies", value: "many"},
		{name: "duplex", value: "sometimes"},
	} {
		resp := postPrint(t, srv, jar, []field{
			{name: "printer_id", value: "prn_1"},
			bad,
			{name: "file", filename: "a.pdf", mime: "application/pdf", value: "%PDF"},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s=%q returned %d, want 400", bad.name, bad.value, resp.StatusCode)
		}
	}
}
