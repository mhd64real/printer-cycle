package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrLinkCodeInvalid = errors.New("store: that pairing code is not valid")
	ErrNotLinked       = errors.New("store: that identity is not linked to anyone")
)

// IdentityLink binds an external identity to a printer-cycle account.
type IdentityLink struct {
	ID          string
	ConnectorID string

	// ExternalID is whatever the connector calls the person, namespaced by the
	// connector: "tg:887312" means nothing outside the Telegram connector, and is
	// not required to.
	ExternalID string

	UserID string

	// Display is something a human recognises, like "@mhd64", shown on the screen
	// listing what is linked to an account.
	Display   string
	CreatedAt time.Time
}

// linkCodeTTL is how long a pairing code lasts if the connector does not say.
const linkCodeTTL = 10 * time.Minute

// NewLinkRequest issues a pairing code for an external identity.
//
// Every connector login flow anybody will write ends in the same place: binding
// an external identity to an account. Interactive chat login, a portal with a
// code, a QR scan, all of them. So core owns the binding and the connector owns
// only how the code is delivered, which is why one primitive covers flows nobody
// has designed yet.
func (db *DB) NewLinkRequest(ctx context.Context, connectorID, externalID, display string, ttl time.Duration) (code string, expiresAt time.Time, err error) {
	if strings.TrimSpace(externalID) == "" {
		return "", time.Time{}, fmt.Errorf("store: no external identity given")
	}
	if _, err := db.Connector(ctx, connectorID); err != nil {
		return "", time.Time{}, err
	}
	if ttl <= 0 {
		ttl = linkCodeTTL
	}

	// Expired codes are cleared here rather than by a sweeper. They are only
	// ever looked up by exact code, so a stale row is harmless until it is
	// removed, and doing it on write keeps the table from growing without adding
	// a background task to a machine with 512MB.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM identity_link_requests WHERE expires_at < datetime('now')`); err != nil {
		return "", time.Time{}, err
	}

	code, err = newLinkCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().UTC().Add(ttl)

	_, err = db.ExecContext(ctx,
		`INSERT INTO identity_link_requests (code, connector_id, external_id, display, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		code, connectorID, externalID, display, expiresAt.Format(sqlTimeLayout))
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// ApproveLinkRequest binds the identity behind a code to a user.
//
// The code is spent whether or not this succeeds in full, and unknown, expired
// and already-used codes are refused identically. Someone guessing gets no
// signal about which guess came closest.
func (db *DB) ApproveLinkRequest(ctx context.Context, code, userID string) (IdentityLink, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return IdentityLink{}, err
	}
	defer tx.Rollback()

	var connectorID, externalID, display, expires string
	err = tx.QueryRowContext(ctx,
		`SELECT connector_id, external_id, display, expires_at
		   FROM identity_link_requests WHERE code = ?`, normaliseCode(code),
	).Scan(&connectorID, &externalID, &display, &expires)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return IdentityLink{}, ErrLinkCodeInvalid
	case err != nil:
		return IdentityLink{}, err
	case parseTime(expires).Before(time.Now().UTC()):
		return IdentityLink{}, ErrLinkCodeInvalid
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM identity_link_requests WHERE code = ?`, normaliseCode(code)); err != nil {
		return IdentityLink{}, err
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return IdentityLink{}, err
	}
	if exists == 0 {
		return IdentityLink{}, ErrNotFound
	}

	link := IdentityLink{
		ID:          NewID("lnk"),
		ConnectorID: connectorID,
		ExternalID:  externalID,
		UserID:      userID,
		Display:     display,
	}

	// Re-linking an identity moves it rather than failing. Somebody handing
	// their old phone to a family member should be able to point the same
	// Telegram account at a different user without an administrator unpicking a
	// row first.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM identity_links WHERE connector_id = ? AND external_id = ?`,
		connectorID, externalID); err != nil {
		return IdentityLink{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO identity_links (id, connector_id, external_id, user_id, display)
		 VALUES (?, ?, ?, ?, ?)`,
		link.ID, link.ConnectorID, link.ExternalID, link.UserID, link.Display); err != nil {
		return IdentityLink{}, err
	}

	if err := tx.Commit(); err != nil {
		return IdentityLink{}, err
	}
	return db.IdentityLink(ctx, link.ID)
}

// ResolveIdentity finds the user an external identity belongs to.
func (db *DB) ResolveIdentity(ctx context.Context, connectorID, externalID string) (IdentityLink, error) {
	link, err := scanLink(db.QueryRowContext(ctx,
		linkSelect+` WHERE connector_id = ? AND external_id = ?`, connectorID, externalID))
	if errors.Is(err, ErrNotFound) {
		return IdentityLink{}, ErrNotLinked
	}
	return link, err
}

// IdentityLink returns one link by id.
func (db *DB) IdentityLink(ctx context.Context, id string) (IdentityLink, error) {
	return scanLink(db.QueryRowContext(ctx, linkSelect+` WHERE id = ?`, id))
}

// IdentityLinks lists what is linked to an account, which is what makes one
// screen able to answer "what can reach my printing, and how do I stop it".
func (db *DB) IdentityLinks(ctx context.Context, userID string) ([]IdentityLink, error) {
	query := linkSelect
	args := []any{}
	if userID != "" {
		query += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY id`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []IdentityLink
	for rows.Next() {
		l, err := scanLinkRow(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// DeleteIdentityLink revokes a link.
func (db *DB) DeleteIdentityLink(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM identity_links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

const linkSelect = `SELECT id, connector_id, external_id, user_id, display, created_at
                      FROM identity_links`

func scanLink(row rowScanner) (IdentityLink, error) {
	l, err := scanLinkRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentityLink{}, ErrNotFound
	}
	return l, err
}

func scanLinkRow(row rowScanner) (IdentityLink, error) {
	var (
		l       IdentityLink
		created string
	)
	if err := row.Scan(&l.ID, &l.ConnectorID, &l.ExternalID, &l.UserID, &l.Display, &created); err != nil {
		return IdentityLink{}, err
	}
	l.CreatedAt = parseTime(created)
	return l, nil
}

// newLinkCode produces something a person can read off a phone screen and type
// somewhere else.
//
// Eight characters of Crockford base32 is forty bits. A code lasts ten minutes,
// so guessing is not a practical attack, and the alphabet omits I, L, O and U so
// nothing is mistaken for a one or a zero while being copied by hand.
func newLinkCode() (string, error) {
	buf := make([]byte, 5) // 40 bits
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: reading random bytes: %w", err)
	}

	var b strings.Builder
	var bits, value uint32
	count := 0
	for _, by := range buf {
		value = value<<8 | uint32(by)
		bits += 8
		for bits >= 5 {
			bits -= 5
			if count == 4 {
				b.WriteByte('-')
			}
			b.WriteByte(crockford[(value>>bits)&31])
			count++
		}
	}
	return b.String(), nil
}

// normaliseCode accepts what somebody actually types: any case, with or without
// the hyphen, with stray spaces.
func normaliseCode(code string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(code))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	if !strings.Contains(cleaned, "-") && len(cleaned) == 8 {
		cleaned = cleaned[:4] + "-" + cleaned[4:]
	}
	return cleaned
}
