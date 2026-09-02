package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mhd64real/printer-cycle/internal/passwd"
)

var (
	ErrNotFound       = errors.New("store: not found")
	ErrUsernameTaken  = errors.New("store: that username is already taken")
	ErrLastAdmin      = errors.New("store: the last administrator cannot be removed")
	ErrBadCredentials = errors.New("store: incorrect username or password")
)

// User is somebody with an account on this box.
type User struct {
	ID          string
	Username    string
	DisplayName string
	IsAdmin     bool
	CreatedAt   time.Time
}

// CreateUser adds a user.
//
// The first user created on a fresh box is an administrator, because otherwise
// nobody could administer it. Every user after that is not, unless promoted.
func (db *DB) CreateUser(ctx context.Context, username, displayName, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, fmt.Errorf("store: username is empty")
	}
	if password == "" {
		return User{}, fmt.Errorf("store: password is empty")
	}
	if displayName == "" {
		displayName = username
	}

	hash, err := passwd.Hash(password)
	if err != nil {
		return User{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&existing); err != nil {
		return User{}, err
	}

	u := User{
		ID:          NewID("user"),
		Username:    username,
		DisplayName: displayName,
		IsAdmin:     existing == 0,
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, username, display_name, password_hash, is_admin)
		 VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.DisplayName, hash, boolToInt(u.IsAdmin))
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}

	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return db.User(ctx, u.ID)
}

// User returns one user by id.
func (db *DB) User(ctx context.Context, id string) (User, error) {
	return db.scanUser(db.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id))
}

// UserByUsername returns one user by name, case insensitively.
func (db *DB) UserByUsername(ctx context.Context, username string) (User, error) {
	return db.scanUser(db.QueryRowContext(ctx, userSelect+` WHERE username = ?`, strings.TrimSpace(username)))
}

// Users lists every user, oldest first.
func (db *DB) Users(ctx context.Context) ([]User, error) {
	rows, err := db.QueryContext(ctx, userSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountUsers reports how many accounts exist. The dashboard hides user
// management entirely while the answer is one.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// Authenticate checks a username and password.
//
// A wrong password and an unknown username return the same error and take
// roughly the same time. Returning quickly for a name that does not exist would
// let anyone on the network enumerate who has an account on the box, so an
// unknown username is still charged the cost of a hash.
func (db *DB) Authenticate(ctx context.Context, username, password string) (User, error) {
	var (
		id, name, display, hash string
		admin                   int
		created                 string
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, is_admin, created_at
		   FROM users WHERE username = ?`, strings.TrimSpace(username),
	).Scan(&id, &name, &display, &hash, &admin, &created)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, _ = passwd.Verify(password, passwd.Dummy)
		return User{}, ErrBadCredentials
	case err != nil:
		return User{}, err
	}

	ok, err := passwd.Verify(password, hash)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrBadCredentials
	}

	return User{
		ID:          id,
		Username:    name,
		DisplayName: display,
		IsAdmin:     admin == 1,
		CreatedAt:   parseTime(created),
	}, nil
}

// SetPassword replaces a user's password.
func (db *DB) SetPassword(ctx context.Context, id, password string) error {
	if password == "" {
		return fmt.Errorf("store: password is empty")
	}
	hash, err := passwd.Hash(password)
	if err != nil {
		return err
	}

	res, err := db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	if err := requireOneRow(res); err != nil {
		return err
	}

	// Every session belonging to this account stops working.
	//
	// Changing a password is what somebody does when they believe it is known,
	// and leaving existing sessions alive would mean the change accomplishes
	// nothing against the person they are worried about.
	return db.EndUserSessions(ctx, id)
}

// SetAdmin promotes or demotes a user.
//
// Demoting the last administrator is refused, for the same reason deleting them
// is: it would leave a box nobody can administer.
func (db *DB) SetAdmin(ctx context.Context, id string, admin bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if !admin {
		if err := requireAnotherAdmin(ctx, tx, id); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE id = ?`, boolToInt(admin), id)
	if err != nil {
		return err
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteUser removes a user. The last administrator cannot be removed.
func (db *DB) DeleteUser(ctx context.Context, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := requireAnotherAdmin(ctx, tx, id); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := requireOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// requireAnotherAdmin fails if removing or demoting id would leave no
// administrator at all.
func requireAnotherAdmin(ctx context.Context, tx *sql.Tx, id string) error {
	var isAdmin int
	err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = ?`, id).Scan(&isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin == 0 {
		return nil
	}

	var others int
	err = tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE is_admin = 1 AND id != ?`, id).Scan(&others)
	if err != nil {
		return err
	}
	if others == 0 {
		return ErrLastAdmin
	}
	return nil
}

const userSelect = `SELECT id, username, display_name, is_admin, created_at FROM users`

type rowScanner interface {
	Scan(dest ...any) error
}

func (db *DB) scanUser(row rowScanner) (User, error) {
	u, err := scanUserRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func scanUserRow(row rowScanner) (User, error) {
	var (
		u       User
		admin   int
		created string
	)
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &admin, &created); err != nil {
		return User{}, err
	}
	u.IsAdmin = admin == 1
	u.CreatedAt = parseTime(created)
	return u, nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func requireOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	// modernc's driver reports constraint failures in the message rather than
	// through a typed error, so this matches on text. Narrow on purpose: it
	// checks for the UNIQUE wording specifically rather than treating every
	// constraint failure as a duplicate name.
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}
