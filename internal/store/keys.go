package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wasichu/go-whassup/internal/crypt"
)

// StoredKey is a row of the keys table: the data key, wrapped.
type StoredKey struct {
	ID        int
	Alg       string
	Source    string
	Salt      []byte
	KDF       *crypt.KDFParams // nil for keyring keys (the KEK is random)
	Wrapped   []byte
	CreatedAt time.Time
	// Finished reports that encrypting under this key completed (every
	// value re-sealed, index emptied, file compacted).
	Finished bool
}

// Key returns the current (newest) key, or nil if messages aren't encrypted.
func (s *Store) Key(ctx context.Context) (*StoredKey, error) {
	var k StoredKey
	var kdf string
	var created, finished int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, alg, source, salt, kdf, wrapped, created_at, finished_at FROM keys ORDER BY id DESC LIMIT 1`).
		Scan(&k.ID, &k.Alg, &k.Source, &k.Salt, &kdf, &k.Wrapped, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading key: %w", err)
	}
	if kdf != "" {
		k.KDF = &crypt.KDFParams{}
		if err := json.Unmarshal([]byte(kdf), k.KDF); err != nil {
			return nil, fmt.Errorf("reading key parameters: %w", err)
		}
	}
	k.CreatedAt, k.Finished = time.Unix(created, 0), finished > 0
	return &k, nil
}

// AddKey stores a new wrapped data key and returns its ID. It also empties
// the search index in the same transaction: the index is a plaintext copy
// of message words, and it must not outlive an enable that gets
// interrupted before FinishEncrypting.
func (s *Store) AddKey(ctx context.Context, k StoredKey) (int, error) {
	var kdf string
	if k.KDF != nil {
		b, err := json.Marshal(k.KDF)
		if err != nil {
			return 0, fmt.Errorf("encoding key parameters: %w", err)
		}
		kdf = string(b)
	}
	var id int
	err := s.inTx(ctx, "storing key", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts`); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO keys (alg, source, salt, kdf, wrapped, created_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
			k.Alg, k.Source, k.Salt, kdf, k.Wrapped, time.Now().Unix()).Scan(&id)
	})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.keyed = true
	s.mu.Unlock()
	return id, nil
}

// Unlock makes c available for reading values it sealed, and (if write)
// for sealing new ones.
func (s *Store) Unlock(c Codec, write bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codecs[c.ID()] = c
	if write {
		s.codec = c
	}
}

// WritePlaintext makes new values be stored unencrypted (when turning
// encryption off); keys stay registered for reading until Decrypted.
func (s *Store) WritePlaintext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codec = Plaintext{}
}

