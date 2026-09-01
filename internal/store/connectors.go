package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrConnectorExists  = errors.New("store: a connector with that id already exists")
	ErrEnrolmentInvalid = errors.New("store: that enrolment token is not valid")
	ErrNotEnrolled      = errors.New("store: the connector has not enrolled a key yet")
)

// Scopes, mirroring PROTOCOL.md section 5.
//
// Held as a closed set so a typo in a scope name is refused outright. Silently
// storing "printers.mangage" would produce a connector that looks permitted in
// the dashboard and is denied at every call, which is a miserable thing to
// debug.
const (
	ScopeJobsSubmit     = "jobs.submit"
	ScopeJobsRead       = "jobs.read"
	ScopeJobsReadAll    = "jobs.read.all"
	ScopeJobsCancel     = "jobs.cancel"
	ScopePrintersRead   = "printers.read"
	ScopePrintersManage = "printers.manage"
	ScopeIdentityLink   = "identity.link"
	ScopeUsersRead      = "users.read"
	ScopeUsersManage    = "users.manage"

	// Added 2026-09-02. The original scope list covered printers, jobs,
	// identities and users, and nothing at all for connectors, which left the
	// dashboard's own main screen with no permission that described it.
	ScopeConnectorsRead   = "connectors.read"
	ScopeConnectorsManage = "connectors.manage"
)

var validScopes = map[string]bool{
	ScopeJobsSubmit:       true,
	ScopeJobsRead:         true,
	ScopeJobsReadAll:      true,
	ScopeJobsCancel:       true,
	ScopePrintersRead:     true,
	ScopePrintersManage:   true,
	ScopeIdentityLink:     true,
	ScopeUsersRead:        true,
	ScopeUsersManage:      true,
	ScopeConnectorsRead:   true,
	ScopeConnectorsManage: true,
}

// KnownScopes lists every scope core recognises, sorted.
func KnownScopes() []string {
	out := make([]string, 0, len(validScopes))
	for s := range validScopes {
		out = append(out, s)
	}
	sortStrings(out)
	return out
}

// IdentityPolicy is how a connector identifies the people it acts for.
type IdentityPolicy string

const (
	// IdentityNone means the connector does not know who anyone is. Everything
	// it submits is attributed to its fallback user. This is the AirPrint case:
	// a phone on the LAN prints without authenticating, because that is what
	// AirPrint is, and the connector is still authenticated to core.
	IdentityNone IdentityPolicy = "none"

	// IdentityLinked means the connector resolves an external identity to a
	// printer-cycle user before submitting.
	IdentityLinked IdentityPolicy = "linked"
)

