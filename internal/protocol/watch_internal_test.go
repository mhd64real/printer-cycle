package protocol

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/store"
)

func silentServer() *Server {
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// CUPS 2.4 reports a job as completed successfully when its filter chain could
// not handle the format and nothing was printed at all. A print server that says
// a job succeeded when nothing came out is worse than one that fails: the person
// walks to the printer, finds nothing, and has no idea which part to distrust.
func TestAJobThatPrintedNothingIsNotReportedAsSuccess(t *testing.T) {
	s := silentServer()

	job := store.Job{ID: "job_1", SizeBytes: 4096}
	event := ipp.Event{
		JobID:           7,
		JobState:        ipp.JobCompleted,
		JobStateReasons: []string{"job-completed-successfully"},
		PagesDone:       0,
	}

	state, reasons, pages, suspect := s.finishJob(context.Background(), job, event)

	if state != "failed" {
		t.Errorf("state = %q, want failed: CUPS called it a success and printed nothing", state)
	}
	if !suspect {
		t.Error("the job was not flagged as having produced no output")
	}
	if pages != 0 {
		t.Errorf("pages = %d", pages)
	}
	// The reason CUPS gave is kept alongside ours, since it is what somebody
	// reading a log will search for.
	if reasons == "" {
		t.Error("no state reasons at all")
	}
}

// A job that actually printed is left alone.
func TestAJobThatPrintedIsReportedAsDone(t *testing.T) {
	s := silentServer()

	job := store.Job{ID: "job_1", SizeBytes: 4096}
	event := ipp.Event{
		JobID:     7,
		JobState:  ipp.JobCompleted,
		PagesDone: 3,
	}

	state, _, pages, suspect := s.finishJob(context.Background(), job, event)
	if state != "done" {
		t.Errorf("state = %q, want done", state)
	}
	if suspect {
		t.Error("a job that printed three pages was flagged as producing nothing")
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
}

// An empty document producing nothing is exactly right, and must not be flagged.
// The check exists to catch documents that were sent and vanished, not documents
// that were never there.
func TestAnEmptyDocumentIsNotFlagged(t *testing.T) {
	s := silentServer()

	job := store.Job{ID: "job_1", SizeBytes: 0}
	event := ipp.Event{JobID: 7, JobState: ipp.JobCompleted, PagesDone: 0}

	state, _, _, suspect := s.finishJob(context.Background(), job, event)
	if suspect {
		t.Error("a zero-byte job producing no pages was flagged")
	}
	if state != "done" {
		t.Errorf("state = %q, want done", state)
	}
}

// Cancelling and aborting are already failures and are reported as they are.
func TestNonCompletedTerminalStatesPassThrough(t *testing.T) {
	s := silentServer()

	for _, tc := range []struct {
		state ipp.JobState
		want  string
	}{
		{ipp.JobCanceled, "cancelled"},
		{ipp.JobAborted, "failed"},
	} {
		job := store.Job{ID: "job_1", SizeBytes: 4096}
		event := ipp.Event{JobID: 7, JobState: tc.state, PagesDone: 0}

		state, _, _, suspect := s.finishJob(context.Background(), job, event)
		if state != tc.want {
			t.Errorf("state = %q, want %q", state, tc.want)
		}
		if suspect {
			t.Errorf("%s was flagged as producing no output, which it plainly did not claim to", tc.want)
		}
	}
}
