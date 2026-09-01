package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

type resolveParams struct {
	ExternalID string `json:"external_id"`
}

// identityResolve answers which user an external identity belongs to.
//
// A connector asks this before submitting on somebody's behalf. It can only ask
// about its own namespace: a Telegram connector cannot discover who a Signal
// identity belongs to, because the lookup is scoped to the connector making it.
func (c *conn) identityResolve(ctx context.Context, params json.RawMessage) (any, error) {
	var p resolveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.ExternalID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no external identity given")
	}

	connector := c.authenticated()
	link, err := c.db.ResolveIdentity(ctx, connector.ID, p.ExternalID)
	if errors.Is(err, store.ErrNotLinked) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeIdentityNotLinked, "that identity is not linked to anyone")
	}
	if err != nil {
		return nil, err
	}

	return map[string]any{"user_id": link.UserID, "display": link.Display}, nil
}

type linkRequestParams struct {
	ExternalID string `json:"external_id"`
	Display    string `json:"display"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// identityLinkRequest issues a pairing code.
//
// The connector delivers it however suits: a chat message, a QR code, a link.
// Core owns what the code means and who it binds, so three connectors can offer
// three completely different experiences without any of them keeping a user
// table or inventing a trust model.
func (c *conn) identityLinkRequest(ctx context.Context, params json.RawMessage) (any, error) {
	var p linkRequestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.ExternalID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no external identity given")
	}

	connector := c.authenticated()

	ttl := time.Duration(p.TTLSeconds) * time.Second
	if ttl > time.Hour {
		// A pairing code is a credential in transit. A connector asking for one
		// that lives all day is asking for something it should not have.
		ttl = time.Hour
	}

	code, expires, err := c.db.NewLinkRequest(ctx, connector.ID, p.ExternalID, p.Display, ttl)
	if err != nil {
		return nil, err
	}

	c.log.Info("pairing code issued", "external_id", p.ExternalID, "expires", expires)

	return map[string]any{
		"code":       code,
		"expires_at": expires.Format(time.RFC3339),
	}, nil
}

type approveParams struct {
	Code   string `json:"code"`
	UserID string `json:"user_id"`
}

// identityApprove binds the identity behind a code to a user.
//
// # Why this needs users.manage rather than identity.link
//
// The caller asserts which user is approving, and core has no way to check that
// on its own: user sessions live in the dashboard, not here. So the assertion is
// only as trustworthy as the connector making it, and requiring users.manage
// means an administrator has already decided this connector may speak for
// people. A connector holding only identity.link can ask for codes and resolve
// identities; it cannot decide who anybody is.
//
// This is looser than the design intends and is recorded as such. See the note
// on user sessions in PLAN.md.
func (c *conn) identityApprove(ctx context.Context, params json.RawMessage) (any, error) {
	var p approveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.Code) == "" || strings.TrimSpace(p.UserID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "a code and a user are both required")
	}

	link, err := c.db.ApproveLinkRequest(ctx, p.Code, p.UserID)
	switch {
	case errors.Is(err, store.ErrLinkCodeInvalid):
		c.log.Warn("a pairing code was refused")
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "that pairing code is not valid")
	case errors.Is(err, store.ErrNotFound):
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such user")
	case err != nil:
		return nil, err
	}

	c.log.Info("identity linked",
		"connector", link.ConnectorID, "external_id", link.ExternalID, "user", link.UserID)

	// The connector that asked for the code is told the pairing completed, so it
	// can carry on with whatever it was doing without polling for an answer.
	c.server.notifyIdentityLinked(ctx, link)

	return map[string]any{
		"link_id":     link.ID,
		"user_id":     link.UserID,
		"external_id": link.ExternalID,
	}, nil
}

// identityLinks lists what is linked to an account.
//
// The screen this exists for is the one that answers "what can reach my
// printing, and how do I stop it". Without core owning the bindings there would
// be no such screen, only one per connector, which is the outcome this design
// exists to avoid.
func (c *conn) identityLinks(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		UserID string `json:"user_id"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
		}
	}

	links, err := c.db.IdentityLinks(ctx, p.UserID)
	if err != nil {
		return nil, err
	}

	type view struct {
		ID          string `json:"id"`
		ConnectorID string `json:"connector_id"`
		ExternalID  string `json:"external_id"`
		UserID      string `json:"user_id"`
		Display     string `json:"display"`
		CreatedAt   string `json:"created_at"`
	}
	out := make([]view, 0, len(links))
	for _, l := range links {
		out = append(out, view{
			ID: l.ID, ConnectorID: l.ConnectorID, ExternalID: l.ExternalID,
			UserID: l.UserID, Display: l.Display,
			CreatedAt: l.CreatedAt.Format(time.RFC3339),
		})
	}
	return map[string]any{"links": out}, nil
}

// identityRevoke removes a link.
func (c *conn) identityRevoke(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	if err := c.db.DeleteIdentityLink(ctx, p.ID); errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such link")
	} else if err != nil {
		return nil, err
	}
	return map[string]any{"revoked": p.ID}, nil
}

// notifyIdentityLinked tells a connector that a pairing it started completed.
func (s *Server) notifyIdentityLinked(ctx context.Context, link store.IdentityLink) {
	s.mu.Lock()
	targets := make([]*conn, 0, 2)
	for c := range s.conns {
		if connector := c.authenticated(); connector != nil && connector.ID == link.ConnectorID {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()

	for _, c := range targets {
		sendCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
		err := c.rpc.Notify(sendCtx, "identity.linked", map[string]any{
			"external_id": link.ExternalID,
			"user_id":     link.UserID,
			"display":     link.Display,
		})
		cancel()
		if err != nil {
			c.log.Debug("cannot deliver a pairing notification", "error", err)
		}
	}
}
