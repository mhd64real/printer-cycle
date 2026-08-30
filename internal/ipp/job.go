package ipp

import (
	"context"
	"fmt"
	"io"
	"net/url"

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
	size, _ := integer(attrs, "job-k-octets")

	return Job{
		ID:           int(id),
		URI:          str(attrs, "job-uri"),
		Name:         str(attrs, "job-name"),
		User:         str(attrs, "job-originating-user-name"),
		State:        JobState(state),
		StateReasons: strs(attrs, "job-state-reasons"),
		PagesTotal:   int(total),
		PagesDone:    int(done),
		SizeBytes:    int(size) * 1024,
	}
}
