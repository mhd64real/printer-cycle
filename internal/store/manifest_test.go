package store_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

func telegramManifest() store.Manifest {
	min, max := 1, 500
	return store.Manifest{
		Name:        "Telegram",
		Version:     "1.2.0",
		Description: "Print by sending a document to a Telegram bot.",
		Identity:    store.IdentityLinked,
		Settings: store.SettingsSchema{
			{Key: "bot_token", Type: store.SettingSecret, Label: "Bot token", Required: true},
			{Key: "allow_groups", Type: store.SettingBool, Label: "Accept documents in groups", Default: false},
			{Key: "max_pages", Type: store.SettingInt, Label: "Page limit per job", Default: 20, Min: &min, Max: &max},
			{Key: "quality", Type: store.SettingEnum, Label: "Quality", Options: []string{"draft", "normal", "best"}},
		},
	}
}

// The stage's done-when, literally: a declared schema has to survive core being
// restarted, which means reopening the database from disk.
func TestADeclaredSchemaSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printer-cycle.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Register(ctx(), "telegram", telegramManifest()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx(), "telegram", "max_pages", 42); err != nil {
		t.Fatal(err)
	}
	db.Close()

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	schema, err := reopened.SettingsSchemaOf(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 4 {
		t.Fatalf("schema has %d fields after a restart, want 4", len(schema))
	}
	if schema[0].Key != "bot_token" || !schema[0].Secret() {
		t.Errorf("first field = %+v", schema[0])
	}
	if schema[2].Min == nil || *schema[2].Min != 1 {
		t.Error("the integer bounds did not survive")
	}

	values, err := reopened.SettingsFor(ctx(), "telegram", false)
	if err != nil {
		t.Fatal(err)
	}
	if values["max_pages"] != 42 {
		t.Errorf("max_pages = %v, want the stored 42", values["max_pages"])
	}

	c, err := reopened.Connector(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if c.IdentityPolicy != store.IdentityLinked {
		t.Errorf("identity policy = %q, want linked", c.IdentityPolicy)
	}
}

// A connector needs its own secrets to work. Everybody else must not see them.
func TestSecretsGoToTheirOwnerAndNobodyElse(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Register(ctx(), "telegram", telegramManifest()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx(), "telegram", "bot_token", "12345:AAHfake-token"); err != nil {
		t.Fatal(err)
	}

	owner, err := db.SettingsFor(ctx(), "telegram", true)
	if err != nil {
		t.Fatal(err)
	}
	if owner["bot_token"] != "12345:AAHfake-token" {
		t.Errorf("the connector cannot read its own token back: %v", owner["bot_token"])
	}

	others, err := db.SettingsFor(ctx(), "telegram", false)
	if err != nil {
		t.Fatal(err)
	}
	shown, ok := others["bot_token"].(map[string]any)
	if !ok {
		t.Fatalf("bot_token = %#v, want a set/unset marker rather than a value", others["bot_token"])
	}
	if shown["set"] != true {
		t.Error("the dashboard cannot tell that a token has been set")
	}
	for _, v := range shown {
		if s, ok := v.(string); ok && strings.Contains(s, "AAHfake") {
			t.Fatal("the secret leaked into what the dashboard is shown")
		}
	}
}

func TestManifestValidation(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "c", "", nil); err != nil {
		t.Fatal(err)
	}

	high, low := 10, 1
	bad := map[string]store.Manifest{
		"no name":                 {Settings: nil},
		"unknown identity policy": {Name: "x", Identity: "sometimes"},
		"setting with no key":     {Name: "x", Settings: store.SettingsSchema{{Type: store.SettingString}}},
		"setting with no type":    {Name: "x", Settings: store.SettingsSchema{{Key: "a"}}},
		"unknown type":            {Name: "x", Settings: store.SettingsSchema{{Key: "a", Type: "colour"}}},
		"duplicate key": {Name: "x", Settings: store.SettingsSchema{
			{Key: "a", Type: store.SettingString}, {Key: "a", Type: store.SettingInt}}},
		"enum with no options": {Name: "x", Settings: store.SettingsSchema{{Key: "a", Type: store.SettingEnum}}},
		"minimum above maximum": {Name: "x", Settings: store.SettingsSchema{
			{Key: "a", Type: store.SettingInt, Min: &high, Max: &low}}},
		"secret with a default": {Name: "x", Settings: store.SettingsSchema{
			{Key: "a", Type: store.SettingSecret, Default: "hunter2"}}},
		"key with a space": {Name: "x", Settings: store.SettingsSchema{
			{Key: "bot token", Type: store.SettingString}}},
	}

	for name, m := range bad {
		if err := db.Register(ctx(), "c", m); err == nil {
			t.Errorf("a manifest with %s was accepted", name)
		}
	}

	if err := db.Register(ctx(), "c", telegramManifest()); err != nil {
		t.Errorf("a valid manifest was rejected: %v", err)
	}
}

