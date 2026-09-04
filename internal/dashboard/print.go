package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxDocument is the largest document a browser may send.
//
// Not a protocol limit: core streams a document of any size. This bounds what
// one upload can cost the machine before core has agreed to accept it, and
// 100MB is well past any document a person prints and well short of anything
// that troubles a Pi.
const maxDocument = 100 << 20

// printOptions is everything the page can say about how to print.
//
// Pointers for the tri-state ones: unset means "whatever the printer does by
// default", which is not the same as "off". Sending false for a printer whose
// default is duplex would quietly change what somebody gets.
type printOptions struct {
	Copies int    `json:"copies,omitempty"`
	Duplex *bool  `json:"duplex,omitempty"`
	Color  *bool  `json:"color,omitempty"`
	Media  string `json:"media,omitempty"`
}

// print takes a document from the browser and streams it to core.
//
// The document is never held here, and never written to disk. It goes from the
// upload straight into binary frames, which is the whole reason the protocol
// has them: a browser sending a 40MB PDF must not become 40MB resident in this
// process, in core, and in a temporary file besides.
//
// That is why this reads the multipart body as a stream rather than through
// ParseMultipartForm, which would spool the file before any of it moved. The
// cost is an ordering rule: the file part has to come last, after the fields
// that describe how to print it. The page controls that, and a request that
// breaks it is refused by name rather than printed with silent defaults.
func (d *Server) print(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := sessionFrom(r)
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	parts, err := r.MultipartReader()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "that upload could not be read")
		return
	}

	// Printing a large document over a slow link is ordinary, and the timeout
	// has to cover the transfer as well as CUPS accepting the job at the end.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	var (
		printerID string
		opts      printOptions
	)

	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "no document was attached")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "that upload could not be read")
			return
		}

		if part.FileName() == "" {
			value, err := readField(part)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "that upload could not be read")
				return
			}
			if err := applyField(part.FormName(), value, &printerID, &opts); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			continue
		}

		if printerID == "" {
			writeJSONError(w, http.StatusBadRequest,
				"the document arrived before the printer was named")
			return
		}

		d.streamToPrinter(ctx, w, part, printerID, token, opts)
		return
	}
}

// streamToPrinter runs submit, document, commit as one sequence.
func (d *Server) streamToPrinter(
	ctx context.Context,
	w http.ResponseWriter,
	part *multipart.Part,
	printerID, session string,
	opts printOptions,
) {
	filename := filepath.Base(part.FileName())
	format := documentFormat(part)

	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	err := d.client.Call(ctx, "jobs.submit", map[string]any{
		"session":    session,
		"printer_id": printerID,
		"document": map[string]any{
			"filename": filename,
			"mime":     format,
		},
		"options": opts,
	}, &opened)
	if err != nil {
		d.log.Debug("core refused a print job", "printer", printerID, "error", err)
		writeCoreError(w, err)
		return
	}

	body := io.LimitReader(part, maxDocument+1)
	sent, digest, err := d.client.SendDocument(ctx, opened.StreamID, body)
	if err != nil {
		// The stream is left to core's sweeper. Nothing useful can be said to it
		// on a connection that just failed to carry a chunk.
		d.log.Error("could not send a document to core", "job", opened.JobID, "error", err)
		writeJSONError(w, http.StatusBadGateway, "the document could not be sent to the printer")
		return
	}
	if sent > maxDocument {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "that document is too large to print")
		return
	}

	// Both are sent so core can tell a complete document from a connection that
	// died mid-transfer. Core checks them before anything reaches the printer.
	var done json.RawMessage
	if err := d.client.Call(ctx, "jobs.commit", map[string]any{
		"stream_id": opened.StreamID,
		"bytes":     sent,
		"sha256":    digest,
	}, &done); err != nil {
		d.log.Debug("core refused to finish a print job", "job", opened.JobID, "error", err)
		writeCoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": done})
}

// applyField reads one form field into the request being built.
func applyField(name, value string, printerID *string, opts *printOptions) error {
	switch name {
	case "printer_id":
		*printerID = value
	case "copies":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 999 {
			return fmt.Errorf("copies must be a number between 1 and 999")
		}
		opts.Copies = n
	case "duplex":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("duplex must be true or false")
		}
		opts.Duplex = &b
	case "color":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("colour must be true or false")
		}
		opts.Color = &b
	case "media":
		opts.Media = value
	}
	// An unknown field is ignored rather than refused, so a newer page talking
	// to an older server degrades instead of failing outright.
	return nil
}

// readField reads a form value, bounded, because a field is not a document.
func readField(part *multipart.Part) (string, error) {
	value, err := io.ReadAll(io.LimitReader(part, 4<<10))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

// documentFormat is the MIME type to tell core the document is.
//
// The browser's own Content-Type first, because it knows what the file input
// picked up. Browsers send application/octet-stream for anything they do not
// recognise, which is a statement of ignorance rather than a format, so the
// extension answers those. Core refuses what the queue cannot print, and does
// it before the document goes anywhere.
func documentFormat(part *multipart.Part) string {
	declared := strings.TrimSpace(part.Header.Get("Content-Type"))
	if parsed, _, err := mime.ParseMediaType(declared); err == nil &&
		parsed != "application/octet-stream" && parsed != "" {
		return parsed
	}

	switch strings.ToLower(filepath.Ext(part.FileName())) {
	case ".pdf":
		return "application/pdf"
	case ".ps":
		return "application/postscript"
	case ".txt", ".text", ".log", ".md":
		return "text/plain"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return "application/octet-stream"
}
