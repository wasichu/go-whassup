// Package store owns go-whassup's chat and message database (messages.db).
// whatsmeow persists only protocol state; message content lives here.
// See docs/storage.md.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/sqlite"
	"github.com/wasichu/go-whassup/internal/wa"
)

// Store is the message database.
type Store struct {
	db     *sql.DB
	codec  Codec         // seals new bodies
	codecs map[int]Codec // opens stored bodies, by Codec.ID
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
	return &Store{
		db:     db,
		codec:  codec,
		codecs: map[int]Codec{Plaintext{}.ID(): Plaintext{}, codec.ID(): codec},
	}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing messages db: %w", err)
	}
	return nil
}

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
		body, codecID, err := s.sealBody(m.Text)
		if err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO messages (chat_jid, id, sender_jid, push_name, ts, from_me, kind, body, codec)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Chat, m.ID, m.Sender, m.PushName, m.Timestamp.Unix(), m.FromMe, m.Kind, body, codecID)
		if err != nil {
			return 0, fmt.Errorf("saving message %s: %w", m.ID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			inserted++
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

// SaveChats records chat metadata. An empty name never overwrites a known one.
func (s *Store) SaveChats(ctx context.Context, chats []wa.Chat) error {
	return s.inTx(ctx, "saving chats", func(tx *sql.Tx) error {
		for _, c := range chats {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO chats (jid, name, is_group) VALUES (?, ?, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
					is_group = excluded.is_group`,
				c.JID, c.Name, c.IsGroup); err != nil {
				return fmt.Errorf("chat %s: %w", c.JID, err)
			}
		}
		return nil
	})
}

// SavePushNames records the display names users chose for themselves.
func (s *Store) SavePushNames(ctx context.Context, names map[string]string) error {
	contacts := make([]wa.Contact, 0, len(names))
	for jid, name := range names {
		contacts = append(contacts, wa.Contact{JID: jid, PushName: name})
	}
	return s.SaveContacts(ctx, contacts)
}

// SaveContacts upserts contact names. Empty fields leave stored values alone,
// so partial updates (e.g. only a new push name) never erase anything.
func (s *Store) SaveContacts(ctx context.Context, contacts []wa.Contact) error {
	return s.inTx(ctx, "saving contacts", func(tx *sql.Tx) error {
		for _, c := range contacts {
			if c.JID == "" || c.Name == "" && c.PushName == "" && c.BusinessName == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO contacts (jid, name, push_name, business_name) VALUES (?, ?, ?, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name          = CASE WHEN excluded.name          != '' THEN excluded.name          ELSE name          END,
					push_name     = CASE WHEN excluded.push_name     != '' THEN excluded.push_name     ELSE push_name     END,
					business_name = CASE WHEN excluded.business_name != '' THEN excluded.business_name ELSE business_name END`,
				c.JID, c.Name, c.PushName, c.BusinessName); err != nil {
				return fmt.Errorf("contact %s: %w", c.JID, err)
			}
		}
		return nil
	})
}

// ChatSummary is a chat as listed in the chat list (or, from Directory, a
// contact who could be messaged).
type ChatSummary struct {
	JID           string
	Name          string // see titleSQL; "" if nothing is known
	IsGroup       bool
	LastMessageAt time.Time
	Messages      int
}

// Title is the best available label for the chat.
func (c ChatSummary) Title() string {
	if c.Name != "" {
		return c.Name
	}
	return c.JID
}

// titleSQL picks a chat's display name, like WhatsApp does: for a group its
// subject; for a person the user's address-book name, then the name history
// sync gave the chat, then their own push name, then their business name.
const titleSQL = `CASE WHEN c.is_group THEN c.name ELSE COALESCE(
	NULLIF(ct.name, ''), NULLIF(c.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.business_name, ''), '') END`

const chatSummaryQuery = `
	SELECT c.jid, ` + titleSQL + `, c.is_group, c.last_message_at,
	       (SELECT count(*) FROM messages m WHERE m.chat_jid = c.jid)
	FROM chats c LEFT JOIN contacts ct ON ct.jid = c.jid`

// Chats lists chats, most recently active first.
func (s *Store) Chats(ctx context.Context) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, chatSummaryQuery+` ORDER BY c.last_message_at DESC, c.jid`)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer rows.Close()
	var chats []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var last int64
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &last, &c.Messages); err != nil {
			return nil, fmt.Errorf("listing chats: %w", err)
		}
		c.LastMessageAt = time.Unix(last, 0)
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	return chats, nil
}

