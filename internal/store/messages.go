package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// SaveMessages stores msgs, ignoring any already stored (history sync and
// live delivery overlap), and bumps each chat's last activity, all in one
// transaction. It returns how many were new.
func (s *Store) SaveMessages(ctx context.Context, msgs []wa.Message) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	inserted := 0
	err := s.inTx(ctx, "saving messages", func(tx *sql.Tx) error {
		for _, m := range msgs {
			isNew, err := s.insertMessage(ctx, tx, m)
			if err != nil {
				return err
			}
			if isNew {
				inserted++
			}
			if err := bumpChat(ctx, tx, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// insertMessage seals and inserts m (indexing it for search) unless it's
// already stored; it reports whether it was new.
func (s *Store) insertMessage(ctx context.Context, tx *sql.Tx, m wa.Message) (bool, error) {
	body, codecID, err := s.seal([]byte(m.Text), m.Chat, m.ID, colBody)
	if err != nil {
		return false, err
	}
	var media []byte
	mediaCodec := 0
	if m.Media != nil {
		if media, mediaCodec, err = s.seal(m.Media.Data, m.Chat, m.ID, colMedia); err != nil {
			return false, err
		}
	}
	var replyID, replySender string
	if m.ReplyTo != nil {
		replyID, replySender = m.ReplyTo.ID, m.ReplyTo.Sender
	}
	var rowid int64
	err = tx.QueryRowContext(ctx, `
		INSERT OR IGNORE INTO messages (chat_jid, id, sender_jid, push_name, ts, from_me, kind, body, codec, media, media_codec, reply_id, reply_sender)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING rowid`,
		m.Chat, m.ID, m.Sender, m.PushName, m.Timestamp.Unix(), m.FromMe, m.Kind, body, codecID, media, mediaCodec,
		replyID, replySender).Scan(&rowid)
	switch {
	case errors.Is(err, sql.ErrNoRows): // already stored
		return false, nil
	case err != nil:
		return false, fmt.Errorf("saving message %s: %w", m.ID, err)
	}
	return true, s.index(ctx, tx, rowid, m.Chat, m.ID, m.Text)
}

// bumpChat creates m's chat if needed and moves its last activity forward.
// It runs in the caller's transaction, so a message and its chat update
// commit together.
func bumpChat(ctx context.Context, tx *sql.Tx, m wa.Message) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chats (jid, is_group, last_message_at) VALUES (?, ?, ?)
		ON CONFLICT (jid) DO UPDATE SET last_message_at = max(last_message_at, excluded.last_message_at)`,
		m.Chat, m.IsGroup, m.Timestamp.Unix()); err != nil {
		return fmt.Errorf("updating chat %s: %w", m.Chat, err)
	}
	return nil
}

// Messages returns the newest limit messages in chat, oldest first. A limit
// of 0 or less returns all of them.
func (s *Store) Messages(ctx context.Context, chat string, limit int) ([]wa.Message, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	msgs, err := s.messagesWhere(ctx, chat, `m.chat_jid = ? ORDER BY m.ts DESC, m.id DESC LIMIT ?`, chat, limit)
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	if err := s.attachReactions(ctx, chat, msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// Quotes returns the messages with ids in chat (the originals replies
// quote), keyed by ID. IDs not stored (older than our history) are absent.
func (s *Store) Quotes(ctx context.Context, chat string, ids []string) (map[string]wa.Message, error) {
	out := map[string]wa.Message{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{chat}
	for _, id := range ids {
		args = append(args, id)
	}
	where := `m.chat_jid = ? AND m.id IN (?` + strings.Repeat(`, ?`, len(ids)-1) + `)`
	msgs, err := s.messagesWhere(ctx, chat, where, args...)
	if err != nil {
		return nil, fmt.Errorf("loading quoted messages: %w", err)
	}
	for _, m := range msgs {
		out[m.ID] = m
	}
	return out, nil
}

// messagesWhere reads and decodes chat's messages matching where (over
// messages m), oldest first.
func (s *Store) messagesWhere(ctx context.Context, chat, where string, args ...any) ([]wa.Message, error) {
	rows, err := queryAll(ctx, s.db, `
		SELECT id, sender_jid, push_name, ts, from_me, kind, body, codec, is_group, sender_name, edited_at, deleted, media, media_codec,
		       reply_id, reply_sender FROM (
			SELECT m.*, c.is_group,
			       `+personName("ct", "m.push_name")+` AS sender_name
			FROM messages m
			JOIN chats c ON c.jid = m.chat_jid
			LEFT JOIN contacts ct ON ct.jid = m.sender_jid
			WHERE `+where+`
		) ORDER BY ts, id`, args, scanMessage)
	if err != nil {
		return nil, err
	}
	msgs := make([]wa.Message, len(rows))
	for i, r := range rows {
		if msgs[i], err = s.decodeMessage(chat, r); err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

// storedMessage is a messages row as read, values still sealed.
type storedMessage struct {
	m                    wa.Message
	ts, editedAt         int64
	body, media          []byte
	codec, mediaCodec    int
	replyID, replySender string
}

func scanMessage(rows *sql.Rows) (storedMessage, error) {
	var r storedMessage
	err := rows.Scan(&r.m.ID, &r.m.Sender, &r.m.PushName, &r.ts, &r.m.FromMe, &r.m.Kind, &r.body, &r.codec,
		&r.m.IsGroup, &r.m.SenderName, &r.editedAt, &r.m.Deleted, &r.media, &r.mediaCodec, &r.replyID, &r.replySender)
	return r, err
}

// decodeMessage opens a stored row's sealed values.
func (s *Store) decodeMessage(chat string, r storedMessage) (wa.Message, error) {
	m := r.m
	m.Chat, m.Timestamp, m.Edited = chat, time.Unix(r.ts, 0), r.editedAt > 0
	if r.replyID != "" {
		m.ReplyTo = &wa.Reply{ID: r.replyID, Sender: r.replySender}
	}
	if m.Deleted {
		return m, nil // text and media were wiped
	}
	text, err := s.open(r.body, r.codec, chat, m.ID, colBody)
	if err != nil {
		return m, err
	}
	m.Text = string(text)
	if r.media == nil {
		return m, nil
	}
	data, err := s.open(r.media, r.mediaCodec, chat, m.ID, colMedia)
	if err != nil {
		return m, err
	}
	// An attachment we can't decode (e.g. after a protocol change) loses
	// its download link but must not hide the message, let alone the chat.
	if parsed, perr := wa.ParseMedia(data); perr == nil {
		m.Media = parsed
	}
	return m, nil
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
	rows, err := queryAll(ctx, s.db, `
		SELECT message_id, sender_jid, emoji, codec, from_me FROM reactions
		WHERE chat_jid = ? AND message_id IN (SELECT id FROM messages WHERE chat_jid = ? AND ts >= ?)
		ORDER BY ts`, []any{chat, chat, msgs[0].Timestamp.Unix()},
		func(rows *sql.Rows) (storedReaction, error) {
			var r storedReaction
			err := rows.Scan(&r.msgID, &r.sender, &r.emoji, &r.codec, &r.mine)
			return r, err
		})
	if err != nil {
		return fmt.Errorf("loading reactions: %w", err)
	}
	opened := make([]openedReaction, 0, len(rows))
	for _, r := range rows {
		if _, ok := byID[r.msgID]; !ok {
			continue
		}
		emoji, err := s.open(r.emoji, r.codec, chat, r.msgID, reactionColumn(r.sender))
		if err != nil {
			return err
		}
		opened = append(opened, openedReaction{msgID: r.msgID, emoji: string(emoji), mine: r.mine})
	}
	for id, counts := range countReactions(opened) {
		msgs[byID[id]].Reactions = counts
	}
	return nil
}

type storedReaction struct {
	msgID, sender string
	emoji         []byte
	codec         int
	mine          bool
}

type openedReaction struct {
	msgID, emoji string
	mine         bool
}

// countReactions summarises reactions (in arrival order) per message: most
// popular emoji first, ties in order of first appearance.
func countReactions(rs []openedReaction) map[string][]wa.ReactionCount {
	perMsg := map[string][]wa.ReactionCount{}
	for _, r := range rs {
		counts := perMsg[r.msgID]
		i := slices.IndexFunc(counts, func(c wa.ReactionCount) bool { return c.Emoji == r.emoji })
		if i < 0 {
			counts = append(counts, wa.ReactionCount{Emoji: r.emoji})
			i = len(counts) - 1
		}
		counts[i].Count++
		counts[i].Mine = counts[i].Mine || r.mine
		perMsg[r.msgID] = counts
	}
	for _, counts := range perMsg {
		slices.SortStableFunc(counts, func(a, b wa.ReactionCount) int { return b.Count - a.Count })
	}
	return perMsg
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
