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

// settingsGet returns a connector's own settings.
//
// No scope, because a connector is asking about itself and core already knows
// which one it is. Secrets are included: a connector that cannot read back its
// own bot token cannot talk to Telegram.
func (c *conn) settingsGet(ctx context.Context) (any, error) {
	connector := c.authenticated()

	settings, err := c.db.SettingsFor(ctx, connector.ID, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"settings": settings}, nil
}

// connectorView is a connector as somebody administering the box sees it.
type connectorView struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Version        string               `json:"version"`
	Description    string               `json:"description"`
	IdentityPolicy string               `json:"identity"`
	Enabled        bool                 `json:"enabled"`
	Enrolled       bool                 `json:"enrolled"`
	Connected      bool                 `json:"connected"`
	Scopes         []string             `json:"scopes"`
	SettingsSchema store.SettingsSchema `json:"settings_schema"`
	Settings       map[string]any       `json:"settings"`
}

// connectorsList is what the dashboard renders its connectors page from.
//
// The settings schema travels with each connector, so the dashboard can draw a
// settings page for something nobody had written when the dashboard was built.
// That is the whole reason a connector declares its settings rather than
// shipping UI: a connector is a separate process and cannot inject anything.
//
// Secret values are not included. The dashboard can see that one is set and can
// replace it, and never reads it back, so a token typed in last year cannot be
// recovered by whoever has a browser open today.
func (c *conn) connectorsList(ctx context.Context) (any, error) {
	connectors, err := c.db.Connectors(ctx)
	if err != nil {
		return nil, err
	}

	connected := c.server.connectedIDs()

	out := make([]connectorView, 0, len(connectors))
	for _, connector := range connectors {
		schema, err := c.db.SettingsSchemaOf(ctx, connector.ID)
		if err != nil {
			return nil, err
		}
		settings, err := c.db.SettingsFor(ctx, connector.ID, false)
		if err != nil {
			return nil, err
		}
		if schema == nil {
			schema = store.SettingsSchema{}
		}
		scopes := connector.Scopes
		if scopes == nil {
			scopes = []string{}
		}

		out = append(out, connectorView{
			ID:             connector.ID,
			Name:           connector.Name,
			Version:        connector.Version,
			Description:    connector.Description,
			IdentityPolicy: string(connector.IdentityPolicy),
			Enabled:        connector.Enabled,
			Enrolled:       connector.Enrolled(),
			Connected:      connected[connector.ID],
			Scopes:         scopes,
			SettingsSchema: schema,
			Settings:       settings,
		})
	}
	// The scope catalogue travels with the list, so an interface offering to
	// invite a connector does not have to keep its own copy of what a scope is
	// called. One source of truth, and it is the one that enforces them.
	return map[string]any{"connectors": out, "known_scopes": store.KnownScopes()}, nil
}

type setSettingParams struct {
	ConnectorID string `json:"connector_id"`
	Key         string `json:"key"`
	Value       any    `json:"value"`
}

// connectorsSetSetting changes one setting and tells the connector at once.
//
// The point of the notification is that a connector does not have to be
// restarted to pick up a change. Somebody correcting a mistyped bot token
// expects it to start working, not to go and restart something.
func (c *conn) connectorsSetSetting(ctx context.Context, params json.RawMessage) (any, error) {
	var p setSettingParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.ConnectorID) == "" || strings.TrimSpace(p.Key) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "a connector and a key are both required")
	}

	if err := c.db.SetSetting(ctx, p.ConnectorID, p.Key, p.Value); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such connector")
		}
		// The reason is the caller's to act on: a value out of range, a key the
		// connector never declared, the wrong type. Saying so helps whoever is
		// filling in the form and tells an attacker nothing.
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s", err)
	}

	c.log.Info("setting changed", "connector", p.ConnectorID, "key", p.Key)

	c.server.notifySettingsChanged(ctx, p.ConnectorID)

	// Answered without the new value. For a secret there would be nothing to
	// send back anyway, and returning it for the others would make one response
	// shaped differently depending on what was written.
	return map[string]any{"connector_id": p.ConnectorID, "key": p.Key}, nil
}

// notifySettingsChanged pushes a connector its current settings.
//
// The values go with the notification rather than telling it to come and ask,
// because the connector would only turn round and call settings.get, and this
// is one message instead of two.
func (s *Server) notifySettingsChanged(ctx context.Context, connectorID string) {
	targets := s.connectionsFor(connectorID)
	if len(targets) == 0 {
		return
	}

	settings, err := s.db.SettingsFor(ctx, connectorID, true)
	if err != nil {
		s.log.Error("cannot read settings to announce them", "connector", connectorID, "error", err)
		return
	}

	for _, target := range targets {
		sendCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
		err := target.rpc.Notify(sendCtx, "settings.changed", map[string]any{"settings": settings})
		cancel()
		if err != nil {
			target.log.Debug("cannot deliver a settings change", "error", err)
		}
	}
}

// connectionsFor returns every open connection a connector holds.
func (s *Server) connectionsFor(connectorID string) []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*conn
	for c := range s.conns {
		if connector := c.authenticated(); connector != nil && connector.ID == connectorID {
			out = append(out, c)
		}
	}
	return out
}

