package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrSessionInvalid covers every reason a session cannot be used: unknown,
// expired, revoked, or belonging to a different connector. One error, because
// telling them apart would help somebody probing and nobody else.
var ErrSessionInvalid = errors.New("store: that session is not valid")

// SessionLifetime is how long a sign-in lasts.
//
// Long enough that a household is not signing in repeatedly, short enough that a
// browser left open on a shared machine stops being useful within a day.
const SessionLifetime = 12 * time.Hour

// Session is a signed-in person.
type Session struct {
	UserID      string
	ConnectorID string
	ExpiresAt   time.Time
}

// SignIn verifies a password and issues a session.
//
// The token is returned once and never stored in full. [DB.Authenticate] checks
// a password without issuing anything, which is what callers that only need to
// confirm a credential should use.
func (db *DB) SignIn(ctx context.Context, username, password, connectorID string) (token string, session Session, err error) {
	user, err := db.Authenticate(ctx, username, password)
	if err != nil {
		return "", Session{}, err
	}

	token, err = newSessionToken()
	if err != nil {
		return "", Session{}, err
	}

	session = Session{
		UserID:      user.ID,
		ConnectorID: connectorID,
		ExpiresAt:   time.Now().UTC().Add(SessionLifetime),
	}

	// Expired rows are cleared on write rather than by a sweeper: they are only
	// ever looked up by exact token, so a stale row is harmless until removed,
	// and a machine with 512MB does not need another periodic task.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM user_sessions WHERE expires_at < datetime('now')`); err != nil {
		return "", Session{}, err
	}

	var connector any
	if connectorID != "" {
		connector = connectorID
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO user_sessions (token_hash, user_id, connector_id, expires_at)
		 VALUES (?, ?, ?, ?)`,
		hashSessionToken(token), session.UserID, connector,
		session.ExpiresAt.Format(sqlTimeLayout))
	if err != nil {
		return "", Session{}, err
	}
	return token, session, nil
}

// Session resolves a token to the person it belongs to.
//
// connectorID is the connector presenting it. A session is usable only by the
// connector that created it, so one minted by the dashboard cannot be replayed
// by another connector that happened to see it go past.
func (db *DB) Session(ctx context.Context, token, connectorID string) (Session, error) {
	var (
		userID  string
		owner   sql.NullString
		expires string
	)
	err := db.QueryRowContext(ctx,
		`SELECT user_id, connector_id, expires_at FROM user_sessions WHERE token_hash = ?`,
		hashSessionToken(token),
	).Scan(&userID, &owner, &expires)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Session{}, ErrSessionInvalid
	case err != nil:
		return Session{}, err
	case parseTime(expires).Before(time.Now().UTC()):
		return Session{}, ErrSessionInvalid
	case owner.Valid && owner.String != connectorID:
		return Session{}, ErrSessionInvalid
	}

	// Recorded for the screen that lists where an account is signed in. Failing
	// to write it is not a reason to refuse a valid session.
	_, _ = db.ExecContext(ctx,
		`UPDATE user_sessions SET last_used_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Format(sqlTimeLayout), hashSessionToken(token))

	return Session{
		UserID:      userID,
		ConnectorID: owner.String,
		ExpiresAt:   parseTime(expires),
	}, nil
}

// EndSession revokes one session.
func (db *DB) EndSession(ctx context.Context, token string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM user_sessions WHERE token_hash = ?`, hashSessionToken(token))
	return err
}

// EndUserSessions revokes every session belonging to a user, which is what a
// password change has to do.
func (db *DB) EndUserSessions(ctx context.Context, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = ?`, userID)
	return err
}

func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
