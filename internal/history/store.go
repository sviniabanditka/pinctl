package history

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/sviniabanditka/pinctl/internal/config"
)

// currentSchemaVersion is the schema version this build knows how to read
// and write. Bump it, and add a case to migrate, whenever samples/meta's
// shape changes.
const currentSchemaVersion = 1

// Store is an open value-history database: Record feeds it from the
// devicebus, Query and DeleteDevice serve reads and device-deletion
// cleanup, and Retain enforces the retention window. Safe for concurrent
// use: SQLite's WAL mode lets Query run against the database while Record
// holds a write transaction (docs/16's "Storage").
type Store struct {
	db    *sql.DB
	cfg   config.History
	clock Clock
}

// Open opens (creating if absent) the SQLite database at path, applies
// docs/16's pragmas (WAL, synchronous=NORMAL, busy_timeout=5000,
// auto_vacuum=incremental), and migrates its schema. A database whose
// meta.schema_version is higher than currentSchemaVersion — written by a
// newer pinctl than this one — is refused rather than risked: Open
// returns an error, and the caller (cmd/pinctl) logs it and runs with
// history disabled, per docs/16's failure-mode table. Open does no I/O
// beyond the database file itself; call Record to start recording.
func Open(path string, cfg config.History) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=auto_vacuum(2)" // 2 = incremental, per docs/16

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("history: opening %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: opening %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, cfg: cfg, clock: realClock{}}, nil
}

// Close closes the underlying database handle. Safe to call once, after
// Record's goroutine has returned (Record returns as soon as its ctx is
// done, so callers typically wait on that before calling Close).
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate ensures the schema exists at currentSchemaVersion, creating it
// fresh on an empty database, and refuses to proceed against a database
// stamped with a schema_version newer than this build understands
// (docs/16's failure-mode table: "refuses to open (log + run without
// history) rather than corrupting anything"). There is only ever one
// schema version so far, but schema_version is read and compared for
// real, not just written once, so a genuine migration step slots in here
// (a "case 1: ... case 2: ..." style upgrade path) whenever the schema
// next changes.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT) STRICT`); err != nil {
		return fmt.Errorf("history: creating meta table: %w", err)
	}

	var versionStr string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&versionStr)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return initSchema(db)
	case err != nil:
		return fmt.Errorf("history: reading schema_version: %w", err)
	}

	version, convErr := strconv.Atoi(versionStr)
	if convErr != nil {
		return fmt.Errorf("history: meta.schema_version %q is not a number", versionStr)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("history: database schema_version %d is newer than this build supports (%d)", version, currentSchemaVersion)
	}
	// version < currentSchemaVersion would run forward migrations here, in
	// order, as they're added. currentSchemaVersion is 1 — the only
	// version that has ever existed — so there is nothing to do yet.
	return nil
}

// initSchema creates samples and its index on a fresh (no schema_version
// row) database and stamps it at currentSchemaVersion, per docs/16's
// "Storage".
func initSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS samples (
			device_id TEXT    NOT NULL,
			ts        INTEGER NOT NULL,
			value     REAL    NOT NULL
		) STRICT`); err != nil {
		return fmt.Errorf("history: creating samples table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS samples_device_ts ON samples (device_id, ts)`); err != nil {
		return fmt.Errorf("history: creating samples index: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, strconv.Itoa(currentSchemaVersion)); err != nil {
		return fmt.Errorf("history: writing schema_version: %w", err)
	}
	return nil
}
