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
	Code    string `json:"code"`
	Session string `json:"session"`
}

// identityApprove binds the identity behind a code to the person approving it.
//
// The approver is identified by their session, not by a connector naming them.
// That distinction is the whole point: a connector cannot decide who anybody is,
// it can only pass along a session the person obtained by signing in.
//
// An earlier version took a user id on trust and required users.manage to make
// that trust an explicit administrative decision. It worked and it was still a
// connector's word for who somebody was.
func (c *conn) identityApprove(ctx context.Context, params json.RawMessage) (any, error) {
	var p approveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.Code) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no pairing code given")
	}

	user, err := c.userFromSession(ctx, p.Session)
	if err != nil {
		return nil, err
	}

	link, err := c.db.ApproveLinkRequest(ctx, p.Code, user.ID)
	switch {
	case errors.Is(err, store.ErrLinkCodeInvalid):
		c.log.Warn("a pairing code was refused", "user", user.Username)
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "that pairing code is not valid")
	case err != nil:
		return nil, err
	}

	c.log.Info("identity linked",
		"connector", link.ConnectorID, "external_id", link.ExternalID, "user", user.Username)

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
		UserID  string `json:"user_id"`
		Session string `json:"session"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
		}
	}

	links, err := c.linksVisibleTo(ctx, p.Session, p.UserID)
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

// linksVisibleTo answers who is allowed to see which links.
//
// The scope alone used to decide this, and the scope is held by every chat
// connector that pairs anybody at all. So a Telegram connector could list every
// external identity on the box by omitting the user id, and unlink somebody's
// Signal account by guessing at nothing more than a row id. Both of those are
// well outside what "may take part in pairing" should buy.
//
// Three cases, each with a real use:
//
//   - With a session, a person sees their own links, whatever connector made
//     them. This is the screen that answers "what can reach my printing".
//   - With a session belonging to an administrator, they may name somebody else
//     or ask for all of them, because somebody has to be able to.
//   - With no session, a connector sees the links it made itself, which is how
//     it knows who it can act for, and nothing else.
func (c *conn) linksVisibleTo(ctx context.Context, session, wantUser string) ([]store.IdentityLink, error) {
	self := c.authenticated()

	if strings.TrimSpace(session) == "" {
		if wantUser != "" {
			return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated,
				"reading somebody's links needs their session")
		}
		links, err := c.db.IdentityLinks(ctx, "")
		if err != nil {
			return nil, err
		}
		mine := links[:0]
		for _, l := range links {
			if l.ConnectorID == self.ID {
				mine = append(mine, l)
			}
		}
		return mine, nil
	}

	actor, err := c.userFromSession(ctx, session)
	if err != nil {
		return nil, err
	}

	if wantUser == "" || wantUser == actor.ID {
		return c.db.IdentityLinks(ctx, actor.ID)
	}
	if !actor.IsAdmin {
		return nil, jsonrpc.Errorf(jsonrpc.CodeScopeDenied,
			"only an administrator can see somebody else's links")
	}
	if wantUser == allUsers {
		return c.db.IdentityLinks(ctx, "")
	}
	return c.db.IdentityLinks(ctx, wantUser)
}

// allUsers is what an administrator passes as user_id to mean everybody.
//
// A word rather than an empty string, because empty already means "whoever is
// asking" and a caller that forgot to fill the field in should get its own
// links rather than the whole machine's.
const allUsers = "*"

// identityRevoke removes a link.
//
// Same rule as listing: your own, or anybody's if you are an administrator, or
// the calling connector's own when no person is involved. Refusing with "no
// such link" rather than a denial, so that trying ids is not a way to learn
// which ones exist.
func (c *conn) identityRevoke(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ID      string `json:"id"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	link, err := c.db.IdentityLink(ctx, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such link")
	}
	if err != nil {
		return nil, err
	}

	if err := c.mayRevoke(ctx, p.Session, link); err != nil {
		return nil, err
	}

	if err := c.db.DeleteIdentityLink(ctx, p.ID); errors.Is(err, store.ErrNotFound) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such link")
	} else if err != nil {
		return nil, err
	}

	c.log.Info("identity link revoked", "link", p.ID, "connector", link.ConnectorID)
	return map[string]any{"revoked": p.ID}, nil
}

func (c *conn) mayRevoke(ctx context.Context, session string, link store.IdentityLink) error {
	if strings.TrimSpace(session) == "" {
		if link.ConnectorID == c.authenticated().ID {
			return nil
		}
		return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such link")
	}

	actor, err := c.userFromSession(ctx, session)
	if err != nil {
		return err
	}
	if actor.ID == link.UserID || actor.IsAdmin {
		return nil
	}
	return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such link")
}

// notifyIdentityLinked tells a connector that a pairing it started completed.
func (s *Server) notifyIdentityLinked(ctx context.Context, link store.IdentityLink) {
	for _, c := range s.connectionsFor(link.ConnectorID) {
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