// Directory lists everyone and every group the user can message: all chats
// (most recent first), then named contacts without a chat, by name.
func (s *Store) Directory(ctx context.Context) ([]ChatSummary, error) {
	chats, err := s.Chats(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ct.jid, COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), ct.business_name) AS title
		FROM contacts ct
		WHERE NOT EXISTS (SELECT 1 FROM chats c WHERE c.jid = ct.jid)
		  AND (ct.name != '' OR ct.push_name != '' OR ct.business_name != '')
		ORDER BY lower(title), ct.jid`)
	if err != nil {
		return nil, fmt.Errorf("listing contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c ChatSummary
		if err := rows.Scan(&c.JID, &c.Name); err != nil {
			return nil, fmt.Errorf("listing contacts: %w", err)
		}
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing contacts: %w", err)
	}
	return chats, nil
}

// ErrChatNotFound is returned by FindChat when nothing matches.
var ErrChatNotFound = errors.New("no matching chat")

// FindChat resolves user input to one chat: an exact JID, a phone number,
// or a case-insensitive substring of the chat's title.
func (s *Store) FindChat(ctx context.Context, query string) (ChatSummary, error) {
	chats, err := s.Chats(ctx)
	if err != nil {
		return ChatSummary{}, err
	}
	want := query
	if !strings.Contains(query, "@") {
		if digits, err := wa.NormalizePhone(query); err == nil {
			want = digits + "@s.whatsapp.net"
		}
	}
	for _, c := range chats {
		if c.JID == want {
			return c, nil
		}
	}
	var matches []ChatSummary
	needle := strings.ToLower(query)
	for _, c := range chats {
		if c.Name != "" && strings.Contains(strings.ToLower(c.Name), needle) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return ChatSummary{}, fmt.Errorf("%w: %q", ErrChatNotFound, query)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, c := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.JID))
		}
		return ChatSummary{}, fmt.Errorf("%q matches several chats: %s", query, strings.Join(names, ", "))
	}
}

// Messages returns the newest limit messages in chat, oldest first. A limit
// of 0 or less returns all of them.
func (s *Store) Messages(ctx context.Context, chat string, limit int) ([]wa.Message, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sender_jid, push_name, ts, from_me, kind, body, codec, is_group, sender_name FROM (
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
		if err := rows.Scan(&m.ID, &m.Sender, &m.PushName, &ts, &m.FromMe, &m.Kind, &body, &codecID, &m.IsGroup, &m.SenderName); err != nil {
			return nil, fmt.Errorf("loading messages: %w", err)
		}
		m.Timestamp = time.Unix(ts, 0)
		if m.Text, err = s.openBody(body, codecID); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}
	return msgs, nil
}

// Retention limits what is kept. Zero values mean no limit.
type Retention struct {
	KeepDays   int
	MaxPerChat int
}

// Prune deletes messages outside r, relative to now, and returns how many.
func (s *Store) Prune(ctx context.Context, r Retention, now time.Time) (int64, error) {
	var total int64
	err := s.inTx(ctx, "pruning messages", func(tx *sql.Tx) error {
		if r.KeepDays > 0 {
			cutoff := now.AddDate(0, 0, -r.KeepDays).Unix()
			res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE ts < ?`, cutoff)
			if err != nil {
				return err
			}
			total += rowsAffected(res)
		}
		if r.MaxPerChat > 0 {
			res, err := tx.ExecContext(ctx, `
				DELETE FROM messages WHERE rowid IN (
					SELECT rowid FROM (
						SELECT rowid, row_number() OVER (PARTITION BY chat_jid ORDER BY ts DESC, id DESC) AS n
						FROM messages
					) WHERE n > ?
				)`, r.MaxPerChat)
			if err != nil {
				return err
			}
			total += rowsAffected(res)
		}
		return nil
	})
	return total, err
}

func rowsAffected(res sql.Result) int64 {
	// modernc's driver always supports RowsAffected.
	n, err := res.RowsAffected()
	if err != nil {
		return 0
	}
	return n
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
