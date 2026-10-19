package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// SetMuted records a chat's mute setting (zero until = not muted). Chats
// not stored yet are skipped rather than created: app-state sync reports
// mutes for chats long gone quiet, and history sync carries the mute of
// any chat it does bring.
func (s *Store) SetMuted(ctx context.Context, chat string, until time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE chats SET muted_until = ? WHERE jid = ?`,
		unixOrZero(until), chat); err != nil {
		return fmt.Errorf("saving mute for %s: %w", chat, err)
	}
	return nil
}

// unixOrZero stores a zero time as 0 rather than year 1's negative seconds.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// SaveChats records chat metadata. An empty name never overwrites a known one.
func (s *Store) SaveChats(ctx context.Context, chats []wa.Chat) error {
	return s.inTx(ctx, "saving chats", func(tx *sql.Tx) error {
		for _, c := range chats {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO chats (jid, name, is_group, muted_until) VALUES (?, ?, ?, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
					is_group = excluded.is_group,
					-- history only knows mutes; an unmute arrives as an app-state event
					muted_until = CASE WHEN excluded.muted_until != 0 THEN excluded.muted_until ELSE muted_until END`,
				c.JID, c.Name, c.IsGroup, unixOrZero(c.MutedUntil)); err != nil {
				return fmt.Errorf("chat %s: %w", c.JID, err)
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
	Unread        int       // incoming messages newer than LastReadAt
	MemberCount   int       // group participants, if known
	MutedUntil    time.Time // zero = not muted; see wa.MutedForever
}

// Muted reports whether the chat is muted at now.
func (c ChatSummary) Muted(now time.Time) bool { return c.MutedUntil.After(now) }

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
// A group in a community is "Community › Group", and the community's
// announcement group (which WhatsApp names after the community) is
// "Community › Announcements", so the community's chats can be told apart.
const titleSQL = `CASE WHEN c.is_group THEN ` + groupTitleSQL + ` ELSE COALESCE(
	NULLIF(ct.name, ''), NULLIF(c.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.business_name, ''), '') END`

const groupTitleSQL = `COALESCE(
	NULLIF((SELECT p.name FROM chats p WHERE p.jid = c.community_jid), '') || ' › ' ||
		CASE WHEN c.is_announcements THEN 'Announcements' ELSE c.name END,
	CASE WHEN c.is_announcements AND c.name != '' THEN c.name || ' › Announcements' END,
	c.name)`

const chatSummaryQuery = `
	SELECT c.jid, ` + titleSQL + `, c.is_group, c.last_message_at, c.last_read_at,
	       (SELECT count(*) FROM messages m WHERE m.chat_jid = c.jid),
	       (SELECT count(*) FROM messages m WHERE m.chat_jid = c.jid AND m.from_me = 0 AND m.deleted = 0 AND m.ts > c.last_read_at),
	       (SELECT count(*) FROM group_participants gp WHERE gp.group_jid = c.jid),
	       c.muted_until
	FROM chats c LEFT JOIN contacts ct ON ct.jid = c.jid`

// Chats lists chats, most recently active first. A community itself isn't
// a chat (its groups are), so it's listed only if it somehow has messages.
func (s *Store) Chats(ctx context.Context) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, chatSummaryQuery+`
		WHERE NOT c.is_community OR EXISTS (SELECT 1 FROM messages m WHERE m.chat_jid = c.jid)
		ORDER BY c.last_message_at DESC, c.jid`)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer rows.Close()
	var chats []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var last, read, muted int64
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &last, &read, &c.Messages, &c.Unread, &c.MemberCount, &muted); err != nil {
			return nil, fmt.Errorf("listing chats: %w", err)
		}
		c.LastMessageAt, c.LastReadAt = time.Unix(last, 0), time.Unix(read, 0)
		if muted != 0 {
			c.MutedUntil = time.Unix(muted, 0)
		}
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
				INSERT INTO chats (jid, name, is_group, topic, community_jid, is_community, is_announcements)
				VALUES (?, ?, 1, ?, ?, ?, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
					topic = excluded.topic, is_group = 1, community_jid = excluded.community_jid,
					is_community = excluded.is_community, is_announcements = excluded.is_announcements`,
				g.JID, g.Name, g.Topic, g.Community, g.IsCommunity, g.Announcements); err != nil {
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
