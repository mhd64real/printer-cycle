package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Setting field types, from PROTOCOL.md section 6.
const (
	SettingString = "string"
	SettingText   = "text"
	SettingInt    = "int"
	SettingBool   = "bool"
	SettingEnum   = "enum"
	SettingSecret = "secret"
)

// SettingField is one setting a connector declares.
//
// A connector is a separate process and cannot inject anything into the
// dashboard, so it describes its settings and the dashboard renders them. That
// means every connector, including ones nobody has written yet, gets a settings
// page without the dashboard knowing anything about it.
type SettingField struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
}

// Secret reports whether the value is write-only to everyone but the connector
// that owns it.
func (f SettingField) Secret() bool { return f.Type == SettingSecret }

// SettingsSchema is everything a connector declares.
type SettingsSchema []SettingField

// Manifest is what a connector sends when it registers.
type Manifest struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Identity    IdentityPolicy `json:"identity"`
	Settings    SettingsSchema `json:"settings"`
}

// Validate checks a manifest before any of it is stored.
//
// Strict on purpose. A schema that gets stored and then fails to render leaves
// a connector nobody can configure and an error message pointing at the
// dashboard rather than at the connector that caused it.
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("store: manifest has no name")
	}
	switch m.Identity {
	case "", IdentityNone, IdentityLinked:
	default:
		return fmt.Errorf("store: identity policy %q is neither none nor linked", m.Identity)
	}
	return m.Settings.Validate()
}

// Validate checks a declared settings schema.
func (s SettingsSchema) Validate() error {
	seen := make(map[string]bool, len(s))

	for i, f := range s {
		if f.Key == "" {
			return fmt.Errorf("store: setting %d has no key", i)
		}
		if seen[f.Key] {
			return fmt.Errorf("store: setting %q is declared twice", f.Key)
		}
		seen[f.Key] = true

		for _, r := range f.Key {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			default:
				return fmt.Errorf(
					"store: setting key %q may only contain lowercase letters, digits and underscores", f.Key)
			}
		}

		switch f.Type {
		case SettingString, SettingText, SettingBool, SettingSecret:
		case SettingInt:
			if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
				return fmt.Errorf("store: setting %q has a minimum above its maximum", f.Key)
			}
		case SettingEnum:
			if len(f.Options) == 0 {
				return fmt.Errorf("store: setting %q is an enum with no options, so nothing could be chosen", f.Key)
			}
		case "":
			return fmt.Errorf("store: setting %q has no type", f.Key)
		default:
			return fmt.Errorf("store: setting %q has unknown type %q", f.Key, f.Type)
		}

		// A secret with a default would be a secret written down in the
		// connector's own source code.
		if f.Secret() && f.Default != nil {
			return fmt.Errorf("store: setting %q is a secret and cannot have a default", f.Key)
		}
	}
	return nil
}

// Register records what a connector says about itself.
//
// Called on every connection, so it replaces rather than merges: a connector
// that dropped a setting in its latest version should not leave the old field
// showing in the dashboard forever.
func (db *DB) Register(ctx context.Context, connectorID string, m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}

	encoded, err := json.Marshal(m.Settings)
	if err != nil {
		return fmt.Errorf("store: encoding the settings schema: %w", err)
	}
	if m.Settings == nil {
		encoded = []byte("[]")
	}

	identity := m.Identity
	if identity == "" {
		identity = IdentityNone
	}

	res, err := db.ExecContext(ctx,
		`UPDATE connectors
		    SET name = ?, version = ?, description = ?, identity_policy = ?, settings_schema = ?
		  WHERE id = ?`,
		m.Name, m.Version, m.Description, string(identity), string(encoded), connectorID)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// SettingsSchemaOf returns what a connector declared.
func (db *DB) SettingsSchemaOf(ctx context.Context, connectorID string) (SettingsSchema, error) {
	var encoded string
	err := db.QueryRowContext(ctx,
		`SELECT settings_schema FROM connectors WHERE id = ?`, connectorID).Scan(&encoded)
	if err != nil {
		return nil, ErrNotFound
	}

	var schema SettingsSchema
	if err := json.Unmarshal([]byte(encoded), &schema); err != nil {
		return nil, fmt.Errorf("store: stored settings schema for %q is unreadable: %w", connectorID, err)
	}
	return schema, nil
}

