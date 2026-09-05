package protocol

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// jobUpdate is what a connector receives as a job moves.
type jobUpdate struct {
	JobID      string `json:"job_id"`
	PrinterID  string `json:"printer_id"`
	State      string `json:"state"`
	Reasons    string `json:"state_reasons,omitempty"`
	PagesDone  int    `json:"pages_done"`
	PagesTotal int    `json:"pages_total"`

	// Suspect marks a job CUPS called successful that produced nothing. See
	// [Server.finishJob].
	Suspect bool `json:"produced_no_output,omitempty"`
}

// notifyTimeout bounds one delivery, so a connector that has stopped reading
// cannot hold up updates to every other connector.
const notifyTimeout = 5 * time.Second

// watchCUPS keeps core informed about jobs and passes that on.
//
// Runs for the life of the server. Reconnects if the subscription drops, since
// cupsd restarting is ordinary on a machine that also installs driver packages,
// and a print server that silently stops reporting progress after an unrelated
// restart would be worse than one that never reported at all.
func (s *Server) watchCUPS(ctx context.Context) {
	if s.cups == nil {
		return
	}

	backoff := time.Second
	for {
		err := s.cups.Watch(ctx, ipp.WatchOptions{}, func(e ipp.Event) {
			s.handleCUPSEvent(ctx, e)
		})
		if ctx.Err() != nil {
			return
		}
		s.log.Warn("lost touch with the printing system, reconnecting",
			"error", err, "in", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// handleCUPSEvent records what changed and tells whoever submitted the job.
func (s *Server) handleCUPSEvent(ctx context.Context, e ipp.Event) {
	if !e.IsJob() {
		return
	}

	job, err := s.db.JobByCUPSID(ctx, e.JobID)
	if errors.Is(err, store.ErrNotFound) {
		// Somebody printed through CUPS directly, bypassing printer-cycle
		// entirely. Not an error, and not ours to report.
		return
	}
	if err != nil {
		s.log.Error("cannot match a printing event to a job", "cups_job", e.JobID, "error", err)
		return
	}

	state := jobStateName(e.JobState)
	reasons := strings.Join(e.JobStateReasons, " ")
	pagesDone := e.PagesDone
	suspect := false

	if e.JobState.Terminal() {
		state, reasons, pagesDone, suspect = s.finishJob(ctx, job, e)
	}

	update := store.JobUpdate{
		State:        &state,
		StateReasons: &reasons,
		PagesDone:    &pagesDone,
	}
	if err := s.db.UpdateJob(ctx, job.ID, update); err != nil {
		s.log.Error("cannot record a job change", "job", job.ID, "error", err)
	}

	s.notifyConnector(ctx, job.ConnectorID, jobUpdate{
		JobID:      job.ID,
		PrinterID:  job.PrinterID,
		State:      state,
		Reasons:    reasons,
		PagesDone:  pagesDone,
		PagesTotal: job.PagesTotal,
		Suspect:    suspect,
	})
}

// finishJob settles what a terminal event really means.
//
// # Why a completed job is not automatically a success
//
// Measured against CUPS 2.4.10: a job whose format the filter chain cannot
// handle is accepted, reported as job-completed-successfully, and produces no
// output at all. No error, no warning, nothing in the log.
//
// A print server that says a job succeeded when nothing came out is worse than
// one that fails, because the person walks to the printer, finds nothing, and
// has no idea which part to distrust. So a job that carried bytes and produced
// no impressions is reported as failed, and flagged.
//
// Narrow on purpose. A job of zero bytes producing nothing is exactly right, and
// is left alone.
func (s *Server) finishJob(ctx context.Context, job store.Job, e ipp.Event) (state, reasons string, pages int, suspect bool) {
	state = jobStateName(e.JobState)
	reasons = strings.Join(e.JobStateReasons, " ")
	pages = e.PagesDone

	// The event's counters can lag the final ones, so the authoritative numbers
	// are read back before drawing a conclusion from them.
	if final, err := s.jobAttributes(ctx, e.JobID); err == nil {
		if final.PagesDone > pages {
			pages = final.PagesDone
		}
		if len(final.StateReasons) > 0 {
			reasons = strings.Join(final.StateReasons, " ")
		}
	}

	if e.JobState != ipp.JobCompleted {
		return state, reasons, pages, false
	}
	if job.SizeBytes <= 0 || pages > 0 {
		return state, reasons, pages, false
	}

	s.log.Warn("a job CUPS called successful produced no output",
		"job", job.ID, "cups_job", e.JobID, "bytes", job.SizeBytes,
		"format", job.DocumentFormat)

	return "failed", "no-output-produced " + reasons, pages, true
}

// notifyConnector delivers an update to every connection that connector holds.
//
// A connector may legitimately have more than one open, and one that is not
// listening must not delay the others, so each delivery is bounded and failures
// are logged rather than retried. Updates are a stream of current state, not a
// queue of things that must arrive: the next one supersedes the last.
func (s *Server) notifyConnector(ctx context.Context, connectorID string, update jobUpdate) {
	if connectorID == "" {
		return
	}

	targets := s.connectionsFor(connectorID)

	for _, c := range targets {
		sendCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
		err := c.rpc.Notify(sendCtx, "job.updated", update)
		cancel()
		if err != nil {
			c.log.Debug("cannot deliver a job update", "job", update.JobID, "error", err)
		}
	}
}

// jobAttributes reads a job back from CUPS, tolerating there being no CUPS at
// all so the completion logic can be exercised on its own.
func (s *Server) jobAttributes(ctx context.Context, cupsJobID int) (ipp.Job, error) {
	if s.cups == nil {
		return ipp.Job{}, errors.New("protocol: no printing system")
	}
	return s.cups.Job(ctx, cupsJobID)
}
