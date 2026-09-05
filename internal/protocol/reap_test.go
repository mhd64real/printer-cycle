package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// A connector that opens a stream and then stops sending must not hold a pipe
// and a blocked goroutine indefinitely.
//
// The connection stays open throughout, which is the case that matters: a
// connector which disconnects is already cleaned up when its connection ends,
// but one that stays connected and simply goes quiet would otherwise hold
// resources until it chose to leave.
func TestAnAbandonedStreamIsCollected(t *testing.T) {
	url, db := cupsBackedServerWithIdle(t, 600*time.Millisecond)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, _ := addTestPrinterNamed(t, c)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "abandoned.txt", "mime": "text/plain"},
	})
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

	c.sendChunk(opened.StreamID, []byte("the beginning of a document"))

	// Then nothing, while the connection stays open.
	deadline := time.Now().Add(15 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		job, err := db.Job(ctx(), opened.JobID)
		if err != nil {
			t.Fatal(err)
		}
		state = job.State
		if state == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if state != "failed" {
		t.Fatalf("job state = %q after going quiet, want failed", state)
	}

	// The stream is gone, so committing it now finds nothing.
	resp = c.call("jobs.commit", map[string]any{"stream_id": opened.StreamID})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeUnknownStream {
		t.Errorf("committing an abandoned stream gave %v, want unknown stream", resp.Error)
	}
}

// Collecting the abandoned must not disturb a stream that is simply slow. A
// connector reading a large file off a slow disk is doing nothing wrong.
func TestASlowButActiveStreamIsLeftAlone(t *testing.T) {
	url, db := cupsBackedServerWithIdle(t, 600*time.Millisecond)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, _ := addTestPrinterNamed(t, c)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "slow.txt", "mime": "text/plain"},
	})
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

	// A chunk every 200ms for two seconds: well inside the idle period each
	// time, but far longer than it in total.
	var sent int
	for range 10 {
		c.sendChunk(opened.StreamID, []byte("still here\n"))
		sent += len("still here\n")
		time.Sleep(200 * time.Millisecond)
	}

	resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     sent,
	})
	if resp.Error != nil {
		t.Fatalf("a slow but active stream was collected: %v", resp.Error)
	}
}