// SettingsFor returns a connector's current setting values.
//
// includeSecrets belongs to exactly one caller: the connector itself. A
// connector needs its own secrets to function, since a Telegram connector that
// cannot read back its bot token cannot talk to Telegram. Everybody else,
// dashboard included, gets everything except them, so a token typed in last year
// cannot be recovered by whoever has a browser open today.
func (db *DB) SettingsFor(ctx context.Context, connectorID string, includeSecrets bool) (map[string]any, error) {
	schema, err := db.SettingsSchemaOf(ctx, connectorID)
	if err != nil {
		return nil, err
	}

	stored := map[string]string{}
	rows, err := db.QueryContext(ctx,
		`SELECT key, value FROM connector_settings WHERE connector_id = ?`, connectorID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		stored[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]any, len(schema))
	for _, f := range schema {
		if f.Secret() && !includeSecrets {
			// Reported as set or unset, never as a value. Whether a token exists
			// is something the dashboard has to show; what it is, is not.
			out[f.Key] = map[string]any{"secret": true, "set": stored[f.Key] != ""}
			continue
		}

		raw, ok := stored[f.Key]
		if !ok {
			out[f.Key] = defaultValue(f)
			continue
		}
		out[f.Key] = decodeSetting(f, raw)
	}
	return out, nil
}

// SetSetting stores one value, checked against the declared schema.
func (db *DB) SetSetting(ctx context.Context, connectorID, key string, value any) error {
	schema, err := db.SettingsSchemaOf(ctx, connectorID)
	if err != nil {
		return err
	}

	var field *SettingField
	for i := range schema {
		if schema[i].Key == key {
			field = &schema[i]
			break
		}
	}
	if field == nil {
		return fmt.Errorf("store: %q declares no setting called %q", connectorID, key)
	}

	encoded, err := encodeSetting(*field, value)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO connector_settings (connector_id, key, value, is_secret, updated_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT (connector_id, key)
		 DO UPDATE SET value = excluded.value, is_secret = excluded.is_secret, updated_at = datetime('now')`,
		connectorID, key, encoded, boolToInt(field.Secret()))
	return err
}

func encodeSetting(f SettingField, value any) (string, error) {
	switch f.Type {
	case SettingBool:
		b, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("store: setting %q expects true or false", f.Key)
		}
		if b {
			return "true", nil
		}
		return "false", nil

	case SettingInt:
		n, ok := toInt(value)
		if !ok {
			return "", fmt.Errorf("store: setting %q expects a whole number", f.Key)
		}
		if f.Min != nil && n < *f.Min {
			return "", fmt.Errorf("store: setting %q is %d, below the minimum of %d", f.Key, n, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return "", fmt.Errorf("store: setting %q is %d, above the maximum of %d", f.Key, n, *f.Max)
		}
		return fmt.Sprintf("%d", n), nil

	case SettingEnum:
		s, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("store: setting %q expects one of its options", f.Key)
		}
		for _, opt := range f.Options {
			if opt == s {
				return s, nil
			}
		}
		return "", fmt.Errorf("store: setting %q is %q, which is not one of %v", f.Key, s, f.Options)

	default:
		s, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("store: setting %q expects text", f.Key)
		}
		return s, nil
	}
}

// defaultValue normalises a declared default to the type the field says it is.
//
// The schema is stored as JSON, so a default written as an int in a manifest
// comes back as a float64 after a restart. Without this, a setting reads as 20
// before core restarts and 20.0 after, and code that compares them behaves
// differently on Tuesday than it did on Monday.
func defaultValue(f SettingField) any {
	if f.Default == nil {
		return nil
	}
	if f.Type == SettingInt {
		if n, ok := toInt(f.Default); ok {
			return n
		}
	}
	return f.Default
}

func decodeSetting(f SettingField, raw string) any {
	switch f.Type {
	case SettingBool:
		return raw == "true"
	case SettingInt:
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
			return f.Default
		}
		return n
	default:
		return raw
	}
}

// toInt accepts the shapes a JSON number can arrive in.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		// JSON has no integers, so a whole number arrives as a float. Anything
		// with a fractional part is not what the connector asked for.
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
