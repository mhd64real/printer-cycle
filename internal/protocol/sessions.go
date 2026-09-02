package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// Sign-in throttling.
//
// A session is issued from a username and a password, so without a limit the
// protocol is an unlimited password oracle for anybody holding the scope. Argon2
// makes each attempt cost about a fifth of a second on the target hardware,
// which is a real cost to an attacker and no defence against one who is patient.
const (
	signInAttempts = 8
	signInWindow   = 5 * time.Minute
)

// throttle counts recent failures per username.
//
// Per username rather than per connection, because a connector reconnecting is
// free and would otherwise reset the count. Held in memory: it protects against
// somebody hammering a running box, and a restart clearing it is not the case
// worth defending.
type throttle struct {
	mu       sync.Mutex
	failures map[string]*failureCount
}

type failureCount struct {
	count int
	since time.Time
}

func newThrottle() *throttle {
	return &throttle{failures: make(map[string]*failureCount)}
}

// allow reports whether another attempt may be made for this name.
func (t *throttle) allow(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	f, ok := t.failures[name]
	if !ok {
		return true
	}
	if time.Since(f.since) > signInWindow {
		delete(t.failures, name)
		return true
	}
	return f.count < signInAttempts
}

func (t *throttle) fail(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	f, ok := t.failures[name]
	if !ok || time.Since(f.since) > signInWindow {
		t.failures[name] = &failureCount{count: 1, since: time.Now()}
		return
	}
	f.count++
}

func (t *throttle) succeed(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, name)
}

type signInParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// usersAuthenticate verifies a password and issues a session.
//
// This is what closes the gap that core had no idea who a person is. Before it,
// a connector approving an identity pairing simply named the user it claimed was
// approving, and core could only trust it.
func (c *conn) usersAuthenticate(ctx context.Context, params json.RawMessage) (any, error) {
	var p signInParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	name := strings.ToLower(strings.TrimSpace(p.Username))
	if name == "" || p.Password == "" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "incorrect username or password")
	}

	if !c.server.signIns.allow(name) {
		c.log.Warn("too many failed sign-ins, refusing further attempts for now", "username", name)
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated,
			"too many attempts, try again shortly")
	}

	connector := c.authenticated()

	token, session, err := c.db.SignIn(ctx, name, p.Password, connector.ID)
	if errors.Is(err, store.ErrBadCredentials) {
		c.server.signIns.fail(name)
		c.log.Warn("failed sign-in", "username", name, "connector", connector.ID)
		return nil, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "incorrect username or password")
	}
	if err != nil {
		return nil, err
	}
	c.server.signIns.succeed(name)

	user, err := c.db.User(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	c.log.Info("user signed in", "user", user.Username, "connector", connector.ID)

	return map[string]any{
		"session":    token,
		"expires_at": session.ExpiresAt.Format(time.RFC3339),
		"user": map[string]any{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"is_admin":     user.IsAdmin,
		},
	}, nil
}

type sessionParams struct {
	Session string `json:"session"`
}

// usersSignOut revokes a session.
func (c *conn) usersSignOut(ctx context.Context, params json.RawMessage) (any, error) {
	var p sessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}
	if err := c.db.EndSession(ctx, p.Session); err != nil {
		return nil, err
	}
	return map[string]any{"signed_out": true}, nil
}

// usersWhoami resolves a session, which is how a connector confirms one is still
// good without doing anything with it.
func (c *conn) usersWhoami(ctx context.Context, params json.RawMessage) (any, error) {
	var p sessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "params are not an object")
	}

	user, err := c.userFromSession(ctx, p.Session)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName,
		"is_admin":     user.IsAdmin,
	}, nil
}

// userFromSession turns a session token into the person it belongs to.
//
// The connector presenting it must be the one that issued it, so a session
// minted by the dashboard cannot be replayed by another connector that happened
// to see it.
func (c *conn) userFromSession(ctx context.Context, token string) (store.User, error) {
	if strings.TrimSpace(token) == "" {
		return store.User{}, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "no session given")
	}

	connector := c.authenticated()
	session, err := c.db.Session(ctx, token, connector.ID)
	if errors.Is(err, store.ErrSessionInvalid) {
		return store.User{}, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "that session is not valid")
	}
	if err != nil {
		return store.User{}, err
	}

	user, err := c.db.User(ctx, session.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, jsonrpc.Errorf(jsonrpc.CodeNotAuthenticated, "that session is not valid")
	}
	return user, err
}
