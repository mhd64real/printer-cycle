package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// streamIDSize is the length of the identifier prefixing every binary frame.
const streamIDSize = 4

// streamIdle is how long a stream may go without a chunk or a commit before it
// is abandoned.
//
// Without it, a connector that opens a stream and vanishes leaves a pipe, a
// goroutine and a half-submitted job in CUPS forever. On a machine with 512MB,
// a few of those is the whole machine.
const streamIdle = 60 * time.Second

// stream is one document being sent to a printer.
//
// The document is never assembled anywhere. Chunks arrive as binary frames and
// go straight into a pipe that a goroutine is reading from into CUPS, so a 50MB
// scan passes through this process without ever being resident in it.
type stream struct {
	id      uint32
	jobID   string
	printer store.Printer

	writer *io.PipeWriter
	digest hash.Hash

	mu      sync.Mutex
	written int64
	lastAt  time.Time

	// done closes when the CUPS side finishes, whether or not it worked.
	done    chan struct{}
	cupsJob ipp.Job
	err     error
}

type submitParams struct {
	PrinterID  string `json:"printer_id"`
	OnBehalfOf string `json:"on_behalf_of"`
	Document   struct {
		Filename string `json:"filename"`
		MIME     string `json:"mime"`
		Size     int64  `json:"size"`
	} `json:"document"`
	Options struct {
		Copies int    `json:"copies"`
		Duplex *bool  `json:"duplex"`
		Color  *bool  `json:"color"`
		Media  string `json:"media"`
	} `json:"options"`
}

type submitResult struct {
	JobID    string `json:"job_id"`
	StreamID uint32 `json:"stream_id"`
}