// Registering replaces rather than merges. A setting the connector dropped in a
// new version must stop appearing in the dashboard.
func TestRegisteringReplacesTheSchema(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Register(ctx(), "telegram", telegramManifest()); err != nil {
		t.Fatal(err)
	}

	slimmer := store.Manifest{
		Name:     "Telegram",
		Version:  "2.0.0",
		Settings: store.SettingsSchema{{Key: "bot_token", Type: store.SettingSecret, Label: "Bot token"}},
	}
	if err := db.Register(ctx(), "telegram", slimmer); err != nil {
		t.Fatal(err)
	}

	schema, err := db.SettingsSchemaOf(ctx(), "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 1 {
		t.Errorf("schema has %d fields after an upgrade dropped three, want 1", len(schema))
	}
}

func TestSettingValuesAreCheckedAgainstTheSchema(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Register(ctx(), "telegram", telegramManifest()); err != nil {
		t.Fatal(err)
	}

	bad := []struct {
		key   string
		value any
		why   string
	}{
		{"max_pages", "twenty", "text where a number belongs"},
		{"max_pages", 9000, "above the declared maximum"},
		{"max_pages", 0, "below the declared minimum"},
		{"max_pages", 1.5, "a fraction where a whole number belongs"},
		{"allow_groups", "yes", "text where a boolean belongs"},
		{"quality", "perfect", "not one of the declared options"},
		{"nonexistent", "x", "a setting the connector never declared"},
	}
	for _, tc := range bad {
		if err := db.SetSetting(ctx(), "telegram", tc.key, tc.value); err == nil {
			t.Errorf("accepted %s for %s", tc.why, tc.key)
		}
	}

	good := []struct {
		key   string
		value any
	}{
		{"max_pages", 20},
		{"max_pages", float64(30)}, // how a JSON number arrives
		{"allow_groups", true},
		{"quality", "best"},
	}
	for _, tc := range good {
		if err := db.SetSetting(ctx(), "telegram", tc.key, tc.value); err != nil {
			t.Errorf("%s = %v was rejected: %v", tc.key, tc.value, err)
		}
	}
}

// A setting never set reads back as its declared default, so a connector does
// not have to write its own defaults in twice.
func TestUnsetSettingsFallBackToTheirDefault(t *testing.T) {
	db := newDB(t)
	if _, err := db.CreateConnector(ctx(), "telegram", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Register(ctx(), "telegram", telegramManifest()); err != nil {
		t.Fatal(err)
	}

	values, err := db.SettingsFor(ctx(), "telegram", true)
	if err != nil {
		t.Fatal(err)
	}
	if values["max_pages"] != 20 {
		t.Errorf("max_pages = %v, want the declared default of 20", values["max_pages"])
	}
	if values["allow_groups"] != false {
		t.Errorf("allow_groups = %v, want the declared default", values["allow_groups"])
	}
}
