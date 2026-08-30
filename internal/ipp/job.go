package ipp

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/OpenPrinting/goipp"
)

// JobState is the IPP job-state enumeration, RFC 8011 section 5.3.7.
type JobState int32

const (
	JobPending           JobState = 3
	JobPendingHeld       JobState = 4
	JobProcessing        JobState = 5
	JobProcessingStopped JobState = 6
	JobCanceled          JobState = 7
	JobAborted           JobState = 8
	JobCompleted         JobState = 9
)

func (s JobState) String() string {
	switch s {
	case JobPending:
		return "pending"
	case JobPendingHeld:
		return "held"
	case JobProcessing:
		return "processing"
	case JobProcessingStopped:
		return "stopped"
	case JobCanceled:
		return "cancelled"
	case JobAborted:
		return "aborted"
	case JobCompleted:
		return "completed"
	default:
		return fmt.Sprintf("unknown(%d)", int32(s))
	}
}

// Terminal reports whether the job has finished moving. Nothing more will happen
// to a job in a terminal state, so watchers can stop watching.
func (s JobState) Terminal() bool {
	return s == JobCanceled || s == JobAborted || s == JobCompleted
}

// Job is a print job in CUPS.
type Job struct {
	ID           int
	URI          string
	Name         string
	Printer      string
	User         string
	State        JobState
	StateMessage string
	StateReasons []string
	PagesTotal   int
	PagesDone    int
	SizeBytes    int
}

// PrintOptions are the per-job settings a caller can ask for.
//
// Duplex and ColorMode are pointers so that "not specified" is distinguishable
// from "explicitly off". A plain bool would make every job that simply omitted
// the field silently force one-sided monochrome, overriding whatever the printer
// or the user had already configured as their default.
type PrintOptions struct {
	// JobName is what the user sees in the queue. Usually the filename.
	JobName string

	// User is requesting-user-name. CUPS uses it for ownership and accounting.
	User string

	// Format is the document MIME type. Empty lets CUPS auto-detect, which it is
	// good at. "application/vnd.cups-raw" bypasses filtering entirely and sends
	// bytes straight to the printer.
	Format string

	// Copies of zero means "do not ask", leaving the printer default.
	Copies int

	Duplex    *bool
	ColorMode *bool

	// Media is a size keyword such as "A4" or "iso_a4_210x297mm". Empty leaves
	// the printer default, which is usually right and is certainly better than
	// guessing the user's paper size.
	Media string
}

// PrintJob sends a document to a queue.
//
// doc is streamed. The encoded IPP message goes out first and the document
// follows it in the same request body, with no length known in advance, so Go
// sends the request chunked and the document is never held in memory. That is
// not a refinement: on a Raspberry Pi Zero 2 W with 512MB shared with cupsd and
// Ghostscript, buffering a scan would be an out-of-memory kill.
//
// The returned Job carries the id and initial state. It does not wait for
// printing to finish; use [Client.Job] or subscriptions for that.
func (c *Client) PrintJob(ctx context.Context, printer string, doc io.Reader, opts PrintOptions) (Job, error) {
	if err := ValidPrinterName(printer); err != nil {
		return Job{}, err
	}
	if doc == nil {
		return Job{}, fmt.Errorf("ipp: print to %q: no document", printer)
	}

	req := c.NewRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.PrinterURI(printer))))

	user := opts.User
	if user == "" {
		user = "printer-cycle"
	}
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String(user)))

	if opts.JobName != "" {
		req.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String(opts.JobName)))
	}
	if opts.Format != "" {
		req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(opts.Format)))
	}

	if opts.Copies > 0 {
		req.Job.Add(goipp.MakeAttribute("copies", goipp.TagInteger, goipp.Integer(int32(opts.Copies))))
	}
	if opts.Duplex != nil {
		sides := "one-sided"
		if *opts.Duplex {
			sides = "two-sided-long-edge"
		}
		req.Job.Add(goipp.MakeAttribute("sides", goipp.TagKeyword, goipp.String(sides)))
	}
	if opts.ColorMode != nil {
		mode := "monochrome"
		if *opts.ColorMode {
			mode = "color"
		}
		req.Job.Add(goipp.MakeAttribute("print-color-mode", goipp.TagKeyword, goipp.String(mode)))
	}
	if opts.Media != "" {
		req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String(opts.Media)))
	}

	resp, err := c.Do(ctx, "/printers/"+url.PathEscape(printer), req, doc)
	if err != nil {
		return Job{}, err
	}
	if err := check(goipp.OpPrintJob, resp); err != nil {
		return Job{}, err
	}

	for _, g := range resp.Groups {
		if g.Tag != goipp.TagJobGroup {
			continue
		}
		job := parseJob(g.Attrs)
		job.Printer = printer
		return job, nil
	}
	return Job{}, &Error{
		Op:      goipp.OpPrintJob,
		Status:  goipp.StatusErrorNotPossible,
		Message: "the server accepted the job but returned no job group",
	}
}

func parseJob(attrs goipp.Attributes) Job {
	id, _ := integer(attrs, "job-id")
	state, _ := integer(attrs, "job-state")
	total, _ := integer(attrs, "job-impressions")
	done, _ := integer(attrs, "job-impressions-completed")
	kOctets, _ := integer(attrs, "job-k-octets")

	job := Job{
		ID:           int(id),
		URI:          str(attrs, "job-uri"),
		Name:         str(attrs, "job-name"),
		User:         str(attrs, "job-originating-user-name"),
		State:        JobState(state),
		StateMessage: str(attrs, "job-state-message"),
		StateReasons: strs(attrs, "job-state-reasons"),
		PagesTotal:   int(total),
		PagesDone:    int(done),

		// job-k-octets counts kibibytes, rounded up, per RFC 8011.
		SizeBytes: int(kOctets) * 1024,
	}

	// CUPS reports the owning queue as a URI. The last path element is the
	// queue name, which is what everything above this layer works in.
	job.Printer = printerNameFromURI(str(attrs, "job-printer-uri"))
	return job
}

