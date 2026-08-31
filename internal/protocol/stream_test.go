package protocol_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// sendChunk writes one document chunk as a binary frame: the stream id, then
// the bytes.
func (c *client) sendChunk(streamID uint32, payload []byte) {
	c.t.Helper()

	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], streamID)
	copy(frame[4:], payload)

	if err := c.ws.Write(c.ctx, websocket.MessageBinary, frame); err != nil {
		c.t.Fatalf("sending a chunk: %v", err)
	}
}

// addTestPrinterNamed pairs a queue and returns its id and CUPS queue name.
func addTestPrinterNamed(t *testing.T, c *client) (string, string) {
	t.Helper()

	name := uniqueName(t)
	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///var/spool/pc-out/" + sanitise(name) + ".out",
		"name":       name,
		"ppd":        "drv:///sample.drv/generic.ppd",
	})
	if resp.Error != nil {
		t.Fatalf("adding a printer: %v", resp.Error)
	}
	var added struct {
		ID        string `json:"id"`
		QueueName string `json:"queue_name"`
	}
	if err := json.Unmarshal(resp.Result, &added); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeQueue(t, added.QueueName) })
	return added.ID, added.QueueName
}

// addTestPrinter pairs a file-backed queue and returns its id and host path.
func addTestPrinter(t *testing.T, c *client) (string, string) {
	t.Helper()

	name := uniqueName(t)
	resp := c.call("printers.add", map[string]any{
		"device_uri": "file:///var/spool/pc-out/" + sanitise(name) + ".out",
		"name":       name,
		"ppd":        "drv:///sample.drv/generic.ppd",
	})
	if resp.Error != nil {
		t.Fatalf("adding a printer: %v", resp.Error)
	}
	var added struct {
		ID        string `json:"id"`
		QueueName string `json:"queue_name"`
	}
	if err := json.Unmarshal(resp.Result, &added); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeQueue(t, added.QueueName) })

	return added.ID, filepath.Join("../../dev/out", sanitise(name)+".out")
}

func sanitise(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// TestSubmitAndCommitPrintsADocument is the ordinary path: open a stream, send
// the document, commit, and find the bytes on the other side.
func TestSubmitAndCommitPrintsADocument(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, outPath := addTestPrinter(t, c)
	_ = os.Remove(outPath)

	document := []byte("printer-cycle streaming check\n")

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "check.txt", "mime": "text/plain"},
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
	if opened.JobID == "" || opened.StreamID == 0 {
		t.Fatalf("submit returned %+v", opened)
	}

	c.sendChunk(opened.StreamID, document)

	sum := sha256.Sum256(document)
	resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     len(document),
		"sha256":    "hex:" + hex.EncodeToString(sum[:]),
	})
	if resp.Error != nil {
		t.Fatalf("commit: %v", resp.Error)
	}
	var committed struct {
		JobID     string `json:"job_id"`
		CUPSJobID int    `json:"cups_job_id"`
		Bytes     int64  `json:"bytes"`
	}
	if err := json.Unmarshal(resp.Result, &committed); err != nil {
		t.Fatal(err)
	}
	t.Logf("job=%s cups_job=%d bytes=%d", committed.JobID, committed.CUPSJobID, committed.Bytes)

	if committed.CUPSJobID == 0 {
		t.Error("CUPS did not return a job id, so nothing was queued")
	}
	if committed.Bytes != int64(len(document)) {
		t.Errorf("bytes = %d, want %d", committed.Bytes, len(document))
	}

	// The document reached the printer, not merely CUPS.
	waitForOutput(t, outPath, 1024, 30*time.Second)

	// And printer-cycle recorded it.
	job, err := db.Job(ctx(), opened.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.CUPSJobID != committed.CUPSJobID {
		t.Errorf("the record says CUPS job %d, the reply said %d", job.CUPSJobID, committed.CUPSJobID)
	}
	if job.SizeBytes != int64(len(document)) {
		t.Errorf("recorded size = %d", job.SizeBytes)
	}
}