// connectedIDs reports which connectors currently have a connection open, so
// the dashboard can show what is actually running rather than what is merely
// installed.
func (s *Server) connectedIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]bool, len(s.conns))
	for c := range s.conns {
		if connector := c.authenticated(); connector != nil {
			out[connector.ID] = true
		}
	}
	return out
}

type fallbackParams struct {
	ConnectorID string `json:"connector_id"`
	UserID      string `json:"user_id"`
}

// connectorsSetFallbackUser chooses who a connector's jobs belong to when it
// does not identify people.
//
// The AirPrint case. A phone on the LAN prints without authenticating, so
// somebody has to decide whose printing that counts as. An empty user id clears
// it, and jobs then belong to nobody in particular, which is honest rather than
// wrong.
func (c *conn) connectorsSetFallbackUser(ctx context.Context, params json.RawMessage) (any, error) {
	var p fallbackParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	if err := c.db.SetConnectorFallbackUser(ctx, p.ConnectorID, p.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such connector or user")
		}
		return nil, err
	}

	c.log.Info("fallback user set", "connector", p.ConnectorID, "user", p.UserID)
	return map[string]any{"connector_id": p.ConnectorID, "user_id": p.UserID}, nil
}

type setEnabledParams struct {
	ConnectorID string `json:"connector_id"`
	Enabled     bool   `json:"enabled"`
}

// connectorsSetEnabled turns a connector on or off.
//
// A disabled connector stays enrolled and keeps its settings. It simply stops
// being allowed to do anything, which is checked fresh on every call rather
// than at connection time, so turning one off takes effect on the call it is
// making right now instead of whenever it next reconnects.
func (c *conn) connectorsSetEnabled(ctx context.Context, params json.RawMessage) (any, error) {
	var p setEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if strings.TrimSpace(p.ConnectorID) == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no connector id given")
	}

	// Refusing to let a connector switch itself off.
	//
	// The dashboard is a connector, and it is the one holding this call. Turning
	// itself off would take effect immediately, on a check that reads the
	// database fresh, so the next request would be refused and the only way back
	// in would be a database edit by hand. Nobody would mean to do this, and
	// everybody would be able to.
	self, err := c.currentConnector(ctx)
	if err != nil {
		return nil, err
	}
	if p.ConnectorID == self.ID && !p.Enabled {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams,
			"a connector cannot switch itself off")
	}

	if err := c.db.SetConnectorEnabled(ctx, p.ConnectorID, p.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no such connector")
		}
		return nil, err
	}

	c.log.Info("connector switched", "connector", p.ConnectorID, "enabled", p.Enabled)
	return map[string]any{"connector_id": p.ConnectorID, "enabled": p.Enabled}, nil
}

type inviteParams struct {
	ConnectorID string   `json:"connector_id"`
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
}

// inviteTTL is how long an enrolment token stays usable.
//
// Long enough to install and start a connector without hurrying, short enough
// that one forgotten in a chat message stops working. Single use besides, so the
// window only matters until it is taken up once.
const inviteTTL = 24 * time.Hour

// connectorsInvite lets a program that is not here yet connect.
//
// The gap this closes: bootstrapping said new connectors are "enrolled the
// normal way, through the dashboard", and there was no way to do it. Core could
// create a connector record and issue an enrolment token, and nothing in the
// protocol reached either, so the dashboard could list and configure connectors
// it already had and could not add one. A connector nobody anticipated could not
// arrive at all.
//
// Creating and inviting are one act on purpose. From an administrator's side
// both are "let this program connect", and a connector created without a token
// is a record nobody can use.
//
// Disabled to begin with. An administrator decides what runs, and enrolling is
// only the act of proving which key belongs to which name.
func (c *conn) connectorsInvite(ctx context.Context, params json.RawMessage) (any, error) {
	var p inviteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	id := strings.TrimSpace(p.ConnectorID)
	if id == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no connector id given")
	}

	existing, err := c.db.Connector(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if _, err := c.db.CreateConnector(ctx, id, strings.TrimSpace(p.Name), p.Scopes); err != nil {
			// The id rules and the scope names are the connector author's to
			// get right, so the reason is passed through rather than flattened.
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s", err.Error())
		}
	case err != nil:
		return nil, err
	default:
		// Inviting one that already exists reissues its token, which is how a
		// connector whose key was lost with the machine it ran on gets back in.
		// Its scopes are left alone: changing what something may do is a
		// separate decision from letting it reconnect.
		c.log.Info("reissuing an enrolment token", "connector", existing.ID)
	}

	token, err := c.db.NewEnrolmentToken(ctx, id, inviteTTL)
	if err != nil {
		return nil, err
	}

	c.log.Info("connector invited", "connector", id)

	// The token is returned once and cannot be shown again: only a hash of it is
	// stored, so there is nothing to print a second time. Callers have to say so
	// rather than offering somewhere to look it up later.
	return map[string]any{
		"connector_id": id,
		"token":        token,
		"expires_in":   int(inviteTTL.Seconds()),
	}, nil
}
