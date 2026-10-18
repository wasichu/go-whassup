// Package sqlite opens the app's SQLite databases with one shared set of
// settings. It is the only place that knows the driver and DSN syntax.
// See docs/storage.md.
package sqlite

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Driver is the database/sql driver name registered by modernc.org/sqlite.
const Driver = "sqlite"

// Open opens (creating if needed) the database at path. The file is created
// user-only, since our databases hold keys and message content; SQLite
// gives its -wal/-shm files the same permissions.
//
// _txlock=immediate + WAL + busy_timeout is what whatsmeow's own SQLite
// users run: it avoids "database is locked" under concurrent writers
// (including a second wa-tui process) without the nested-transaction
// deadlocks of a one-connection pool.
func Open(path string) (*sql.DB, error) {
	if err := createPrivate(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open(Driver, "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return db, nil
}

func createPrivate(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	// Tighten files created by older builds.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("securing %s: %w", path, err)
	}
	return nil
}
