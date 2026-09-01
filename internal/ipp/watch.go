package ipp

import (
	"context"
	"time"

	"github.com/OpenPrinting/goipp"
)

// Event is something CUPS reported through a subscription.
type Event struct {
	// Sequence orders events within a subscription and is how the next request
	// asks only for what it has not seen.
	Sequence int

	// Type is the notify-subscribed-event keyword: job-created,
	// job-state-changed, job-completed, printer-state-changed.
	Type string

	Printer string
	Text    string

	JobID           int
	JobState        JobState
	JobStateReasons []string
	PagesDone       int

	PrinterState        PrinterState
	PrinterStateReasons []string
}

// IsJob reports whether the event concerns a job rather than a printer.
func (e Event) IsJob() bool { return e.JobID != 0 }

// WatchOptions tunes the event loop.
type WatchOptions struct {
	// Printer limits the subscription to one queue. Empty watches everything,
	// which is what core wants: jobs can arrive from any connector, and from
	// somebody using lp on the box directly.
	Printer string

	// ActiveInterval is how often to ask for events while things are happening.
	ActiveInterval time.Duration

	// IdleInterval is how often to ask once nothing has happened for a while.
	IdleInterval time.Duration

	// LeaseDuration is how long CUPS should keep the subscription alive. It is
	// renewed at half this interval, so a missed renewal has a whole period to
	// recover in rather than dropping events immediately.
	LeaseDuration time.Duration
}

func (o *WatchOptions) applyDefaults() {
	if o.ActiveInterval <= 0 {
		o.ActiveInterval = 500 * time.Millisecond
	}
	if o.IdleInterval <= 0 {
		o.IdleInterval = 2 * time.Second
	}
	if o.LeaseDuration <= 0 {
		o.LeaseDuration = time.Hour
	}
}

// watchedEvents is what core needs to keep connectors informed.
var watchedEvents = []string{
	"job-created",
	"job-state-changed",
	"job-completed",
	"printer-state-changed",
	"printer-stopped",
}

// Watch subscribes to CUPS events and calls fn for each one, until ctx ends.
//
// # Why this polls, and why that is still the right design
//
// The intent was for CUPS to tell core when something happened, so core could
// sit idle. CUPS supports IPP subscriptions and generates exactly the right
// events, but measured against CUPS 2.4.10 it does not honour notify-wait:
// Get-Notifications returns immediately whether or not anything is waiting, and
// CUPS advertises notify-get-interval of 60 seconds, meaning it expects clients
// to poll once a minute. A minute is useless for showing somebody their print
// job progressing.
//
// So this asks on an interval it chooses. That is still far better than the
// alternative it replaces: one small request covers every printer and every job
// on the box, where polling job state would mean a request per printer, growing
// with the number of queues. The interval adapts, dropping to IdleInterval once
// nothing has happened, so a quiet box costs one tiny request every couple of
// seconds.
//
// None of this is visible above this layer. Connectors still receive pushed
// job.updated notifications and never poll anything.
func (c *Client) Watch(ctx context.Context, opts WatchOptions, fn func(Event)) error {
	opts.applyDefaults()

	subID, err := c.createSubscription(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		// Best effort. A subscription left behind expires with its lease.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = c.cancelSubscription(cleanup, subID)
	}()

	var (
		lastSeq  int
		quiet    int
		interval = opts.ActiveInterval
		renewAt  = time.Now().Add(opts.LeaseDuration / 2)
	)

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}

		events, err := c.notifications(ctx, subID, lastSeq)
		if err != nil {
			return err
		}

		for _, e := range events {
			if e.Sequence > lastSeq {
				lastSeq = e.Sequence
			}
			fn(e)
		}

		if len(events) > 0 {
			quiet = 0
			interval = opts.ActiveInterval
		} else if quiet++; quiet >= 5 {
			interval = opts.IdleInterval
		}

		if time.Now().After(renewAt) {
			if err := c.renewSubscription(ctx, subID, opts.LeaseDuration); err != nil {
				return err
			}
			renewAt = time.Now().Add(opts.LeaseDuration / 2)
		}

		timer.Reset(interval)
	}
}

