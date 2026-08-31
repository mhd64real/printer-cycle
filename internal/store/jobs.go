package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Job is printer-cycle's own record of something printed.
//
// Kept rather than deferred to CUPS because CUPS forgets completed jobs after a
// while, and a user's own history has to outlive that.
type Job struct {
	ID          string
	CUPSJobID   int
	PrinterID   string
	UserID      string
	ConnectorID string

	Name           string
	DocumentFormat string

	State        string
	StateReasons string

	SizeBytes  int64
	PagesTotal int
	PagesDone  int

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt time.Time
}

// JobSpec describes a job to record.
type JobSpec struct {
	PrinterID      string
	UserID         string
	ConnectorID    string
	Name           string
	DocumentFormat string
}

// CreateJob records a job before anything is sent to CUPS.
//
// Written first on purpose. A job that reached the printer but was never
// recorded is invisible to the person who printed it, which is worse than a
// record for a job that turned out to fail.
func (db *DB) CreateJob(ctx context.Context, spec JobSpec) (Job, error) {
	if spec.PrinterID == "" {
		return Job{}, errors.New("store: job has no printer")
	}

	id := NewID("job")

	var user, connector any
	if spec.UserID != "" {
		user = spec.UserID
	}
	if spec.ConnectorID != "" {
		connector = spec.ConnectorID
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, printer_id, user_id, connector_id, name, document_format, state)
		 VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
		id, spec.PrinterID, user, connector, spec.Name, spec.DocumentFormat)
	if err != nil {
		return Job{}, err
	}
	return db.Job(ctx, id)
}

// JobUpdate carries whatever changed. Nil fields are left alone, so a caller
// reporting progress does not have to know the rest of the row.
type JobUpdate struct {
	CUPSJobID    *int
	State        *string
	StateReasons *string
	SizeBytes    *int64
	PagesTotal   *int
	PagesDone    *int
}

// UpdateJob applies a partial update.
func (db *DB) UpdateJob(ctx context.Context, id string, u JobUpdate) error {
	sets := []string{"updated_at = datetime('now')"}
	args := []any{}

	if u.CUPSJobID != nil {
		sets = append(sets, "cups_job_id = ?")
		args = append(args, *u.CUPSJobID)
	}
	if u.State != nil {
		sets = append(sets, "state = ?")
		args = append(args, *u.State)

		// Terminal states stamp a completion time, so history can be shown in
		// the order things actually finished rather than the order they started.
		if isTerminalState(*u.State) {
			sets = append(sets, "completed_at = datetime('now')")
		}
	}
	if u.StateReasons != nil {
		sets = append(sets, "state_reasons = ?")
		args = append(args, *u.StateReasons)
	}
	if u.SizeBytes != nil {
		sets = append(sets, "size_bytes = ?")
		args = append(args, *u.SizeBytes)
	}
	if u.PagesTotal != nil {
		sets = append(sets, "pages_total = ?")
		args = append(args, *u.PagesTotal)
	}
	if u.PagesDone != nil {
		sets = append(sets, "pages_done = ?")
		args = append(args, *u.PagesDone)
	}

	args = append(args, id)
	res, err := db.ExecContext(ctx,
		`UPDATE jobs SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

func isTerminalState(state string) bool {
	switch state {
	case "completed", "cancelled", "aborted", "failed":
		return true
	}
	return false
}

const jobSelect = `SELECT id, coalesce(cups_job_id, 0), printer_id, coalesce(user_id, ''),
                          coalesce(connector_id, ''), name, document_format, state, state_reasons,
                          size_bytes, pages_total, pages_done,
                          created_at, updated_at, coalesce(completed_at, '')
                     FROM jobs`

// Job returns one job.
func (db *DB) Job(ctx context.Context, id string) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id))
}

// JobByCUPSID finds the job a CUPS job identifier belongs to.
//
// Needed because CUPS reports progress by its own numbering, and events have to
// be matched back to the job printer-cycle knows about.
func (db *DB) JobByCUPSID(ctx context.Context, cupsID int) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, jobSelect+` WHERE cups_job_id = ?`, cupsID))
}

// Jobs lists jobs, newest first. An empty userID lists everybody's.
func (db *DB) Jobs(ctx context.Context, userID string, limit int) ([]Job, error) {
	query := jobSelect
	args := []any{}
	if userID != "" {
		query += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func scanJob(row rowScanner) (Job, error) {
	j, err := scanJobRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

func scanJobRow(row rowScanner) (Job, error) {
	var (
		j                           Job
		created, updated, completed string
	)
	err := row.Scan(&j.ID, &j.CUPSJobID, &j.PrinterID, &j.UserID, &j.ConnectorID,
		&j.Name, &j.DocumentFormat, &j.State, &j.StateReasons,
		&j.SizeBytes, &j.PagesTotal, &j.PagesDone,
		&created, &updated, &completed)
	if err != nil {
		return Job{}, err
	}
	j.CreatedAt = parseTime(created)
	j.UpdatedAt = parseTime(updated)
	if completed != "" {
		j.CompletedAt = parseTime(completed)
	}
	return j, nil
}
