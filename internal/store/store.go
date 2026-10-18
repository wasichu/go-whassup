// Package store owns go-whassup's chat and message database (messages.db).
// whatsmeow persists only protocol state; message content lives here.
// See docs/storage.md.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/wasichu/go-whassup/internal/sqlite"
)

// Store is the message database.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex  // guards the fields below (codecs change at unlock/enable)
	codec  Codec         // seals new values
	codecs map[int]Codec // opens stored values, by Codec.ID
	keyed  bool          // a key exists: the search index may be stale or must not grow
}

// current returns the codec that seals new values.
func (s *Store) current() Codec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.codec
}

// Open opens (creating and migrating if needed) the database at path. A nil
// codec means Plaintext.
func Open(ctx context.Context, path string, codec Codec) (*Store, error) {
	if codec == nil {
		codec = Plaintext{}
	}
	db, err := sqlite.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening messages db: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	st := &Store{
		db:     db,
		codec:  codec,
		codecs: map[int]Codec{Plaintext{}.ID(): Plaintext{}, codec.ID(): codec},
	}
	var keys int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM keys`).Scan(&keys); err != nil {
		return nil, errors.Join(fmt.Errorf("reading keys: %w", err), db.Close())
	}
	st.keyed = keys > 0
	return st, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing messages db: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, what string, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer rollback(tx)
	if err := fn(tx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}