// Pending counts messages and reactions not yet sealed by the current codec.
func (s *Store) Pending(ctx context.Context) (int, error) {
	cur := s.current().ID()
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM messages WHERE codec != ?1 OR (media IS NOT NULL AND media_codec != ?1))
		     + (SELECT count(*) FROM reactions WHERE codec != ?1)`, cur).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting values to re-seal: %w", err)
	}
	return n, nil
}

// ResealError names a value that couldn't be re-sealed (e.g. corrupted, or
// sealed under a key that isn't unlocked). Re-sealing can't make progress
// past it, so callers must stop rather than retry.
type ResealError struct {
	Chat, ID string
	Err      error
}

func (e *ResealError) Error() string {
	return fmt.Sprintf("re-sealing message %s in %s: %v", e.ID, e.Chat, e.Err)
}

func (e *ResealError) Unwrap() error { return e.Err }

// ResealBatch re-seals up to n messages (and then reactions) whose values
// aren't sealed by the current codec, in one short transaction (so live
// writes aren't held up), and returns how many it did. Call until it
// returns 0; it's safe to stop and resume at any point, including across
// restarts. After each batch the WAL is checkpointed so old page images
// (which may hold plaintext) don't linger in the -wal file. A *ResealError
// means a value can't be opened: stop, don't retry.
func (s *Store) ResealBatch(ctx context.Context, n int) (int, error) {
	done, err := s.resealMessages(ctx, n)
	if err == nil && done == 0 {
		done, err = s.resealReactions(ctx, n)
	}
	if err != nil {
		return 0, err
	}
	if done > 0 {
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return 0, fmt.Errorf("checkpointing: %w", err)
		}
	}
	return done, nil
}

// resealValue opens v with the codec that sealed it and seals it with the
// current one. Failing to open is a *ResealError: retrying can't help.
func (s *Store) resealValue(v []byte, codec int, chat, id, column string) ([]byte, error) {
	plain, err := s.open(v, codec, chat, id, column)
	if err != nil {
		return nil, &ResealError{Chat: chat, ID: id, Err: err}
	}
	sealed, _, err := s.seal(plain, chat, id, column)
	return sealed, err
}

// resealRows runs one batch over a table in a single short transaction:
// load the rows that need it (scan), then rewrite each (apply).
func resealRows[T any](ctx context.Context, s *Store, what, query string, args []any,
	scan func(*sql.Rows) (T, error), apply func(*sql.Tx, T) error,
) (int, error) {
	done := 0
	err := s.inTx(ctx, what, func(tx *sql.Tx) error {
		batch, err := queryAll(ctx, tx, query, args, scan)
		if err != nil {
			return err
		}
		for _, r := range batch {
			if err := apply(tx, r); err != nil {
				return err
			}
		}
		done = len(batch)
		return nil
	})
	var rerr *ResealError
	if errors.As(err, &rerr) {
		return 0, rerr
	}
	return done, err
}

type messageValues struct {
	rowid             int64
	chat, id          string
	body, media       []byte
	codec, mediaCodec int
}

func (s *Store) resealMessages(ctx context.Context, n int) (int, error) {
	cur := s.current().ID()
	return resealRows(ctx, s, "re-sealing messages", `
		SELECT rowid, chat_jid, id, body, codec, media, media_codec FROM messages
		WHERE codec != ?1 OR (media IS NOT NULL AND media_codec != ?1) LIMIT ?2`, []any{cur, n},
		func(rows *sql.Rows) (messageValues, error) {
			var r messageValues
			err := rows.Scan(&r.rowid, &r.chat, &r.id, &r.body, &r.codec, &r.media, &r.mediaCodec)
			return r, err
		},
		func(tx *sql.Tx, r messageValues) error {
			body, err := s.resealValue(r.body, r.codec, r.chat, r.id, colBody)
			if err != nil {
				return err
			}
			var media []byte
			mediaCodec := 0
			if r.media != nil {
				if media, err = s.resealValue(r.media, r.mediaCodec, r.chat, r.id, colMedia); err != nil {
					return err
				}
				mediaCodec = cur
			}
			_, err = tx.ExecContext(ctx, `UPDATE messages SET body = ?, codec = ?, media = ?, media_codec = ? WHERE rowid = ?`,
				body, cur, media, mediaCodec, r.rowid)
			return err
		})
}

type reactionValue struct {
	rowid             int64
	chat, msg, sender string
	emoji             []byte
	codec             int
}

func (s *Store) resealReactions(ctx context.Context, n int) (int, error) {
	cur := s.current().ID()
	return resealRows(ctx, s, "re-sealing reactions", `
		SELECT rowid, chat_jid, message_id, sender_jid, emoji, codec FROM reactions WHERE codec != ? LIMIT ?`, []any{cur, n},
		func(rows *sql.Rows) (reactionValue, error) {
			var r reactionValue
			err := rows.Scan(&r.rowid, &r.chat, &r.msg, &r.sender, &r.emoji, &r.codec)
			return r, err
		},
		func(tx *sql.Tx, r reactionValue) error {
			emoji, err := s.resealValue(r.emoji, r.codec, r.chat, r.msg, reactionColumn(r.sender))
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE reactions SET emoji = ?, codec = ? WHERE rowid = ?`, emoji, cur, r.rowid)
			return err
		})
}

// FinishEncrypting runs once every value is sealed: it rewrites the file so
// freed pages that held old plaintext are gone. (The search index was
// already emptied by AddKey.)
func (s *Store) FinishEncrypting(ctx context.Context) error {
	if n, err := s.Pending(ctx); err != nil || n > 0 {
		return errors.Join(err, fmt.Errorf("%d values still to encrypt", n))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM messages_fts`); err != nil {
		return fmt.Errorf("emptying search index: %w", err) // defensive; AddKey emptied it
	}
	if err := s.compact(ctx); err != nil {
		return err
	}
	// Only now is it finished; a crash before this line repeats the step.
	if _, err := s.db.ExecContext(ctx, `UPDATE keys SET finished_at = ? WHERE id = ?`, time.Now().Unix(), s.current().ID()); err != nil {
		return fmt.Errorf("recording encryption finished: %w", err)
	}
	return nil
}

// FinishDecrypting runs once every value is plaintext again: it rebuilds the
// search index, forgets the keys and compacts the file.
func (s *Store) FinishDecrypting(ctx context.Context) error {
	if n, err := s.Pending(ctx); err != nil || n > 0 {
		return errors.Join(err, fmt.Errorf("%d values still to decrypt", n))
	}
	err := s.inTx(ctx, "rebuilding search index", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages_fts (rowid, text, chat_jid, msg_id)
			SELECT rowid, CAST(body AS TEXT), chat_jid, id FROM messages
			WHERE codec = 0 AND deleted = 0 AND length(body) > 0`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM keys`)
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.keyed = false
	for id := range s.codecs {
		if id != 0 {
			delete(s.codecs, id)
		}
	}
	s.mu.Unlock()
	return s.compact(ctx)
}

// compact checkpoints the WAL and VACUUMs, so pages that held data before
// a re-seal are rewritten (secure_delete zeroes freed pages as well).
func (s *Store) compact(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("compacting: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing: %w", err)
	}
	return nil
}
