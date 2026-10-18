package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// SaveMessages stores msgs, ignoring any already stored (history sync and
// live delivery overlap), and bumps each chat's last activity. It returns
// how many were new.
func (s *Store) SaveMessages(ctx context.Context, msgs []wa.Message) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("saving messages: %w", err)
	}
	defer rollback(tx)

	inserted := 0
	for _, m := range msgs {
		body, codecID, err := s.seal([]byte(m.Text), m.Chat, m.ID, colBody)
		if err != nil {
			return 0, err
		}
		var media []byte
		mediaCodec := 0
		if m.Media != nil {
			if media, mediaCodec, err = s.seal(m.Media.Data, m.Chat, m.ID, colMedia); err != nil {
				return 0, err
			}
		}
		var rowid int64
		err = tx.QueryRowContext(ctx, `
			INSERT OR IGNORE INTO messages (chat_jid, id, sender_jid, push_name, ts, from_me, kind, body, codec, media, media_codec)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING rowid`,
			m.Chat, m.ID, m.Sender, m.PushName, m.Timestamp.Unix(), m.FromMe, m.Kind, body, codecID, media, mediaCodec).Scan(&rowid)
		switch {
		case errors.Is(err, sql.ErrNoRows): // already stored
		case err != nil:
			return 0, fmt.Errorf("saving message %s: %w", m.ID, err)
		default:
			inserted++
			if err := s.index(ctx, tx, rowid, m.Chat, m.ID, m.Text); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chats (jid, is_group, last_message_at) VALUES (?, ?, ?)
			ON CONFLICT (jid) DO UPDATE SET last_message_at = max(last_message_at, excluded.last_message_at)`,
			m.Chat, m.IsGroup, m.Timestamp.Unix()); err != nil {
			return 0, fmt.Errorf("updating chat %s: %w", m.Chat, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("saving messages: %w", err)
	}
	return inserted, nil
}

// Messages returns the newest limit messages in chat, oldest first. A limit
// of 0 or less returns all of them.
func (s *Store) Messages(ctx context.Context, chat string, limit int) ([]wa.Message, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sender_jid, push_name, ts, from_me, kind, body, codec, is_group, sender_name, edited_at, deleted, media, media_codec FROM (
			SELECT m.*, c.is_group,
			       COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(m.push_name, ''), ct.business_name, '') AS sender_name
			FROM messages m
			JOIN chats c ON c.jid = m.chat_jid
			LEFT JOIN contacts ct ON ct.jid = m.sender_jid
			WHERE m.chat_jid = ? ORDER BY m.ts DESC, m.id DESC LIMIT ?
		) ORDER BY ts, id`, chat, limit)
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	defer rows.Close()
	var msgs []wa.Message
	for rows.Next() {
		m := wa.Message{Chat: chat}
		var ts int64
		var body []byte
		var codecID int
		var editedAt int64
		var media []byte
		var mediaCodec int
		if err := rows.Scan(&m.ID, &m.Sender, &m.PushName, &ts, &m.FromMe, &m.Kind, &body, &codecID, &m.IsGroup, &m.SenderName, &editedAt, &m.Deleted, &media, &mediaCodec); err != nil {
			return nil, fmt.Errorf("loading messages: %w", err)
		}
		m.Timestamp, m.Edited = time.Unix(ts, 0), editedAt > 0
		if !m.Deleted {
			text, err := s.open(body, codecID, chat, m.ID, colBody)
			if err != nil {
				return nil, err
			}
			m.Text = string(text)
			if media != nil {
				data, err := s.open(media, mediaCodec, chat, m.ID, colMedia)
				if err != nil {
					return nil, err
				}
				// An attachment we can't decode (e.g. after a protocol
				// change) loses its download link but must not hide the
				// message, let alone the chat.
				if parsed, perr := wa.ParseMedia(data); perr == nil {
					m.Media = parsed
				}
			}
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	if err := s.attachReactions(ctx, chat, msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// attachReactions fills in each message's reaction summary. Emoji are
// sealed, so they're opened and counted here rather than grouped in SQL.
func (s *Store) attachReactions(ctx context.Context, chat string, msgs []wa.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := make(map[string]int, len(msgs))
	for i, m := range msgs {
		byID[m.ID] = i
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, sender_jid, emoji, codec, from_me, ts FROM reactions
		WHERE chat_jid = ? AND message_id IN (SELECT id FROM messages WHERE chat_jid = ? AND ts >= ?)
		ORDER BY ts`,
		chat, chat, msgs[0].Timestamp.Unix())
	if err != nil {
		return fmt.Errorf("loading reactions: %w", err)
	}
	defer rows.Close()
	type agg struct {
		count int
		mine  bool
		order int // first seen, for ties
	}
	perMsg := map[string]map[string]*agg{}
	seen := 0
	for rows.Next() {
		var id, sender string
		var sealed []byte
		var codec int
		var mine bool
		var ts int64
		if err := rows.Scan(&id, &sender, &sealed, &codec, &mine, &ts); err != nil {
			return fmt.Errorf("loading reactions: %w", err)
		}
		if _, ok := byID[id]; !ok {
			continue
		}
		emoji, err := s.open(sealed, codec, chat, id, reactionColumn(sender))
		if err != nil {
			return err
		}
		m := perMsg[id]
		if m == nil {
			m = map[string]*agg{}
			perMsg[id] = m
		}
		a := m[string(emoji)]
		if a == nil {
			seen++
			a = &agg{order: seen}
			m[string(emoji)] = a
		}
		a.count++
		a.mine = a.mine || mine
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("loading reactions: %w", err)
	}
	for id, m := range perMsg {
		var rcs []wa.ReactionCount
		var order []int
		for emoji, a := range m {
			rcs = append(rcs, wa.ReactionCount{Emoji: emoji, Count: a.count, Mine: a.mine})
			order = append(order, a.order)
		}
		idx := make([]int, len(rcs))
		for i := range idx {
			idx[i] = i
		}
		slices.SortFunc(idx, func(a, b int) int {
			if rcs[a].Count != rcs[b].Count {
				return rcs[b].Count - rcs[a].Count
			}
			return order[a] - order[b]
		})
		sorted := make([]wa.ReactionCount, len(rcs))
		for i, j := range idx {
			sorted[i] = rcs[j]
		}
		msgs[byID[id]].Reactions = sorted
	}
	return nil
}

// EditMessage replaces a message's text with a newer edit. It reports
// whether the message was found (an edit can arrive before its original).
// Text goes through seal like every other write.
func (s *Store) EditMessage(ctx context.Context, e wa.MessageEdited) (bool, error) {
	body, codecID, err := s.seal([]byte(e.Text), e.Chat, e.ID, colBody)
	if err != nil {
		return false, err
	}
	found := false
	err = s.inTx(ctx, "editing message", func(tx *sql.Tx) error {
		var rowid int64
		err := tx.QueryRowContext(ctx, `
			UPDATE messages SET body = ?, codec = ?, kind = ?, edited_at = ?
			WHERE chat_jid = ? AND id = ? AND deleted = 0 AND edited_at <= ? RETURNING rowid`,
			body, codecID, e.Kind, e.At.Unix(), e.Chat, e.ID, e.At.Unix()).Scan(&rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return s.index(ctx, tx, rowid, e.Chat, e.ID, e.Text)
	})
	return found, err
}

// DeleteMessage applies a deletion. "For everyone" wipes the text (and
// reactions) but keeps a placeholder row, as WhatsApp shows "This message
// was deleted"; "for me" removes the message entirely.
func (s *Store) DeleteMessage(ctx context.Context, d wa.MessageDeleted) error {
	return s.inTx(ctx, "deleting message", func(tx *sql.Tx) error {
		if err := unindex(ctx, tx, d.Chat, d.ID); err != nil {
			return err
		}
		if d.ForMe {
			if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE chat_jid = ? AND id = ?`, d.Chat, d.ID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `
			UPDATE messages SET body = x'', codec = 0, media = NULL, media_codec = 0, deleted = 1 WHERE chat_jid = ? AND id = ?`,
			d.Chat, d.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE chat_jid = ? AND message_id = ?`, d.Chat, d.ID)
		return err
	})
}

// SaveReactions applies reactions in order; an empty emoji removes the
// sender's reaction. An older reaction never replaces a newer one.
func (s *Store) SaveReactions(ctx context.Context, rs []wa.Reaction) error {
	return s.inTx(ctx, "saving reactions", func(tx *sql.Tx) error {
		for _, r := range rs {
			if r.Emoji == "" {
				if _, err := tx.ExecContext(ctx, `
					DELETE FROM reactions WHERE chat_jid = ? AND message_id = ? AND sender_jid = ? AND ts <= ?`,
					r.Chat, r.MessageID, r.Sender, r.At.Unix()); err != nil {
					return err
				}
				continue
			}
			sealed, codec, err := s.seal([]byte(r.Emoji), r.Chat, r.MessageID, reactionColumn(r.Sender))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO reactions (chat_jid, message_id, sender_jid, from_me, emoji, codec, ts) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (chat_jid, message_id, sender_jid) DO UPDATE SET
					emoji = excluded.emoji, codec = excluded.codec, ts = excluded.ts, from_me = excluded.from_me
				WHERE excluded.ts >= reactions.ts`,
				r.Chat, r.MessageID, r.Sender, r.FromMe, sealed, codec, r.At.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkRead records that chat was read up to at. Read state only moves
// forward.
func (s *Store) MarkRead(ctx context.Context, chat string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE chats SET last_read_at = max(last_read_at, ?) WHERE jid = ?`, at.Unix(), chat); err != nil {
		return fmt.Errorf("marking %s read: %w", chat, err)
	}
	return nil
}

// UnreadMessages returns chat's incoming messages newer than its read
// state (ID, sender and time only), oldest first.
func (s *Store) UnreadMessages(ctx context.Context, chat string) ([]wa.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.sender_jid, m.ts, c.is_group FROM messages m JOIN chats c ON c.jid = m.chat_jid
		WHERE m.chat_jid = ? AND m.from_me = 0 AND m.deleted = 0 AND m.ts > c.last_read_at
		ORDER BY m.ts, m.id`, chat)
	if err != nil {
		return nil, fmt.Errorf("loading unread: %w", err)
	}
	defer rows.Close()
	var msgs []wa.Message
	for rows.Next() {
		m := wa.Message{Chat: chat}
		var ts int64
		if err := rows.Scan(&m.ID, &m.Sender, &ts, &m.IsGroup); err != nil {
			return nil, fmt.Errorf("loading unread: %w", err)
		}
		m.Timestamp = time.Unix(ts, 0)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading unread: %w", err)
	}
	return msgs, nil
}

// SetUnreadFromPhone sets chat's read state so that its n newest incoming
// messages are unread, matching the phone's count from history sync. Unlike
// MarkRead this may move read state backwards: the phone is authoritative.
func (s *Store) SetUnreadFromPhone(ctx context.Context, chat string, n int) error {
	var q string
	var args []any
	if n <= 0 {
		q = `UPDATE chats SET last_read_at = max(last_message_at,
			coalesce((SELECT max(ts) FROM messages WHERE chat_jid = ?1), 0)) WHERE jid = ?1`
		args = []any{chat}
	} else {
		// Just before the nth newest incoming message (or 0 if fewer are stored).
		q = `UPDATE chats SET last_read_at = coalesce((
			SELECT ts - 1 FROM messages WHERE chat_jid = ?1 AND from_me = 0
			ORDER BY ts DESC, id DESC LIMIT 1 OFFSET ?2), 0) WHERE jid = ?1`
		args = []any{chat, n - 1}
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("setting unread for %s: %w", chat, err)
	}
	return nil
}
