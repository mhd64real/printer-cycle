package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/store"
)

// The stage's done-when: a connector is told how its job is going without ever
// asking, and the updates arrive in the order the job actually moved.
func TestAConnectorIsToldHowItsJobIsGoing(t *testing.T) {
	url, db := watchingServer(t)
	// A long deadline: the point is to wait for updates nobody asked for, and
	// the client's own context bounds every read on the connection.
	c := authedClientWithTimeout(t, url, db, "dashboard", store.KnownScopes(), 2*time.Minute)

	printerID, _ := addTestPrinterNamed(t, c)

	type update struct {
		JobID     string `json:"job_id"`
		State     string `json:"state"`
		PagesDone int    `json:"pages_done"`
		Suspect   bool   `json:"produced_no_output"`
	}
	var seen []update

	collect := func(method string, params json.RawMessage) {
		if method != "job.updated" {
			return
		}
		var u update
		if err := json.Unmarshal(params, &u); err == nil {
			seen = append(seen, u)
		}
	}

	document := []byte("printer-cycle progress check\n")

	resp := c.callCollecting("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "progress.txt", "mime": "text/plain"},
	}, collect)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	if err := json.Unmarshal(resp.Result, &opened); err != nil {
		t.Fatal(err)
	}

	c.sendChunk(opened.StreamID, document)

	resp = c.callCollecting("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     len(document),
	}, collect)
	if resp.Error != nil {
		t.Fatalf("commit: %v", resp.Error)
	}

	// Nothing was asked for from here on. Whatever arrives, arrives because core
	// decided the connector should know.
	c.awaitNotification(func(method string, params json.RawMessage) bool {
		if method != "job.updated" {
			return false
		}
		var u update
		if err := json.Unmarshal(params, &u); err != nil {
			return false
		}
		seen = append(seen, u)
		return u.State == "completed" || u.State == "failed" || u.State == "aborted"
	}, 60*time.Second)

	for _, u := range seen {
		t.Logf("job.updated  state=%-11s pages=%d suspect=%v", u.State, u.PagesDone, u.Suspect)
	}
	if len(seen) == 0 {
		t.Fatal("no job updates arrived at all")
	}

	for _, u := range seen {
		if u.JobID != opened.JobID {
			t.Errorf("an update arrived for job %q, this connector submitted %q", u.JobID, opened.JobID)
		}
	}

	last := seen[len(seen)-1]
	if last.State != "completed" {
		t.Errorf("final state = %q, want completed", last.State)
	}
	if last.Suspect {
		t.Error("a job that printed was flagged as producing no output")
	}

	// Updates are a record of movement, so a state must never go backwards.
	rank := map[string]int{"pending": 1, "held": 2, "processing": 3, "completed": 4, "failed": 4, "aborted": 4, "cancelled": 4}
	highest := 0
	for _, u := range seen {
		r := rank[u.State]
		if r < highest {
			t.Errorf("state went backwards to %q after reaching rank %d", u.State, highest)
		}
		if r > highest {
			highest = r
		}
	}

	// And the database agrees with what the connector was told.
	job, err := db.Job(ctx(), opened.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != last.State {
		t.Errorf("the record says %q, the connector was told %q", job.State, last.State)
	}
	if job.CompletedAt.IsZero() {
		t.Error("a finished job has no completion time recorded")
	}
}

// A job somebody printed through CUPS directly, bypassing printer-cycle, is not
// ours to report and must not produce updates or errors.
func TestJobsPrintedOutsidePrinterCycleAreIgnored(t *testing.T) {
	url, db := watchingServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())
	_ = c

	// file-ps belongs to the development environment, not to printer-cycle, so
	// nothing here has a record of it.
	cups := cupsClient(t)
	if _, err := cups.PrintJob(ctx(), "file-ps", stringsReader("printed directly\n"),
		ippPrintOptions()); err != nil {
		t.Skipf("cannot print directly: %v", err)
	}

	// Give the watcher time to see it and decide to do nothing.
	time.Sleep(3 * time.Second)

	jobs, err := db.Jobs(ctx(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Errorf("printer-cycle recorded %d jobs it never submitted", len(jobs))
	}
}
