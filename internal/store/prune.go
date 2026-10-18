package store

import (
	"context"
	"database/sql"
	"time"
)

// Retention limits what is kept. Zero values mean no limit.
type Retention struct {
	KeepDays   int
	MaxPerChat int
}

// MessageRef identifies a stored message.
type MessageRef struct{ Chat, ID string }

// Prune deletes messages outside r, relative to now, and returns the ones
// it removed (so callers can clean up anything kept outside the database,
// like cached media).
func (s *Store) Prune(ctx context.Context, r Retention, now time.Time) ([]MessageRef, error) {
	var removed []MessageRef
	err := s.inTx(ctx, "pruning messages", func(tx *sql.Tx) error {
		if r.KeepDays > 0 {
			refs, err := deleteReturning(ctx, tx, `DELETE FROM messages WHERE ts < ?`, now.AddDate(0, 0, -r.KeepDays).Unix())
			if err != nil {
				return err
			}
			removed = append(removed, refs...)
		}
		if r.MaxPerChat > 0 {
			refs, err := deleteReturning(ctx, tx, `
				DELETE FROM messages WHERE rowid IN (
					SELECT rowid FROM (
						SELECT rowid, row_number() OVER (PARTITION BY chat_jid ORDER BY ts DESC, id DESC) AS n
						FROM messages
					) WHERE n > ?
				)`, r.MaxPerChat)
			if err != nil {
				return err
			}
			removed = append(removed, refs...)
		}
		if len(removed) == 0 {
			return nil
		}
		return dropOrphans(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// deleteReturning runs a DELETE on messages and returns what it removed.
func deleteReturning(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]MessageRef, error) {
	return queryAll(ctx, tx, query+` RETURNING chat_jid, id`, args, func(rows *sql.Rows) (MessageRef, error) {
		var ref MessageRef
		err := rows.Scan(&ref.Chat, &ref.ID)
		return ref, err
	})
}

// dropOrphans removes index entries and reactions whose message is gone.
func dropOrphans(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid NOT IN (SELECT rowid FROM messages)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE NOT EXISTS (
		SELECT 1 FROM messages m WHERE m.chat_jid = reactions.chat_jid AND m.id = reactions.message_id)`)
	return err
}
