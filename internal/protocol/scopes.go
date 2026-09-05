package protocol

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mhd64real/printer-cycle/internal/ipp"
	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// scopeNone marks a method any authenticated connector may call.
const scopeNone = ""

// methodScopes is the permission table, and it is the only place a method
// becomes callable.
//
// Deny by default, and deny by construction: a method absent from here does not
// exist as far as the protocol is concerned, whatever handler somebody wrote.
// Adding a method means deciding what it costs, which is the point.
var methodScopes = map[string]string{
	// A connector describing itself and reading its own configuration needs no
	// permission. It is talking about itself, to a core that already knows who
	// it is.
	"register": scopeNone,

	"users.list":        store.ScopeUsersRead,
	"printers.discover": store.ScopePrintersRead,
	"printers.probe":    store.ScopePrintersRead,
	"printers.list":     store.ScopePrintersRead,

	"printers.drivers":          store.ScopePrintersRead,
	"printers.driverCandidates": store.ScopePrintersRead,
	"printers.add":              store.ScopePrintersManage,
	"printers.remove":           store.ScopePrintersManage,

	"jobs.submit": store.ScopeJobsSubmit,
	"jobs.commit": store.ScopeJobsSubmit,

	// jobs.read is the floor. Asking for everybody's jobs needs jobs.read.all
	// on top, checked in the handler because it depends on what was asked for.
	"jobs.list":   store.ScopeJobsRead,
	"jobs.cancel": store.ScopeJobsCancel,

	"identity.resolve":     store.ScopeIdentityLink,
	"identity.linkRequest": store.ScopeIdentityLink,
	"identity.links":       store.ScopeIdentityLink,
	"identity.revoke":      store.ScopeIdentityLink,

	// Approving now carries the approver's own session, so core checks who it
	// is rather than believing the connector. It therefore needs no more than
	// the scope for taking part in pairing at all.
	"identity.approve": store.ScopeIdentityLink,

	// Hosting a sign-in is its own permission. Listing who has an account and
	// being allowed to try their passwords are different powers.
	"users.authenticate": store.ScopeUsersAuthenticate,
	"users.signOut":      scopeNone,
	"users.create":       store.ScopeUsersManage,
	"users.whoami":       scopeNone,

	// A connector reading its own settings needs no permission: it is asking
	// about itself, of a core that already knows which one it is.
	"settings.get": scopeNone,

	"connectors.list":            store.ScopeConnectorsRead,
	"connectors.setSetting":      store.ScopeConnectorsManage,
	"connectors.setFallbackUser": store.ScopeConnectorsManage,
}

// requiredScope reports the scope a method needs, and whether it exists at all.
func requiredScope(method string) (string, bool) {
	scope, ok := methodScopes[method]
	return scope, ok
}

// authorise checks that this connection may call method.
//
// The connector is read from the database on every call rather than taken from
// the snapshot made when the connection authenticated.
//
// That snapshot is wrong for anything that can change while a connection is
// open, and several things can: an administrator revoking a scope, disabling a
// connector, or a connector re-registering with a different identity policy.
// Deciding from a snapshot means a revoked permission keeps working until the
// connector happens to reconnect, which for something that stays connected for
// weeks means never.
//
// It costs one small query per call against a local SQLite file. Correctness is
// worth more than that here.
func (c *conn) authorise(ctx context.Context, method string) error {
	connector, err := c.currentConnector(ctx)
	if err != nil {
		return err
	}

	scope, exists := requiredScope(method)
	if !exists {
		return jsonrpc.Errorf(jsonrpc.CodeMethodNotFound, "no method %q", method)
	}
	if scope == scopeNone {
		return nil
	}

	for _, held := range connector.Scopes {
		if held == scope {
			return nil
		}
	}

	// Naming the missing scope tells the connector author exactly what to ask an
	// administrator for. It reveals nothing: the caller already knows what it
	// tried to do.
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeScopeDenied,
		Message: "this connector does not hold the scope required by " + method,
		Data:    json.RawMessage(`{"required_scope":` + quote(scope) + `}`),
	}
}

