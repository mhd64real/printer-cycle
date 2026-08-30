package store

import (
	"context"
	"errors"
	"time"
)

// DashboardConnectorID is the connector the dashboard authenticates as.
//
// The dashboard is a connector like any other and gets no privileged path into
// core. It holds every scope because it is the interface through which a person
// does everything, not because it is trusted differently.
const DashboardConnectorID = "dashboard"

// Bootstrap prepares a fresh box for first use and returns a setup token.
//
// # The problem this solves
//
// The dashboard is a connector, and connectors authenticate with a key core
// already knows. On a brand new box core knows no keys, and there is no
// administrator who could approve one, because creating the administrator is
// what the dashboard is for. Something has to break that circle, and it has to
// be something only a person with access to the machine can obtain.
//
// So core writes a single-use token where only someone with access to the box
// can read it: the console, and a file readable by nobody else.
//
// Returns issued=false, and no token, once any user account exists. From that
// point the box has an administrator and new connectors are enrolled the normal
// way, through the dashboard.
func (db *DB) Bootstrap(ctx context.Context) (token string, issued bool, err error) {
	users, err := db.CountUsers(ctx)
	if err != nil {
		return "", false, err
	}
	if users > 0 {
		return "", false, nil
	}

	// The dashboard record may already exist from a previous start that nobody
	// finished setting up.
	if _, err := db.Connector(ctx, DashboardConnectorID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return "", false, err
		}
		if _, err := db.CreateConnector(ctx, DashboardConnectorID, "Dashboard", KnownScopes()); err != nil {
			return "", false, err
		}
	}

	// Any token from an earlier start is discarded.
	//
	// Not a choice so much as a consequence: only a hash of a token is stored,
	// so a previous one cannot be printed again. Issuing a fresh token each
	// start and invalidating the old one is the only behaviour that leaves
	// exactly one valid token in existence, which is also the one on screen.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM connector_enrolments WHERE connector_id = ? AND used_at IS NULL`,
		DashboardConnectorID); err != nil {
		return "", false, err
	}

	token, err = db.NewEnrolmentToken(ctx, DashboardConnectorID, 24*time.Hour)
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

// NeedsSetup reports whether this box has never been set up.
func (db *DB) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := db.CountUsers(ctx)
	return n == 0, err
}