func (c *Client) createSubscription(ctx context.Context, opts WatchOptions) (int, error) {
	target := c.RootURI()
	if opts.Printer != "" {
		if err := ValidPrinterName(opts.Printer); err != nil {
			return 0, err
		}
		target = c.PrinterURI(opts.Printer)
	}

	req := c.NewRequest(goipp.OpCreatePrinterSubscriptions)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(target)))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String("printer-cycle")))

	events := goipp.Attribute{Name: "notify-events"}
	for _, e := range watchedEvents {
		events.Values.Add(goipp.TagKeyword, goipp.String(e))
	}

	sub := goipp.Group{Tag: goipp.TagSubscriptionGroup}
	sub.Add(events)
	sub.Add(goipp.MakeAttribute("notify-pull-method", goipp.TagKeyword, goipp.String("ippget")))
	sub.Add(goipp.MakeAttribute("notify-lease-duration", goipp.TagInteger,
		goipp.Integer(int32(opts.LeaseDuration/time.Second))))

	// Groups must be set explicitly: with more than one attribute group, goipp
	// encodes Groups and ignores the named per-group fields.
	req.Groups = goipp.Groups{
		{Tag: goipp.TagOperationGroup, Attrs: req.Operation},
		sub,
	}

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return 0, err
	}
	if err := check(goipp.OpCreatePrinterSubscriptions, resp); err != nil {
		return 0, err
	}

	for _, g := range resp.Groups {
		if g.Tag != goipp.TagSubscriptionGroup {
			continue
		}
		if id, ok := integer(g.Attrs, "notify-subscription-id"); ok {
			return int(id), nil
		}
	}
	return 0, &Error{
		Op:      goipp.OpCreatePrinterSubscriptions,
		Status:  goipp.StatusErrorNotPossible,
		Message: "the subscription was accepted but no id came back",
	}
}

func (c *Client) notifications(ctx context.Context, subID, lastSeq int) ([]Event, error) {
	req := c.NewRequest(goipp.OpGetNotifications)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String("printer-cycle")))
	req.Operation.Add(goipp.MakeAttribute("notify-subscription-ids", goipp.TagInteger, goipp.Integer(int32(subID))))
	if lastSeq > 0 {
		req.Operation.Add(goipp.MakeAttribute("notify-sequence-numbers", goipp.TagInteger,
			goipp.Integer(int32(lastSeq+1))))
	}

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return nil, err
	}
	if err := check(goipp.OpGetNotifications, resp); err != nil {
		return nil, err
	}

	var events []Event
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagEventNotificationGroup {
			continue
		}
		if e, ok := parseEvent(g.Attrs); ok {
			events = append(events, e)
		}
	}
	return events, nil
}

func parseEvent(attrs goipp.Attributes) (Event, bool) {
	kind := str(attrs, "notify-subscribed-event")
	if kind == "" {
		return Event{}, false
	}

	seq, _ := integer(attrs, "notify-sequence-number")
	jobState, _ := integer(attrs, "job-state")

	// The job an event is about arrives as notify-job-id, not job-id.
	//
	// Worth stating plainly because reading the wrong one fails silently: the
	// attribute is simply absent, the identifier comes back as zero, and every
	// job event looks like it concerns no job at all. job-id is tried as well,
	// since RFC 3995 names it that way and not every IPP server is CUPS.
	jobID, ok := integer(attrs, "notify-job-id")
	if !ok {
		jobID, _ = integer(attrs, "job-id")
	}
	pagesDone, _ := integer(attrs, "job-impressions-completed")
	printerState, _ := integer(attrs, "printer-state")

	e := Event{
		Sequence:            int(seq),
		Type:                kind,
		Text:                str(attrs, "notify-text"),
		JobID:               int(jobID),
		JobState:            JobState(jobState),
		JobStateReasons:     strs(attrs, "job-state-reasons"),
		PagesDone:           int(pagesDone),
		PrinterState:        PrinterState(printerState),
		PrinterStateReasons: strs(attrs, "printer-state-reasons"),
	}
	// printer-name is sent directly; the URI is a fallback for servers that
	// send only that.
	e.Printer = str(attrs, "printer-name")
	if e.Printer == "" {
		e.Printer = printerNameFromURI(str(attrs, "notify-printer-uri"))
	}
	return e, true
}

func (c *Client) renewSubscription(ctx context.Context, subID int, lease time.Duration) error {
	req := c.NewRequest(goipp.OpRenewSubscription)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))
	req.Operation.Add(goipp.MakeAttribute("notify-subscription-id", goipp.TagInteger, goipp.Integer(int32(subID))))
	req.Operation.Add(goipp.MakeAttribute("notify-lease-duration", goipp.TagInteger,
		goipp.Integer(int32(lease/time.Second))))

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return err
	}
	return check(goipp.OpRenewSubscription, resp)
}

func (c *Client) cancelSubscription(ctx context.Context, subID int) error {
	req := c.NewRequest(goipp.OpCancelSubscription)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))
	req.Operation.Add(goipp.MakeAttribute("notify-subscription-id", goipp.TagInteger, goipp.Integer(int32(subID))))

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return err
	}
	return check(goipp.OpCancelSubscription, resp)
}
