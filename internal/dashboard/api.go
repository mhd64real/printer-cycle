package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionCookie carries the core session the browser signed in with.
//
// The cookie holds a core session token, not the dashboard's connector key. The
// distinction is the whole point of this layer: the connector key stays in this
// process, so a browser tab cannot act as the dashboard even if somebody points
// one straight at core.
//
// A stolen cookie is also worth less than it looks. Core binds a session to the
// connector that issued it, so this one is unusable by anything except the
// dashboard.
const sessionCookie = "printer_cycle_session"

// browserMethods is what a browser is allowed to ask core for, through here.
//
// An allowlist rather than a general relay. A relay that passed any method
// through would give a page every power the dashboard connector holds, which is
// all of them, and would make a single scripting flaw equivalent to owning the
// machine. Naming them keeps that bounded and gives each one somewhere to put a
// per-user rule.
var browserMethods = map[string]bool{
	"printers.list":             true,
	"printers.discover":         true,
	"printers.probe":            true,
	"printers.driverCandidates": true,
	"printers.add":              true,
	"printers.remove":           true,

	// jobs.submit and jobs.commit are deliberately absent. They are two ends of
	// a sequence whose middle is binary frames, and this bridge carries JSON
	// only, so a page calling them could open a stream and never feed it: a job
	// stuck half-made until core's sweeper collects it a minute later, and as
	// many of those at once as the page cared to ask for. Printing goes through
	// /api/print, which runs the whole sequence or none of it.

	"connectors.list":            true,
	"connectors.setSetting":      true,
	"connectors.setFallbackUser": true,

	"identity.approve": true,
	"identity.links":   true,
	"identity.revoke":  true,

	"users.list":   true,
	"users.create": true,
}

// methodsNeedingSession are relayed with the signed-in person attached, because
// they act for somebody rather than merely reading.
var methodsNeedingSession = map[string]bool{
	"identity.approve": true,
	"users.create":     true,
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login exchanges a username and password for a session, and puts it in a
// cookie the page cannot read.
func (d *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "that request could not be read")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var out struct {
		Session   string         `json:"session"`
		ExpiresAt string         `json:"expires_at"`
		User      map[string]any `json:"user"`
	}
	err := d.client.Call(ctx, "users.authenticate", map[string]any{
		"username": req.Username,
		"password": req.Password,
	}, &out)
	if err != nil {
		// Whatever went wrong, the page is told the same thing. Core already
		// makes a wrong password and an unknown name indistinguishable, and
		// relaying its wording would undo that.
		d.log.Warn("sign-in refused", "username", req.Username, "error", err)
		writeJSONError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}

	expires, _ := time.Parse(time.RFC3339, out.ExpiresAt)

	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: out.Session,
		Path:  "/",

		// Unreadable from JavaScript, so a scripting flaw cannot lift the
		// session out of the page.
		HttpOnly: true,

		// Not sent on requests originating from another site, which is what
		// stops a page elsewhere printing through somebody's own dashboard.
		SameSite: http.SameSiteStrictMode,

		// Only when the connection is already secure. Setting it unconditionally
		// would break the ordinary case, which is a Raspberry Pi on a home
		// network with no certificate.
		Secure:  r.TLS != nil,
		Expires: expires,
	})

	writeJSON(w, http.StatusOK, map[string]any{"user": out.User})
}

// logout ends the session both here and in core.
func (d *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionFrom(r); token != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		// Ending it in core matters more than clearing the cookie: a cookie
		// already copied elsewhere would otherwise keep working.
		if err := d.client.Call(ctx, "users.signOut", map[string]any{"session": token}, nil); err != nil {
			d.log.Warn("could not end the session in core", "error", err)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})

	writeJSON(w, http.StatusOK, map[string]any{"signed_out": true})
}

// me reports who is signed in, which is how the page decides whether to show a
// login form or an interface.
func (d *Server) me(w http.ResponseWriter, r *http.Request) {
	token := sessionFrom(r)
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var user map[string]any
	if err := d.client.Call(ctx, "users.whoami", map[string]any{"session": token}, &user); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

type callRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// call relays one named method to core on behalf of the signed-in person.
//
// The browser never speaks to core. It asks this process, which holds the
// connector key, checks the method is one a page may ask for, and attaches the
// session so core can decide for itself who is acting.
func (d *Server) call(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := sessionFrom(r)
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	var req callRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "that request could not be read")
		return
	}

	if !browserMethods[req.Method] {
		d.log.Warn("the page asked for a method it may not call", "method", req.Method)
		writeJSONError(w, http.StatusForbidden, "that is not something this page may do")
		return
	}

	params := map[string]any{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONError(w, http.StatusBadRequest, "params must be an object")
			return
		}
	}

	// The session is attached here rather than being sent by the page, so a page
	// cannot act for somebody other than whoever it is signed in as.
	if methodsNeedingSession[req.Method] {
		params["session"] = token
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	var result json.RawMessage
	if err := d.client.Call(ctx, req.Method, params, &result); err != nil {
		d.log.Debug("core refused a relayed call", "method", req.Method, "error", err)
		writeJSONError(w, http.StatusBadRequest, humanise(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// sessionFrom reads the session out of the request cookie.
//
// Only from the cookie. Accepting one from a header or a query string as well
// would hand a page a way to supply its own, which is exactly what this layer
// exists to prevent.
func sessionFrom(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

type setupRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// setup creates the first account, on a box that has none.
//
// Separate from the relay because it is the one thing that happens before
// anybody can sign in, so there is no session to carry. Core refuses it once any
// account exists, which is what keeps it from being a way in later: this
// endpoint is not trusted, it is simply the only shape the first request can
// take.
func (d *Server) setup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req setupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "that request could not be read")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var created map[string]any
	err := d.client.Call(ctx, "users.create", map[string]any{
		"username":     req.Username,
		"display_name": req.DisplayName,
		"password":     req.Password,
	}, &created)
	if err != nil {
		// The reason is the person's to act on: a password too short, a name
		// already taken, or setup already finished.
		writeJSONError(w, http.StatusBadRequest, humanise(err))
		return
	}

	d.log.Info("first account created", "username", req.Username)
	writeJSON(w, http.StatusOK, map[string]any{"user": created})
}

// needsSetup reports whether this box has any accounts, so the page knows
// whether to show a setup form or a sign-in form.
func (d *Server) needsSetup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var out struct {
		Users []any `json:"users"`
	}
	if err := d.client.Call(ctx, "users.list", nil, &out); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "cannot reach core")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": len(out.Users) == 0})
}

// humanise strips the protocol wrapper off an error before a person reads it.
//
// Core's errors arrive as "jsonrpc: -32002 this box already has an account",
// where the first two words are addressed to a program and the rest to a person.
// Showing the whole thing on a screen puts a number in front of a sentence
// somebody was meant to simply read.
func humanise(err error) string {
	message := err.Error()
	message = strings.TrimPrefix(message, "jsonrpc: ")

	// Then the code, if one is there: a minus sign, digits, one space.
	if strings.HasPrefix(message, "-") {
		if i := strings.IndexByte(message, ' '); i > 1 {
			if _, convErr := strconv.Atoi(message[:i]); convErr == nil {
				message = message[i+1:]
			}
		}
	}

	if message == "" {
		return "something went wrong"
	}
	return message
}
