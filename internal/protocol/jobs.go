package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// jobsLimit bounds how much history one call can ask for.
//
// A print queue is not an archive. Somebody looking at a jobs page wants to
// know what is happening now and what happened recently, and a connector that
// asked for everything would be asking a Raspberry Pi to build a reply covering
// every document ever printed on it.
const (
	jobsLimitDefault = 50
	jobsLimitMax     = 500
)

type jobsListParams struct {
	// Session and OnBehalfOf say whose jobs these are, the same way they do for
	// jobs.submit. A connector cannot name a user directly here either.
	Session    string `json:"session"`
	OnBehalfOf string `json:"on_behalf_of"`

	// All asks for every job on the box rather than one person's. Needs
	// jobs.read.all, which is a separate scope precisely because seeing what
	// the household has been printing is a different power from seeing your
	// own.
	All bool `json:"all"`

	Limit int `json:"limit"`
}

// jobView is a job as a connector sees it.
//
// It carries the same fields as a job.updated notification plus the ones that
// do not change, so a page can render the list and then apply updates to it
// without having to ask again.
type jobView struct {
	JobID     string `json:"job_id"`
	PrinterID string `json:"printer_id"`
	UserID    string `json:"user_id"`

	Name   string `json:"name"`
	Format string `json:"document_format,omitempty"`

	State   string `json:"state"`
	Reasons string `json:"state_reasons,omitempty"`

	SizeBytes  int64 `json:"size_bytes"`
	PagesDone  int   `json:"pages_done"`
	PagesTotal int   `json:"pages_total"`

	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

func viewOfJob(j store.Job) jobView {
	view := jobView{
		JobID:      j.ID,
		PrinterID:  j.PrinterID,
		UserID:     j.UserID,
		Name:       j.Name,
		Format:     j.DocumentFormat,
		State:      j.State,
		Reasons:    j.StateReasons,
		SizeBytes:  j.SizeBytes,
		PagesDone:  j.PagesDone,
		PagesTotal: j.PagesTotal,
		CreatedAt:  j.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !j.CompletedAt.IsZero() {
		view.CompletedAt = j.CompletedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// jobsList answers what has been printed, and what is printing now.
func (c *conn) jobsList(ctx context.Context, params json.RawMessage) (any, error) {
	var p jobsListParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
		}
	}

	connector, err := c.currentConnector(ctx)
	if err != nil {
		return nil, err
	}

	limit := p.Limit
	if limit <= 0 {
		limit = jobsLimitDefault
	}
	limit = min(limit, jobsLimitMax)

	// Empty means every user, which is why it is only reachable with the scope
	// for it.
	userID := ""
	if !p.All {
		userID, err = c.attribute(ctx, connector, p.Session, p.OnBehalfOf)
		if err != nil {
			return nil, err
		}
	} else if !hasScope(connector, store.ScopeJobsReadAll) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeScopeDenied,
			"reading everybody's jobs needs the jobs.read.all scope")
	}

	jobs, err := c.db.Jobs(ctx, userID, limit)
	if err != nil {
		return nil, err
	}

	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, viewOfJob(j))
	}
	return map[string]any{"jobs": out}, nil
}

type jobsCancelParams struct {
	JobID      string `json:"job_id"`
	Session    string `json:"session"`
	OnBehalfOf string `json:"on_behalf_of"`
}

// jobsCancel stops a job, in CUPS and in the record.
//
// Cancelling somebody else's job needs jobs.read.all as well as jobs.cancel: a
// connector that cannot be told a job exists should not be able to stop it, and
// the two scopes together are what "acts for the whole box" means.
func (c *conn) jobsCancel(ctx context.Context, params json.RawMessage) (any, error) {
	if c.server.cups == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInternalError, "no connection to the printing system")
	}

	var p jobsCancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.JobID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no job id given")
	}

	connector, err := c.currentConnector(ctx)
	if err != nil {
		return nil, err
	}

	job, err := c.db.Job(ctx, p.JobID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownJob, "no such job")
	}
	if err != nil {
		return nil, err
	}

	if !hasScope(connector, store.ScopeJobsReadAll) {
		asker, err := c.attribute(ctx, connector, p.Session, p.OnBehalfOf)
		if err != nil {
			return nil, err
		}
		// Reported as not found rather than refused, so that asking about a job
		// is not a way to learn that it exists.
		if job.UserID != asker {
			return nil, jsonrpc.Errorf(jsonrpc.CodeUnknownJob, "no such job")
		}
	}

	// Already finished is not a failure. Somebody pressing cancel on a job that
	// completed a moment earlier wanted it stopped, and it is stopped.
	if isFinished(job.State) {
		return map[string]any{"job_id": job.ID, "state": job.State}, nil
	}

	// A job with no CUPS id never reached the printing system, so there is
	// nothing there to cancel and the record is the whole of it.
	if job.CUPSJobID != 0 {
		if err := c.server.cups.CancelJob(ctx, job.CUPSJobID); err != nil {
			return nil, c.translateIPP(err, subjectJob)
		}
	}

	cancelled := "cancelled"
	if err := c.db.UpdateJob(ctx, job.ID, store.JobUpdate{State: &cancelled}); err != nil {
		return nil, err
	}

	c.log.Info("job cancelled", "job", job.ID, "connector", connector.ID)
	return map[string]any{"job_id": job.ID, "state": "cancelled"}, nil
}

// isFinished reports whether a job has stopped moving.
func isFinished(state string) bool {
	switch state {
	case "done", "failed", "cancelled":
		return true
	}
	return false
}

// jobStateName is the one place IPP's job states become printer-cycle's.
//
// It exists because they were not being translated at all. Core wrote
// `ipp.JobState.String()` straight into the record and into every job.updated
// notification, so connectors were told "processing", "completed" and "aborted"
// while PROTOCOL.md promised "printing", "done" and "failed". A connector author
// switching on the documented names matched nothing, and the states core wrote
// by hand for its own failures used the documented vocabulary, so the two were
// mixed inside a single function.
//
// The documented vocabulary wins, minus one word. "rendering" was in the draft
// and is gone: CUPS reports processing while a filter runs and while paper
// moves, with nothing to tell them apart, so it could only ever have been
// guessed at. "held" and "stopped" are here instead, because they are real,
// reachable, and mean something different to somebody looking at a queue.
func jobStateName(state ipp.JobState) string {
	switch state {
	case ipp.JobPending:
		return "queued"
	case ipp.JobPendingHeld:
		return "held"
	case ipp.JobProcessing:
		return "printing"
	case ipp.JobProcessingStopped:
		return "stopped"
	case ipp.JobCompleted:
		return "done"
	case ipp.JobAborted:
		return "failed"
	case ipp.JobCanceled:
		return "cancelled"
	}
	// A state no version of IPP this was written against defines. Reported
	// rather than hidden behind one of the above, because a job in an unknown
	// state is not a job anybody should be told is fine.
	return "unknown"
}