// jobsSubmit opens a stream for a document.
//
// Returns immediately with a stream identifier. The document follows as binary
// frames and the job is finished by jobs.commit, which is what keeps a large
// document out of memory on both sides.
func (c *conn) jobsSubmit(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p submitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	printer, err := c.db.Printer(ctx, p.PrinterID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownPrinter, "no such printer")
	}
	if err != nil {
		return nil, err
	}

	format := strings.TrimSpace(p.Document.MIME)
	if err := c.checkFormat(ctx, printer.QueueName, format); err != nil {
		return nil, err
	}

	connector := c.authenticated()

	job, err := c.db.CreateJob(ctx, store.JobSpec{
		PrinterID:      printer.ID,
		ConnectorID:    connector.ID,
		Name:           p.Document.Filename,
		DocumentFormat: format,
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	s := &stream{
		id:      c.nextStreamID(),
		jobID:   job.ID,
		printer: printer,
		writer:  pw,
		digest:  sha256.New(),
		lastAt:  time.Now(),
		done:    make(chan struct{}),
	}

	opts := ipp.PrintOptions{
		JobName:   p.Document.Filename,
		User:      connector.ID,
		Format:    format,
		Copies:    p.Options.Copies,
		Duplex:    p.Options.Duplex,
		ColorMode: p.Options.Color,
		Media:     p.Options.Media,
	}

	// Detached from the request context on purpose: the submit call returns
	// straight away, and this has to outlive it.
	printCtx := context.WithoutCancel(ctx)
	go func() {
		defer close(s.done)
		cupsJob, err := c.server.cups.PrintJob(printCtx, printer.QueueName, pr, opts)
		s.cupsJob, s.err = cupsJob, err
		// Unblocks anything still writing into the pipe if CUPS gave up early,
		// rather than leaving the connection's read loop stuck forever.
		pr.CloseWithError(err)
	}()

	c.addStream(s)
	c.log.Info("stream opened", "job", job.ID, "stream", s.id,
		"printer", printer.QueueName, "format", format)

	return submitResult{JobID: job.ID, StreamID: s.id}, nil
}

// checkFormat refuses a document the queue cannot handle.
//
// Added because of what CUPS 2.4 does otherwise: a job whose format it cannot
// filter is accepted, reported as completed successfully, and prints nothing at
// all. No error, no warning, nothing in the log. A print server that lies about
// success is worse than one that fails, so the refusal happens here.
//
// A queue that reports no supported formats is not blocked. Missing information
// is not the same as a refusal, and failing open matters more than failing safe
// when the alternative is a printer nobody can print to.
func (c *conn) checkFormat(ctx context.Context, queue, format string) error {
	if format == "" {
		return jsonrpc.Errorf(jsonrpc.CodePayloadRejected, "no document format given")
	}

	// Checked before the supported list, because the supported list is not
	// enough on its own: CUPS 2.4 advertises these and then discards jobs sent
	// as them, reporting job-completed-successfully and printing nothing.
	// Measured, not assumed. See the note on silentlyDiscarded.
	if silentlyDiscarded[strings.ToLower(format)] {
		return jsonrpc.Errorf(jsonrpc.CodePayloadRejected,
			"%s is accepted by CUPS and then discarded without printing, so it is refused here", format)
	}

	printer, err := c.server.cups.Printer(ctx, queue)
	if err != nil {
		return c.translateIPP(err, subjectPrinter)
	}
	if len(printer.Formats) == 0 {
		c.log.Warn("queue reports no supported formats, so the document format cannot be checked",
			"queue", queue)
		return nil
	}

	for _, supported := range printer.Formats {
		if strings.EqualFold(supported, format) {
			return nil
		}
	}

	return &jsonrpc.Error{
		Code:    jsonrpc.CodePayloadRejected,
		Message: fmt.Sprintf("%s does not accept %s", queue, format),
		Data:    mustJSON(map[string]any{"supported": printer.Formats}),
	}
}

// silentlyDiscarded are formats CUPS advertises as supported and then throws
// away.
//
// Measured against CUPS 2.4.10: a job submitted as either is accepted, reported
// as job-completed-successfully, and produces no output whatsoever. No error, no
// warning, nothing in the log. Raw printing was removed from CUPS 2.4 without
// the advertised format list being updated to match.
//
// Refusing them is the only way a connector finds out. A print server that says
// a job succeeded when nothing came out is worse than one that fails.
var silentlyDiscarded = map[string]bool{
	"application/vnd.cups-raw": true,
	"application/octet-stream": true,
}

type commitParams struct {
	StreamID uint32 `json:"stream_id"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
}

type commitResult struct {
	JobID     string `json:"job_id"`
	CUPSJobID int    `json:"cups_job_id"`
	State     string `json:"state"`
	Bytes     int64  `json:"bytes"`
}

// jobsCommit finishes a stream and waits for CUPS to accept the job.
func (c *conn) jobsCommit(ctx context.Context, params json.RawMessage) (any, error) {
	var p commitParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	s := c.takeStream(p.StreamID)
	if s == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownStream, "no such stream")
	}

	s.mu.Lock()
	written := s.written
	digest := hex.EncodeToString(s.digest.Sum(nil))
	s.mu.Unlock()

	// Checked before the pipe is closed, so a truncated upload never reaches the
	// printer at all rather than being cancelled halfway through printing.
	if p.Bytes > 0 && p.Bytes != written {
		s.abort(fmt.Errorf("truncated"))
		c.failJob(ctx, s.jobID, "failed")
		return nil, jsonrpc.Errorf(jsonrpc.CodePayloadRejected,
			"received %d bytes, the connector said it sent %d", written, p.Bytes)
	}
	if want := strings.TrimPrefix(p.SHA256, "hex:"); want != "" && !strings.EqualFold(want, digest) {
		s.abort(fmt.Errorf("checksum mismatch"))
		c.failJob(ctx, s.jobID, "failed")
		return nil, jsonrpc.Errorf(jsonrpc.CodePayloadRejected,
			"the document does not match the checksum the connector gave")
	}

	// Closing the writer is what tells CUPS the document has ended.
	if err := s.writer.Close(); err != nil {
		return nil, err
	}

	select {
	case <-s.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if s.err != nil {
		c.failJob(ctx, s.jobID, "failed")
		return nil, c.translateIPP(s.err, subjectJob)
	}

	cupsID := s.cupsJob.ID
	state := s.cupsJob.State.String()
	if err := c.db.UpdateJob(ctx, s.jobID, store.JobUpdate{
		CUPSJobID: &cupsID,
		State:     &state,
		SizeBytes: &written,
	}); err != nil {
		c.log.Error("cannot record an accepted job", "job", s.jobID, "error", err)
	}

	c.log.Info("document delivered", "job", s.jobID, "cups_job", cupsID, "bytes", written)

	return commitResult{JobID: s.jobID, CUPSJobID: cupsID, State: state, Bytes: written}, nil
}

// writeChunk routes one binary frame into its stream.
//
// Called from the connection's read loop, so a slow printer stops core reading
// from this connector, and TCP backpressure carries that all the way to the
// connector's own writes. That is the intended behaviour: a document arrives no
// faster than the printer can take it, and nothing queues up in memory to make
// it look otherwise.
func (c *conn) writeChunk(frame []byte) {
	if len(frame) < streamIDSize {
		c.log.Warn("binary frame is too short to carry a stream id", "bytes", len(frame))
		return
	}

	id := binary.BigEndian.Uint32(frame[:streamIDSize])
	payload := frame[streamIDSize:]

	s := c.findStream(id)
	if s == nil {
		c.log.Warn("binary frame for a stream that is not open", "stream", id)
		return
	}

	if _, err := s.writer.Write(payload); err != nil {
		c.log.Warn("cannot forward a document chunk", "stream", id, "error", err)
		return
	}

	s.mu.Lock()
	s.written += int64(len(payload))
	s.digest.Write(payload)
	s.lastAt = time.Now()
	s.mu.Unlock()
}

func (s *stream) abort(err error) {
	s.writer.CloseWithError(err)
	<-s.done
}

func (c *conn) nextStreamID() uint32 {
	return uint32(c.streamCounter.Add(1))
}

func (c *conn) addStream(s *stream) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.streams == nil {
		c.streams = make(map[uint32]*stream)
	}
	c.streams[s.id] = s
}

func (c *conn) findStream(id uint32) *stream {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	return c.streams[id]
}

// takeStream removes a stream and returns it, so a commit cannot race a second
// commit for the same stream.
func (c *conn) takeStream(id uint32) *stream {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	s := c.streams[id]
	delete(c.streams, id)
	return s
}

// closeStreams abandons everything still open when a connection ends.
//
// A connector that disconnects mid-document leaves a pipe with a goroutine
// blocked on it and a job CUPS is still waiting to receive. Both have to go.
func (c *conn) closeStreams() {
	c.streamMu.Lock()
	open := make([]*stream, 0, len(c.streams))
	for id, s := range c.streams {
		open = append(open, s)
		delete(c.streams, id)
	}
	c.streamMu.Unlock()

	for _, s := range open {
		c.log.Warn("abandoning a document the connector never finished sending",
			"job", s.jobID, "stream", s.id)
		s.writer.CloseWithError(errors.New("the connector disconnected"))
		c.failJob(context.WithoutCancel(context.Background()), s.jobID, "aborted")
	}
}

func (c *conn) failJob(ctx context.Context, jobID, state string) {
	if err := c.db.UpdateJob(ctx, jobID, store.JobUpdate{State: &state}); err != nil {
		c.log.Error("cannot record a failed job", "job", jobID, "error", err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}