// printerNameFromURI pulls the queue name out of a CUPS printer URI.
func printerNameFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	i := strings.LastIndex(uri, "/")
	if i < 0 || i+1 >= len(uri) {
		return ""
	}
	name, err := url.PathUnescape(uri[i+1:])
	if err != nil {
		return ""
	}
	return name
}

// jobFields is what we ask CUPS for about a job.
var jobFields = []string{
	"job-id",
	"job-uri",
	"job-name",
	"job-state",
	"job-state-message",
	"job-state-reasons",
	"job-originating-user-name",
	"job-printer-uri",
	"job-k-octets",
	"job-impressions",
	"job-impressions-completed",
}

// JobURI builds the URI CUPS uses to address one job.
func (c *Client) JobURI(id int) string {
	return fmt.Sprintf("ipp://%s/jobs/%d", c.authority, id)
}

// Job reads one job back.
//
// A job id that does not exist yields an error satisfying errors.Is(err,
// ErrNotFound). Note that CUPS forgets completed jobs after a while, so an id
// that was valid an hour ago may legitimately be gone.
func (c *Client) Job(ctx context.Context, id int) (Job, error) {
	req := c.NewRequest(goipp.OpGetJobAttributes)
	req.Operation.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(c.JobURI(id))))
	req.Operation.Add(requestedAttributes(jobFields...))

	resp, err := c.Do(ctx, fmt.Sprintf("/jobs/%d", id), req, nil)
	if err != nil {
		return Job{}, err
	}
	if err := check(goipp.OpGetJobAttributes, resp); err != nil {
		return Job{}, err
	}

	for _, g := range resp.Groups {
		if g.Tag == goipp.TagJobGroup {
			if job := parseJob(g.Attrs); job.ID != 0 {
				return job, nil
			}
		}
	}
	return Job{}, &Error{
		Op:      goipp.OpGetJobAttributes,
		Status:  goipp.StatusErrorNotFound,
		Message: fmt.Sprintf("no job group in the response for job %d", id),
	}
}

// JobScope selects which jobs [Client.Jobs] returns.
type JobScope string

const (
	JobsNotCompleted JobScope = "not-completed"
	JobsCompleted    JobScope = "completed"
	JobsAll          JobScope = "all"
)

// Jobs lists jobs on a queue, or across every queue when printer is empty.
//
// CUPS keeps completed jobs only for a while and then forgets them, so this is a
// view of recent history rather than a permanent record. printer-cycle keeps its
// own job records for anything that has to outlive that.
func (c *Client) Jobs(ctx context.Context, printer string, scope JobScope, limit int) ([]Job, error) {
	target := c.RootURI()
	path := "/"
	if printer != "" {
		if err := ValidPrinterName(printer); err != nil {
			return nil, err
		}
		target = c.PrinterURI(printer)
		path = "/printers/" + url.PathEscape(printer)
	}
	if scope == "" {
		scope = JobsNotCompleted
	}

	req := c.NewRequest(goipp.OpGetJobs)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(target)))
	req.Operation.Add(goipp.MakeAttribute("which-jobs", goipp.TagKeyword, goipp.String(string(scope))))
	if limit > 0 {
		req.Operation.Add(goipp.MakeAttribute("limit", goipp.TagInteger, goipp.Integer(int32(limit))))
	}
	req.Operation.Add(requestedAttributes(jobFields...))

	resp, err := c.Do(ctx, path, req, nil)
	if err != nil {
		return nil, err
	}
	if err := check(goipp.OpGetJobs, resp); err != nil {
		// No jobs is an empty list, not a failure.
		if errorsIsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	var jobs []Job
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagJobGroup {
			continue
		}
		if job := parseJob(g.Attrs); job.ID != 0 {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

// CancelJob cancels a job, whether it is queued or already printing.
//
// Cancelling a job that has already finished returns an error satisfying
// errors.Is(err, ErrNotPossible), which callers should usually treat as success:
// the user wanted it stopped, and it is stopped.
func (c *Client) CancelJob(ctx context.Context, id int) error {
	req := c.NewRequest(goipp.OpCancelJob)
	req.Operation.Add(goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(c.JobURI(id))))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String("printer-cycle")))

	resp, err := c.Do(ctx, fmt.Sprintf("/jobs/%d", id), req, nil)
	if err != nil {
		return err
	}
	return check(goipp.OpCancelJob, resp)
}

// PausePrinter stops a queue from processing jobs. Jobs already submitted stay
// queued and print when the queue resumes; nothing is lost.
func (c *Client) PausePrinter(ctx context.Context, name string) error {
	return c.printerControl(ctx, goipp.OpPausePrinter, name)
}

// ResumePrinter starts a paused queue processing again.
func (c *Client) ResumePrinter(ctx context.Context, name string) error {
	return c.printerControl(ctx, goipp.OpResumePrinter, name)
}

func (c *Client) printerControl(ctx context.Context, op goipp.Op, name string) error {
	if err := ValidPrinterName(name); err != nil {
		return err
	}

	req := c.NewRequest(op)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.PrinterURI(name))))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String("printer-cycle")))

	resp, err := c.Do(ctx, "/admin/", req, nil)
	if err != nil {
		return err
	}
	return check(op, resp)
}
