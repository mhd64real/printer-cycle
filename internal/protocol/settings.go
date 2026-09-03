package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
	return map[string]any{"connectors": out}, nil
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