// Connector is an installed connector.
type Connector struct {
	ID          string
	Name        string
	Version     string
	Description string

	IdentityPolicy IdentityPolicy
	FallbackUserID string

	// PublicKey is nil until the connector has enrolled. Core never holds
	// anything else: there is no secret here that could impersonate it, so a
	// copied database is worth nothing to whoever took it.
	PublicKey ed25519.PublicKey

	Enabled    bool
	Scopes     []string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// Enrolled reports whether the connector has registered a key and can
// authenticate.
func (c Connector) Enrolled() bool { return len(c.PublicKey) == ed25519.PublicKeySize }

// ValidConnectorID reports whether id is usable.
//
// Connector ids travel through the protocol, appear in log lines, and are typed
// by administrators, so they are restricted to lowercase letters, digits and
// hyphens.
func ValidConnectorID(id string) error {
	if id == "" {
		return fmt.Errorf("store: connector id is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("store: connector id %q is longer than 64 characters", id)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("store: connector id %q may only contain lowercase letters, digits and hyphens", id)
		}
	}
	return nil
}

// CreateConnector registers a connector.
//
// It is created disabled and unenrolled: it has no key yet, and nothing runs
// until an administrator turns it on. Both are deliberate. Installing something
// should never be the same act as trusting it.
func (db *DB) CreateConnector(ctx context.Context, id, name string, scopes []string) (Connector, error) {
	if err := ValidConnectorID(id); err != nil {
		return Connector{}, err
	}
	if name == "" {
		name = id
	}
	if err := checkScopes(scopes); err != nil {
		return Connector{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Connector{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO connectors (id, name, auth_method, credential, enabled)
		 VALUES (?, ?, 'ed25519', '', 0)`, id, name)
	if err != nil {
		if isUniqueViolation(err) {
			return Connector{}, ErrConnectorExists
		}
		return Connector{}, err
	}

	if err := replaceScopes(ctx, tx, id, scopes); err != nil {
		return Connector{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connector{}, err
	}
	return db.Connector(ctx, id)
}

// NewEnrolmentToken issues a single-use token letting a connector register its
// public key. The token is returned once and never stored in full.
func (db *DB) NewEnrolmentToken(ctx context.Context, connectorID string, ttl time.Duration) (string, error) {
	if _, err := db.Connector(ctx, connectorID); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	token, err := newEnrolmentToken()
	if err != nil {
		return "", err
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO connector_enrolments (token_hash, connector_id, expires_at)
		 VALUES (?, ?, ?)`,
		hashToken(token), connectorID, time.Now().UTC().Add(ttl).Format(sqlTimeLayout))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Enrol records a connector's public key against a valid, unused, unexpired
// token, and spends the token.
//
// Every failure returns the same error. A caller presenting a bad token must not
// be able to learn whether it was unknown, already used, or merely expired.
func (db *DB) Enrol(ctx context.Context, token string, publicKey ed25519.PublicKey) (Connector, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return Connector{}, fmt.Errorf("store: public key is %d bytes, want %d",
			len(publicKey), ed25519.PublicKeySize)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Connector{}, err
	}
	defer tx.Rollback()

	var (
		connectorID string
		expiresAt   string
		usedAt      sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT connector_id, expires_at, used_at FROM connector_enrolments WHERE token_hash = ?`,
		hashToken(token),
	).Scan(&connectorID, &expiresAt, &usedAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Connector{}, ErrEnrolmentInvalid
	case err != nil:
		return Connector{}, err
	case usedAt.Valid:
		return Connector{}, ErrEnrolmentInvalid
	case parseTime(expiresAt).Before(time.Now().UTC()):
		return Connector{}, ErrEnrolmentInvalid
	}

	// Enrolling normally does not enable anything: an administrator decides
	// what runs. During first run there is no administrator to decide, because
	// creating one is what the dashboard exists to do, so possession of a valid
	// single-use token from the machine's own console is the authorisation.
	//
	// Narrow on purpose. It applies only to the dashboard, and only while no
	// account exists. "Nothing else could hold a token on a fresh box anyway" is
	// probably true and is not a reason to write the broader rule.
	var users int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return Connector{}, err
	}
	firstRun := users == 0 && connectorID == DashboardConnectorID

	encoded := base64.StdEncoding.EncodeToString(publicKey)
	if _, err := tx.ExecContext(ctx,
		`UPDATE connectors SET auth_method = 'ed25519', credential = ?, enabled = ? WHERE id = ?`,
		encoded, boolToInt(firstRun), connectorID); err != nil {
		return Connector{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE connector_enrolments SET used_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Format(sqlTimeLayout), hashToken(token)); err != nil {
		return Connector{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connector{}, err
	}
	return db.Connector(ctx, connectorID)
}

// Connector returns one connector with its scopes.
func (db *DB) Connector(ctx context.Context, id string) (Connector, error) {
	var (
		c          Connector
		policy     string
		fallback   sql.NullString
		credential string
		enabled    int
		created    string
		lastSeen   sql.NullString
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, name, version, description, identity_policy, fallback_user_id,
		        credential, enabled, created_at, last_seen_at
		   FROM connectors WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Version, &c.Description, &policy, &fallback,
		&credential, &enabled, &created, &lastSeen)

	if errors.Is(err, sql.ErrNoRows) {
		return Connector{}, ErrNotFound
	}
	if err != nil {
		return Connector{}, err
	}

	c.IdentityPolicy = IdentityPolicy(policy)
	c.FallbackUserID = fallback.String
	c.Enabled = enabled == 1
	c.CreatedAt = parseTime(created)
	if lastSeen.Valid {
		c.LastSeenAt = parseTime(lastSeen.String)
	}
	if credential != "" {
		if key, err := base64.StdEncoding.DecodeString(credential); err == nil &&
			len(key) == ed25519.PublicKeySize {
			c.PublicKey = ed25519.PublicKey(key)
		}
	}

	scopes, err := connectorScopes(ctx, db.DB, id)
	if err != nil {
		return Connector{}, err
	}
	c.Scopes = scopes
	return c, nil
}

// Connectors lists every installed connector.
func (db *DB) Connectors(ctx context.Context) ([]Connector, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM connectors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Connector, 0, len(ids))
	for _, id := range ids {
		c, err := db.Connector(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// SetConnectorEnabled turns a connector on or off.
func (db *DB) SetConnectorEnabled(ctx context.Context, id string, enabled bool) error {
	if enabled {
		c, err := db.Connector(ctx, id)
		if err != nil {
			return err
		}
		// Enabling something that cannot authenticate would leave an entry that
		// looks live in the dashboard and rejects every connection.
		if !c.Enrolled() {
			return ErrNotEnrolled
		}
	}

	res, err := db.ExecContext(ctx, `UPDATE connectors SET enabled = ? WHERE id = ?`,
		boolToInt(enabled), id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// SetConnectorScopes replaces a connector's permissions.
func (db *DB) SetConnectorScopes(ctx context.Context, id string, scopes []string) error {
	if err := checkScopes(scopes); err != nil {
		return err
	}
	if _, err := db.Connector(ctx, id); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := replaceScopes(ctx, tx, id, scopes); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteConnector removes a connector, along with its scopes, settings, identity
// links and enrolment tokens. Its jobs survive, with connector_id set to null.
func (db *DB) DeleteConnector(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM connectors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// TouchConnector records that a connector was seen.
func (db *DB) TouchConnector(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `UPDATE connectors SET last_seen_at = ? WHERE id = ?`,
		time.Now().UTC().Format(sqlTimeLayout), id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

const sqlTimeLayout = "2006-01-02 15:04:05"

func connectorScopes(ctx context.Context, q *sql.DB, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT scope FROM connector_scopes WHERE connector_id = ? ORDER BY scope`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scopes []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		scopes = append(scopes, s)
	}
	return scopes, rows.Err()
}

func replaceScopes(ctx context.Context, tx *sql.Tx, id string, scopes []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM connector_scopes WHERE connector_id = ?`, id); err != nil {
		return err
	}
	for _, s := range scopes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO connector_scopes (connector_id, scope) VALUES (?, ?)`, id, s); err != nil {
			return err
		}
	}
	return nil
}

func checkScopes(scopes []string) error {
	for _, s := range scopes {
		if !validScopes[s] {
			return fmt.Errorf("store: %q is not a scope core recognises", s)
		}
	}
	return nil
}

// newEnrolmentToken produces a token an administrator can read off a screen and
// type somewhere else without transcription errors.
func newEnrolmentToken() (string, error) {
	buf := make([]byte, 13) // 104 bits, giving 20 base32 characters
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: reading random bytes: %w", err)
	}

	var b strings.Builder
	b.WriteString("PCE-")
	var bits, value uint32
	count := 0
	for _, by := range buf {
		value = value<<8 | uint32(by)
		bits += 8
		for bits >= 5 {
			bits -= 5
			if count > 0 && count%5 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(crockford[(value>>bits)&31])
			count++
		}
	}
	return b.String(), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(token))))
	return hex.EncodeToString(sum[:])
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