// currentConnector loads this connection's connector as it is now.
//
// Refuses if it has been deleted or switched off since the connection opened, so
// an administrator turning something off does not have to hunt down its
// connection to make that stick.
func (c *conn) currentConnector(ctx context.Context) (*store.Connector, error) {
	snapshot := c.authenticated()
	if snapshot == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "authenticate first")
	}

	current, err := c.db.Connector(ctx, snapshot.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "this connector no longer exists")
	}
	if err != nil {
		return nil, err
	}
	if !current.Enabled {
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "this connector has been disabled")
	}
	return &current, nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// subject is what an operation was about, which is the piece of context IPP
// cannot supply.
type subject int

const (
	subjectPrinter subject = iota
	subjectJob
)

// translateIPP turns an error from the CUPS client into one a connector sees.
//
// This lives here rather than in the ipp package for a reason recorded back at
// Stage 12: IPP reports a missing printer and a missing job with the same
// not-found, while the protocol gives them different codes. Only the caller
// knows which it asked for, and that caller is here.
func (c *conn) translateIPP(err error, about subject) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, ipp.ErrNotFound):
		switch about {
		case subjectPrinter:
			return jsonrpc.Errorf(jsonrpc.CodeUnknownPrinter, "no such printer")
		default:
			return jsonrpc.Errorf(jsonrpc.CodeUnknownJob, "no such job")
		}

	case errors.Is(err, ipp.ErrFormatUnsupported):
		return jsonrpc.Errorf(jsonrpc.CodePayloadRejected,
			"the printer does not accept that document format")

	case errors.Is(err, ipp.ErrNotPossible), errors.Is(err, ipp.ErrConflict):
		return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "the printer refused those options")

	case errors.Is(err, ipp.ErrForbidden), errors.Is(err, ipp.ErrNotAuthenticated),
		errors.Is(err, ipp.ErrNotAuthorized):
		// Deliberately NOT reported as a scope problem. CUPS refusing core is an
		// operator problem, almost always core not being in the lpadmin group,
		// and telling the connector "scope denied" would send its author hunting
		// for a permission they cannot grant and do not need.
		c.log.Error("CUPS refused the operation, which usually means core is not in the lpadmin group",
			"error", err)
		return jsonrpc.Errorf(jsonrpc.CodeInternalError, "internal error")

	case errors.Is(err, ipp.ErrServer):
		c.log.Warn("CUPS reported a server error", "error", err)
		return jsonrpc.Errorf(jsonrpc.CodeInternalError, "the printing system is not answering")
	}

	// Anything else is core's problem to diagnose, not the connector's to read.
	c.log.Error("unexpected error from CUPS", "error", err)
	return jsonrpc.Errorf(jsonrpc.CodeInternalError, "internal error")
}

// usersList returns the accounts on this box.
//
// The first method behind a real scope, so permission checking has something to
// enforce against rather than being tested in the abstract.
func (c *conn) usersList(ctx context.Context) (any, error) {
	users, err := c.db.Users(ctx)
	if err != nil {
		return nil, err
	}

	type userView struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		IsAdmin     bool   `json:"is_admin"`
		CreatedAt   string `json:"created_at"`
	}

	out := make([]userView, 0, len(users))
	for _, u := range users {
		// No password hash, ever. Not because it is directly usable, but
		// because there is no reason for it to leave this process.
		out = append(out, userView{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			IsAdmin:     u.IsAdmin,
			CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	return map[string]any{"users": out}, nil
}

// hasScope reports whether a connector holds a permission.
//
// For the cases where a method's own table entry is only the floor: jobs.list
// is allowed with jobs.read, but asking it for everybody's jobs needs
// jobs.read.all as well, which the table cannot express because it depends on
// what was asked for rather than on which method was called.
func hasScope(connector *store.Connector, scope string) bool {
	for _, held := range connector.Scopes {
		if held == scope {
			return true
		}
	}
	return false
}
