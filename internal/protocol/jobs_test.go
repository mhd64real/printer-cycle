package protocol_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

type listedJob struct {
	JobID     string `json:"job_id"`
	PrinterID string `json:"printer_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

func listJobs(t *testing.T, c *client, params map[string]any) []listedJob {
	t.Helper()

	resp := c.call("jobs.list", params)
	if resp.Error != nil {
		t.Fatalf("jobs.list: %v", resp.Error)
	}
	var out struct {
		Jobs []listedJob `json:"jobs"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out.Jobs
}

// A job that was submitted has to be findable afterwards, with enough on it to
// render a row: what it was called, where it went, and how it ended.
func TestJobsListReportsWhatWasPrinted(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, outPath := addTestPrinter(t, c)
	_ = os.Remove(outPath)

	document := []byte("printer-cycle jobs list check\n")
	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "listed.txt", "mime": "text/plain"},
	})
	if resp.Error != nil {
		t.Fatalf("submit: %v", resp.Error)
	}
	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	if err := json.Unmarshal(resp.Result, &opened); err != nil {
		t.Fatal(err)
	}
	c.sendChunk(opened.StreamID, document)
	if resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID, "bytes": len(document),
	}); resp.Error != nil {
		t.Fatalf("commit: %v", resp.Error)
	}

	jobs := listJobs(t, c, map[string]any{"all": true})

	var found *listedJob
	for i := range jobs {
		if jobs[i].JobID == opened.JobID {
			found = &jobs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the job just submitted is not in the list of %d", len(jobs))
	}
	if found.Name != "listed.txt" {
		t.Errorf("name = %q, want listed.txt", found.Name)
	}
	if found.PrinterID != printerID {
		t.Errorf("printer = %q, want %q", found.PrinterID, printerID)
	}
	if found.SizeBytes != int64(len(document)) {
		t.Errorf("size = %d, want %d", found.SizeBytes, len(document))
	}
	if found.CreatedAt == "" {
		t.Error("the job has no created time, so a list cannot be ordered or dated")
	}

	// The states are printer-cycle's, not IPP's. A page switching on the
	// documented names has to match what actually arrives.
	switch found.State {
	case "queued", "held", "printing", "stopped", "done", "failed", "cancelled":
	default:
		t.Errorf("state = %q, which is not one of the documented states", found.State)
	}
}

// The newest first, because a jobs page is about what is happening now.
func TestJobsListPutsTheNewestFirst(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, outPath := addTestPrinter(t, c)
	_ = os.Remove(outPath)

	var ids []string
	for _, name := range []string{"first.txt", "second.txt", "third.txt"} {
		resp := c.call("jobs.submit", map[string]any{
			"printer_id": printerID,
			"document":   map[string]any{"filename": name, "mime": "text/plain"},
		})
		if resp.Error != nil {
			t.Fatalf("submit %s: %v", name, resp.Error)
		}
		var opened struct {
			JobID    string `json:"job_id"`
			StreamID uint32 `json:"stream_id"`
		}
		if err := json.Unmarshal(resp.Result, &opened); err != nil {
			t.Fatal(err)
		}
		body := []byte("printer-cycle " + name + "\n")
		c.sendChunk(opened.StreamID, body)
		if resp = c.call("jobs.commit", map[string]any{
			"stream_id": opened.StreamID, "bytes": len(body),
		}); resp.Error != nil {
			t.Fatalf("commit %s: %v", name, resp.Error)
		}
		ids = append(ids, opened.JobID)
	}

	jobs := listJobs(t, c, map[string]any{"all": true, "limit": 3})
	if len(jobs) != 3 {
		t.Fatalf("asked for 3 jobs and got %d", len(jobs))
	}
	if jobs[0].JobID != ids[2] {
		t.Errorf("first listed job is %q, want the newest %q", jobs[0].JobID, ids[2])
	}
}

// Reading everybody's jobs is its own permission: what the household has
// printed is not the same thing as what you have printed.
func TestReadingEverybodysJobsNeedsItsOwnScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsRead, store.ScopeJobsSubmit})

	resp := c.call("jobs.list", map[string]any{"all": true})
	if resp.Error == nil {
		t.Fatal("a connector without jobs.read.all was given everybody's jobs")
	}
	if resp.Error.Code != -32001 {
		t.Errorf("refused with code %d, want scope denied", resp.Error.Code)
	}
}

func TestListingJobsNeedsAScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{})

	if resp := c.call("jobs.list", map[string]any{}); resp.Error == nil {
		t.Fatal("listing jobs was allowed with no scopes")
	}
}

// Cancelling has to stop it in CUPS as well as in the record, or the printer
// keeps going while the interface says it stopped.
func TestCancellingAJobStopsIt(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, outPath := addTestPrinter(t, c)
	_ = os.Remove(outPath)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "cancelled.txt", "mime": "text/plain"},
	})
	if resp.Error != nil {
		t.Fatalf("submit: %v", resp.Error)
	}
	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	if err := json.Unmarshal(resp.Result, &opened); err != nil {
		t.Fatal(err)
	}
	body := []byte("printer-cycle cancel check\n")
	c.sendChunk(opened.StreamID, body)
	if resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID, "bytes": len(body),
	}); resp.Error != nil {
		t.Fatalf("commit: %v", resp.Error)
	}

	resp = c.call("jobs.cancel", map[string]any{"job_id": opened.JobID})
	if resp.Error != nil {
		t.Fatalf("cancel: %v", resp.Error)
	}

	var out struct {
		JobID string `json:"job_id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.JobID != opened.JobID {
		t.Errorf("cancel answered about job %q, want %q", out.JobID, opened.JobID)
	}

	// A job that had already finished reports the state it reached rather than
	// claiming to have been cancelled, because saying otherwise would be a lie
	// about something somebody may have in their hand on paper.
	switch out.State {
	case "cancelled", "done", "failed":
	default:
		t.Errorf("state after cancelling = %q", out.State)
	}

	// And it must stick, whatever it settled on.
	jobs := listJobs(t, c, map[string]any{"all": true})
	for _, j := range jobs {
		if j.JobID != opened.JobID {
			continue
		}
		switch j.State {
		case "cancelled", "done", "failed":
		default:
			t.Errorf("the record says %q after a cancel, so it is still moving", j.State)
		}
	}
}

// Cancelling a job that does not exist must not confirm that it does not exist
// any differently from one belonging to somebody else.
func TestCancellingAnUnknownJobIsRefused(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("jobs.cancel", map[string]any{"job_id": "job_01NOTAREALJOBIDATALL00"})
	if resp.Error == nil {
		t.Fatal("cancelling a job that does not exist succeeded")
	}
	if resp.Error.Code != -32004 {
		t.Errorf("refused with code %d, want unknown job", resp.Error.Code)
	}
}

func TestCancellingNeedsTheCancelScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopeJobsRead})

	if resp := c.call("jobs.cancel", map[string]any{"job_id": "job_1"}); resp.Error == nil {
		t.Fatal("cancelling was allowed without the cancel scope")
	}
}