// The memory claim, end to end through the protocol this time rather than
// against the IPP client alone.
func TestALargeDocumentIsNeverResident(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClientWithTimeout(t, url, db, "dashboard", store.KnownScopes(), 5*time.Minute)

	printerID, queue := addTestPrinterNamed(t, c)

	// Paused, so CUPS accepts every byte but never spends CPU rasterising 50MB
	// of it. What is being measured is whether the document is held in memory
	// on the way through, not how fast Ghostscript is.
	if err := cupsClient(t).PausePrinter(ctx(), queue); err != nil {
		t.Fatalf("pausing: %v", err)
	}

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "large.ps", "mime": "application/postscript"},
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

	// The chunk size the specification recommends, so the test exercises what a
	// connector author would actually write.
	const (
		chunkSize = 64 << 10
		chunks    = 800 // 50MB
		total     = chunkSize * chunks
	)

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

	// One reused buffer, so the sender is not what fills the heap.
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte('A' + i%26)
	}
	copy(chunk, []byte("%!PS-Adobe-3.0\n% "))

	for range chunks {
		c.sendChunk(opened.StreamID, chunk)
	}

	resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     total,
	})
	close(stop)
	<-sampled

	if resp.Error != nil {
		t.Fatalf("commit: %v", resp.Error)
	}

	growth := int64(peak) - int64(base.HeapAlloc)
	t.Logf("sent %d MB through the protocol, peak heap growth %.1f MB",
		total>>20, float64(growth)/(1<<20))

	const budget = 24 << 20
	if growth > budget {
		t.Errorf("heap grew %.1f MB carrying a %d MB document; it is being buffered somewhere",
			float64(growth)/(1<<20), total>>20)
	}

	var committed struct {
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal(resp.Result, &committed); err != nil {
		t.Fatal(err)
	}
	if committed.Bytes != int64(total) {
		t.Errorf("core received %d bytes of %d", committed.Bytes, total)
	}
}

// The Stage 17 finding, now guarded against. CUPS 2.4 accepts a job whose format
// it cannot filter, reports it completed successfully, and prints nothing. A
// print server that lies about success is worse than one that fails.
func TestAnUnsupportedFormatIsRefusedRatherThanSilentlyDiscarded(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, _ := addTestPrinter(t, c)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "x.raw", "mime": "application/vnd.cups-raw"},
	})
	if resp.Error == nil {
		t.Fatal("a format the queue cannot filter was accepted, which prints nothing and reports success")
	}
	if resp.Error.Code != jsonrpc.CodePayloadRejected {
		t.Errorf("code = %d, want payload rejected", resp.Error.Code)
	}
	t.Logf("refused: %s", resp.Error.Message)
}

// A truncated upload must never reach the printer. Catching it at commit means
// nothing is printed at all, rather than half a document being cancelled part
// way through.
func TestATruncatedUploadIsRefused(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, _ := addTestPrinter(t, c)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "short.txt", "mime": "text/plain"},
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	json.Unmarshal(resp.Result, &opened)

	c.sendChunk(opened.StreamID, []byte("only part of it"))

	resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     9999,
	})
	if resp.Error == nil {
		t.Fatal("a commit claiming more bytes than arrived was accepted")
	}
	if resp.Error.Code != jsonrpc.CodePayloadRejected {
		t.Errorf("code = %d, want payload rejected", resp.Error.Code)
	}

	job, err := db.Job(ctx(), opened.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" {
		t.Errorf("job state = %q, want failed", job.State)
	}
}

func TestAMismatchedChecksumIsRefused(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	printerID, _ := addTestPrinter(t, c)

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "x.txt", "mime": "text/plain"},
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var opened struct {
		StreamID uint32 `json:"stream_id"`
	}
	json.Unmarshal(resp.Result, &opened)

	doc := []byte("the real document")
	c.sendChunk(opened.StreamID, doc)

	resp = c.call("jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     len(doc),
		"sha256":    "hex:" + hex.EncodeToString(make([]byte, 32)),
	})
	if resp.Error == nil {
		t.Fatal("a commit with the wrong checksum was accepted")
	}
}

func TestCommittingAnUnknownStream(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "dashboard", store.KnownScopes())

	resp := c.call("jobs.commit", map[string]any{"stream_id": 4242})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeUnknownStream {
		t.Errorf("committing a stream that was never opened gave %v", resp.Error)
	}
}

func TestSubmittingNeedsTheJobsSubmitScope(t *testing.T) {
	url, db := cupsBackedServer(t)
	c := authedClient(t, url, db, "telegram", []string{store.ScopePrintersRead})

	resp := c.call("jobs.submit", map[string]any{
		"printer_id": "prn_whatever",
		"document":   map[string]any{"mime": "text/plain"},
	})
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeScopeDenied {
		t.Errorf("submitting without the scope gave %v", resp.Error)
	}
}

func waitForOutput(t *testing.T, path string, minSize int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() >= int64(minSize) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing printed to %s within %v", path, timeout)
}
