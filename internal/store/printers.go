package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	// The queue_name column exists to hold a name CUPS will accept, so the rules
	// governing it belong to CUPS. Importing them is more honest than restating
	// them here and letting the two drift apart.
	"github.com/mhd64real/printer-cycle/internal/ipp"
)

// Printer is a queue printer-cycle created in CUPS.
type Printer struct {
	ID string

	// QueueName is the name CUPS knows it by: sanitised, no spaces.
	QueueName string

	// DisplayName is what the user typed and what the user sees.
	DisplayName string

	DeviceURI  string
	PPDName    string
	Location   string
	Restricted bool
	CreatedAt  time.Time
	CreatedBy  string
}

// PrinterSpec describes a printer to record.
type PrinterSpec struct {
	DisplayName string
	DeviceURI   string
	PPDName     string
	Location    string
	CreatedBy   string
}

// CreatePrinter records a printer and reserves its queue name.
//
// The queue name is derived from what the user typed, since CUPS will not accept
// spaces, and made unique by appending a number if something already holds it.
// Two printers called "Office Laser" is an ordinary thing for a household to
// want, and refusing the second would be a strange place to draw a line.
func (db *DB) CreatePrinter(ctx context.Context, spec PrinterSpec) (Printer, error) {
	display := strings.TrimSpace(spec.DisplayName)
	if display == "" {
		return Printer{}, fmt.Errorf("store: printer has no name")
	}
	if spec.DeviceURI == "" {
		return Printer{}, fmt.Errorf("store: printer %q has no device uri", display)
	}

	base := ipp.SanitiseName(display)
	if base == "" {
		// Nothing survived sanitising, which happens with a name written
		// entirely in a script CUPS queue names cannot carry.
		base = "printer"
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Printer{}, err
	}
	defer tx.Rollback()

	queue, err := uniqueQueueName(ctx, tx, base)
	if err != nil {
		return Printer{}, err
	}

	p := Printer{
		ID:          NewID("prn"),
		QueueName:   queue,
		DisplayName: display,
		DeviceURI:   spec.DeviceURI,
		PPDName:     spec.PPDName,
		Location:    spec.Location,
		CreatedBy:   spec.CreatedBy,
	}

	var createdBy any
	if p.CreatedBy != "" {
		createdBy = p.CreatedBy
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO printers (id, queue_name, display_name, device_uri, ppd_name, location, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.QueueName, p.DisplayName, p.DeviceURI, p.PPDName, p.Location, createdBy)
	if err != nil {
		return Printer{}, err
	}
	if err := tx.Commit(); err != nil {
		return Printer{}, err
	}
	return db.Printer(ctx, p.ID)
}

// uniqueQueueName finds a name nothing else holds.
func uniqueQueueName(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		if err := ipp.ValidPrinterName(candidate); err != nil {
			return "", err
		}

		var taken int
		err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM printers WHERE queue_name = ?`, candidate).Scan(&taken)
		if err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("store: cannot find a free queue name based on %q", base)
}

const printerSelect = `SELECT id, queue_name, display_name, device_uri, ppd_name, location,
                              restricted, created_at, coalesce(created_by, '')
                         FROM printers`

// Printer returns one printer by id.
func (db *DB) Printer(ctx context.Context, id string) (Printer, error) {
	return scanPrinter(db.QueryRowContext(ctx, printerSelect+` WHERE id = ?`, id))
}

// PrinterByQueue returns one printer by its CUPS queue name.
func (db *DB) PrinterByQueue(ctx context.Context, queue string) (Printer, error) {
	return scanPrinter(db.QueryRowContext(ctx, printerSelect+` WHERE queue_name = ?`, queue))
}

// Printers lists every printer, oldest first.
func (db *DB) Printers(ctx context.Context) ([]Printer, error) {
	rows, err := db.QueryContext(ctx, printerSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var printers []Printer
	for rows.Next() {
		p, err := scanPrinterRow(rows)
		if err != nil {
			return nil, err
		}
		printers = append(printers, p)
	}
	return printers, rows.Err()
}

// DeletePrinter removes a printer and, by cascade, its jobs.
func (db *DB) DeletePrinter(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM printers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// SetPrinterRestricted turns per-user access on or off for a printer.
func (db *DB) SetPrinterRestricted(ctx context.Context, id string, restricted bool) error {
	res, err := db.ExecContext(ctx,
		`UPDATE printers SET restricted = ? WHERE id = ?`, boolToInt(restricted), id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

func scanPrinter(row rowScanner) (Printer, error) {
	p, err := scanPrinterRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Printer{}, ErrNotFound
	}
	return p, err
}

func scanPrinterRow(row rowScanner) (Printer, error) {
	var (
		p          Printer
		restricted int
		created    string
	)
	err := row.Scan(&p.ID, &p.QueueName, &p.DisplayName, &p.DeviceURI, &p.PPDName,
		&p.Location, &restricted, &created, &p.CreatedBy)
	if err != nil {
		return Printer{}, err
	}
	p.Restricted = restricted == 1
	p.CreatedAt = parseTime(created)
	return p, nil
}
