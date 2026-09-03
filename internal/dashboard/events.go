package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Server-sent events, so the page learns about things nobody asked for.
//
// Discovery announces printers as they are found and jobs report their own
// progress, and both are useless if the page can only ask. SSE rather than a
// second WebSocket: it is one-directional, which is exactly what this is, it
// travels over the connection the page already has, and a browser reconnects it
// without being told to.
type subscriber struct {
	events chan sseEvent
}

type sseEvent struct {
	Name string
	Data json.RawMessage
}

// subscriberBuffer is how far behind a page may fall before it stops being sent
// things.
//
// A page that is not reading must not hold up the others, and these are updates
// rather than instructions: the next one supersedes the last, so dropping a few
// for somebody whose laptop is asleep costs nothing worth keeping.
const subscriberBuffer = 32

// events streams what core says to a page.
func (d *Server) events(w http.ResponseWriter, r *http.Request) {
	if sessionFrom(r) == "" {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming is not available here")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Told explicitly, because a proxy that buffers this turns a live stream into
	// a page that updates once, at the end, for reasons nobody can see.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := &subscriber{events: make(chan sseEvent, subscriberBuffer)}
	d.addSubscriber(sub)
	defer d.removeSubscriber(sub)

	// A comment line every so often, so an idle connection is not closed by
	// something in the middle that considers silence a fault.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()

		case event := <-sub.events:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, event.Data)
			flusher.Flush()
		}
	}
}

// HandleNotification passes something core said on to every open page.
//
// Called from the connector client's read loop, so it must not block. A page
// that has fallen behind is skipped rather than waited for.
func (d *Server) HandleNotification(method string, params json.RawMessage) {
	d.subsMu.RLock()
	subs := make([]*subscriber, 0, len(d.subs))
	for sub := range d.subs {
		subs = append(subs, sub)
	}
	d.subsMu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.events <- sseEvent{Name: method, Data: params}:
		default:
			// Behind, so skipped. See subscriberBuffer.
			d.log.Debug("a page is not keeping up with events", "event", method)
		}
	}
}

func (d *Server) addSubscriber(sub *subscriber) {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	if d.subs == nil {
		d.subs = make(map[*subscriber]struct{})
	}
	d.subs[sub] = struct{}{}
}

func (d *Server) removeSubscriber(sub *subscriber) {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	delete(d.subs, sub)
}
