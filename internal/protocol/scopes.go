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

	"printers.driverCandidates": store.ScopePrintersRead,
	"printers.add":              store.ScopePrintersManage,
	"printers.remove":           store.ScopePrintersManage,

	"jobs.submit": store.ScopeJobsSubmit,
	"jobs.commit": store.ScopeJobsSubmit,

	"identity.resolve":     store.ScopeIdentityLink,
	"identity.linkRequest": store.ScopeIdentityLink,
	"identity.links":       store.ScopeIdentityLink,
	"identity.revoke":      store.ScopeIdentityLink,

	// Approving asserts which user is approving, and core cannot check that
	// claim: user sessions live in the dashboard rather than here. Requiring
	// users.manage means an administrator has already decided this connector may
	// speak for people. See identityApprove.
	"identity.approve": store.ScopeUsersManage,
}

// requiredScope reports the scope a method needs, and whether it exists at all.
func requiredScope(method string) (string, bool) {
	scope, ok := methodScopes[method]
	return scope, ok
}

// authorise checks that this connection may call method.
func (c *conn) authorise(method string) error {
	connector := c.authenticated()
	if connector == nil {
		return jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "authenticate before calling %s", method)
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
