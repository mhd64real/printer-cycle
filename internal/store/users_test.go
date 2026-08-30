package store_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/store"
)

func ctx() context.Context { return context.Background() }

// The first account on a fresh box has to be an administrator, or nobody can
// administer it. Every account after that must not be.
func TestFirstUserIsAdmin(t *testing.T) {
	db := newDB(t)

	first, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsAdmin {
		t.Error("the first user is not an administrator, so the box cannot be administered")
	}

	second, err := db.CreateUser(ctx(), "yasmin", "Yasmin", "another-password")
	if err != nil {
		t.Fatal(err)
	}
	if second.IsAdmin {
		t.Error("the second user was made an administrator")
	}
}

func TestUsersRoundTrip(t *testing.T) {
	db := newDB(t)

	created, err := db.CreateUser(ctx(), "mohamed", "Mohamed Elsayed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !strings.HasPrefix(created.ID, "user_") {
		t.Errorf("id = %q, want a user_ prefixed identifier", created.ID)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at did not come back")
	}

	got, err := db.User(ctx(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != created {
		t.Errorf("User returned %+v, want %+v", got, created)
	}

	// Usernames are case insensitive, so signing in with different capitals has
	// to find the same account.
	byName, err := db.UserByUsername(ctx(), "MOHAMED")
	if err != nil {
		t.Fatalf("looking up a username in different case: %v", err)
	}
	if byName.ID != created.ID {
		t.Error("a differently capitalised username found a different account")
	}

	if _, err := db.User(ctx(), "user_doesnotexist"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("User on a missing id returned %v, want ErrNotFound", err)
	}
}

func TestDuplicateUsernameIsRefused(t *testing.T) {
	db := newDB(t)

	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	_, err := db.CreateUser(ctx(), "Mohamed", "", "different")
	if !errors.Is(err, store.ErrUsernameTaken) {
		t.Errorf("err = %v, want ErrUsernameTaken", err)
	}
}

func TestAuthenticate(t *testing.T) {
	db := newDB(t)

	created, err := db.CreateUser(ctx(), "mohamed", "Mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	got, err := db.Authenticate(ctx(), "mohamed", "hunter2hunter2")
	if err != nil {
		t.Fatalf("the correct password was rejected: %v", err)
	}
	if got.ID != created.ID {
		t.Error("authenticated as the wrong user")
	}

	if _, err := db.Authenticate(ctx(), "mohamed", "wrong"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("wrong password gave %v, want ErrBadCredentials", err)
	}

	// An unknown username must be indistinguishable from a wrong password, or
	// anybody can find out who has an account on the box.
	if _, err := db.Authenticate(ctx(), "nobody", "whatever"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("unknown username gave %v, want the same ErrBadCredentials as a wrong password", err)
	}
}

func TestSetPassword(t *testing.T) {
	db := newDB(t)

	u, err := db.CreateUser(ctx(), "mohamed", "", "old-password-here")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetPassword(ctx(), u.ID, "new-password-here"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Authenticate(ctx(), "mohamed", "old-password-here"); !errors.Is(err, store.ErrBadCredentials) {
		t.Error("the old password still works after a change")
	}
	if _, err := db.Authenticate(ctx(), "mohamed", "new-password-here"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

// Losing the last administrator would leave a box nobody can configure, with no
// way back short of editing the database by hand.
func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	db := newDB(t)

	admin, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := db.CreateUser(ctx(), "yasmin", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteUser(ctx(), admin.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("deleting the only admin gave %v, want ErrLastAdmin", err)
	}
	if err := db.SetAdmin(ctx(), admin.ID, false); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("demoting the only admin gave %v, want ErrLastAdmin", err)
	}

	// A second administrator makes both operations legitimate.
	if err := db.SetAdmin(ctx(), plain.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx(), admin.ID); err != nil {
		t.Errorf("deleting an admin while another exists: %v", err)
	}

	// A non-administrator is never protected.
	third, err := db.CreateUser(ctx(), "third", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx(), third.ID); err != nil {
		t.Errorf("deleting an ordinary user: %v", err)
	}
}

func TestUsersAndCount(t *testing.T) {
	db := newDB(t)

	n, err := db.CountUsers(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a fresh box has %d users, want 0", n)
	}

	for _, name := range []string{"aaa", "bbb", "ccc"} {
		if _, err := db.CreateUser(ctx(), name, "", "hunter2hunter2"); err != nil {
			t.Fatal(err)
		}
	}

	n, err = db.CountUsers(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("CountUsers = %d, want 3", n)
	}

	users, err := db.Users(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("Users returned %d, want 3", len(users))
	}
	// Identifiers sort by creation time, so listings are in creation order
	// without needing an index on a timestamp.
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	if !sort.StringsAreSorted(ids) {
		t.Error("user ids are not sorted, so listings are not in creation order")
	}
}

func TestNewIDIsSortableAndUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	prev := ""
	for range 1000 {
		id := store.NewID("user")
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true

		if len(id) != len("user_")+26 {
			t.Fatalf("id %q is %d characters, want %d", id, len(id), len("user_")+26)
		}
		// Generated in the same millisecond, ids differ only in their random
		// half and need not be ordered; across milliseconds they must be.
		if prev != "" && id[:10] != prev[:10] && id < prev {
			t.Fatalf("%s sorts before %s despite being generated later", id, prev)
		}
		prev = id
	}
}
