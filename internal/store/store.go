// Package store owns go-whassup's chat and message database (messages.db).
// whatsmeow persists only protocol state; message content lives here.
// See docs/storage.md.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wasichu/go-whassup/internal/sqlite"
	"github.com/wasichu/go-whassup/internal/wa"
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
	LastReadAt    time.Time
	Messages      int
	Unread        int // incoming messages newer than LastReadAt
	MemberCount   int // group participants, if known
}

// Title is the best available label for the chat.
func (c ChatSummary) Title() string {
	if c.Name != "" {
		return c.Name
	}
	// Someone not in the address book: show their number, like the phone.
	if user, ok := strings.CutSuffix(c.JID, "@s.whatsapp.net"); ok {
		return "+" + user
	}
	return c.JID
}

// titleSQL picks a chat's display name, like WhatsApp does: for a group its
// subject; for a person the user's address-book name, then the name history
// sync gave the chat, then their own push name, then their business name.
const titleSQL = `CASE WHEN c.is_group THEN c.name ELSE COALESCE(
	NULLIF(ct.name, ''), NULLIF(c.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.business_name, ''), '') END`

const chatSummaryQuery = `
	SELECT c.jid, ` + titleSQL + `, c.is_group, c.last_message_at, c.last_read_at,
	       (SELECT count(*) FROM messages m WHERE m.chat_jid = c.jid),
	       (SELECT count(*) FROM messages m WHERE m.chat_jid = c.jid AND m.from_me = 0 AND m.deleted = 0 AND m.ts > c.last_read_at),
	       (SELECT count(*) FROM group_participants gp WHERE gp.group_jid = c.jid)
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
		var last, read int64
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &last, &read, &c.Messages, &c.Unread, &c.MemberCount); err != nil {
			return nil, fmt.Errorf("listing chats: %w", err)
		}
		c.LastMessageAt, c.LastReadAt = time.Unix(last, 0), time.Unix(read, 0)
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	return chats, nil
}

// SaveGroups records group names, topics and (when known) membership. A
// group's participant list is replaced as a whole.
func (s *Store) SaveGroups(ctx context.Context, groups []wa.Group) error {
	return s.inTx(ctx, "saving groups", func(tx *sql.Tx) error {
		for _, g := range groups {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO chats (jid, name, is_group, topic) VALUES (?, ?, 1, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
					topic = excluded.topic, is_group = 1`,
				g.JID, g.Name, g.Topic); err != nil {
				return fmt.Errorf("group %s: %w", g.JID, err)
			}
			if g.Participants == nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM group_participants WHERE group_jid = ?`, g.JID); err != nil {
				return fmt.Errorf("group %s: %w", g.JID, err)
			}
			for _, p := range g.Participants {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO group_participants (group_jid, user_jid, is_admin) VALUES (?, ?, ?)
					ON CONFLICT DO UPDATE SET is_admin = max(is_admin, excluded.is_admin)`,
					g.JID, p.JID, p.Admin); err != nil {
					return fmt.Errorf("group %s member %s: %w", g.JID, p.JID, err)
				}
			}
		}
		return nil
	})
}

// Member is a group participant with a display name.
type Member struct {
	JID   string
	Name  string // address book, push name or business name; "" if unknown
	Admin bool
}

// ChatDetails is what the info screen shows about a chat.
type ChatDetails struct {
	ChatSummary
	Topic    string
	PushName string   // for a person: the name they chose
	Members  []Member // for a group: admins first, then by name
}

// Details returns everything known about a chat.
func (s *Store) Details(ctx context.Context, chat string) (ChatDetails, error) {
	var d ChatDetails
	c, err := s.chat(ctx, chat)
	if err != nil {
		return d, err
	}
	d.ChatSummary = c
	if err := s.db.QueryRowContext(ctx, `
		SELECT c.topic, coalesce(ct.push_name, '') FROM chats c LEFT JOIN contacts ct ON ct.jid = c.jid WHERE c.jid = ?`,
		chat).Scan(&d.Topic, &d.PushName); err != nil {
		return d, fmt.Errorf("loading chat details: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT gp.user_jid, gp.is_admin,
		       COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), ct.business_name, '') AS display
		FROM group_participants gp LEFT JOIN contacts ct ON ct.jid = gp.user_jid
		WHERE gp.group_jid = ?
		ORDER BY gp.is_admin DESC, display = '', lower(display), gp.user_jid`, chat)
	if err != nil {
		return d, fmt.Errorf("loading members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.JID, &m.Admin, &m.Name); err != nil {
			return d, fmt.Errorf("loading members: %w", err)
		}
		d.Members = append(d.Members, m)
	}
	if err := rows.Err(); err != nil {
		return d, fmt.Errorf("loading members: %w", err)
	}
	return d, nil
}

func (s *Store) chat(ctx context.Context, jid string) (ChatSummary, error) {
	chats, err := s.Chats(ctx)
	if err != nil {
		return ChatSummary{}, err
	}
	for _, c := range chats {
		if c.JID == jid {
			return c, nil
		}
	}
	return ChatSummary{}, fmt.Errorf("%w: %q", ErrChatNotFound, jid)
}

// Names returns the best known display name for each of jids that has one
// (address book, push name, business name).
func (s *Store) Names(ctx context.Context, jids []string) (map[string]string, error) {
	names := make(map[string]string, len(jids))
	for _, jid := range jids {
		var name string
		err := s.db.QueryRowContext(ctx, `
			SELECT COALESCE(NULLIF(name, ''), NULLIF(push_name, ''), business_name, '') FROM contacts WHERE jid = ?`, jid).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("looking up names: %w", err)
		}
		if name != "" {
			names[jid] = name
		}
	}
	return names, nil
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
