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
	collect := func(tx *sql.Tx, q string, args ...any) error {
		rows, err := tx.QueryContext(ctx, q+` RETURNING chat_jid, id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ref MessageRef
			if err := rows.Scan(&ref.Chat, &ref.ID); err != nil {
				return err
			}
			removed = append(removed, ref)
		}
		return rows.Err()
	}
	err := s.inTx(ctx, "pruning messages", func(tx *sql.Tx) error {
		if r.KeepDays > 0 {
			cutoff := now.AddDate(0, 0, -r.KeepDays).Unix()
			if err := collect(tx, `DELETE FROM messages WHERE ts < ?`, cutoff); err != nil {
				return err
			}
		}
		if r.MaxPerChat > 0 {
			if err := collect(tx, `
				DELETE FROM messages WHERE rowid IN (
					SELECT rowid FROM (
						SELECT rowid, row_number() OVER (PARTITION BY chat_jid ORDER BY ts DESC, id DESC) AS n
						FROM messages
					) WHERE n > ?
				)`, r.MaxPerChat); err != nil {
				return err
			}
		}
		if len(removed) == 0 {
			return nil
		}
		// Reactions and index entries of pruned messages go with them.
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid NOT IN (SELECT rowid FROM messages)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE NOT EXISTS (
			SELECT 1 FROM messages m WHERE m.chat_jid = reactions.chat_jid AND m.id = reactions.message_id)`)
		return err
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}
