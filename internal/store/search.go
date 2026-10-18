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

// The search index (messages_fts, FTS5) is a plaintext copy of message
// words, so it is only kept while the store writes plaintext. It's
// maintained here, in the same transactions as the messages, rather than by
// triggers: triggers can't see through the codec. Enabling encryption must
// empty it (and VACUUM). See docs/storage.md.

// ErrSearchUnavailable is returned by Search while messages are encrypted.
var ErrSearchUnavailable = errors.New("message search is unavailable while encryption is on")

// Snippet markers around matched words in SearchHit.Snippet.
const (
	MatchStart = "\x02"
	MatchEnd   = "\x03"
)

// SearchHit is one matching message.
type SearchHit struct {
	Chat    ChatSummary
	Message wa.Message // text not filled in; see Snippet
	Snippet string     // excerpt with matches wrapped in MatchStart/MatchEnd
}

// indexing reports whether the search index is kept: only with plaintext
// storage and no key (during enable/disable it's stale or must not grow).
func (s *Store) indexing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.codec.ID() == (Plaintext{}).ID() && !s.keyed
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// index adds (or replaces) a message's text in the search index.
func (s *Store) index(ctx context.Context, x execer, rowid int64, chat, id, text string) error {
	if !s.indexing() {
		return nil
	}
	if _, err := x.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid = ?`, rowid); err != nil {
		return fmt.Errorf("indexing message %s: %w", id, err)
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := x.ExecContext(ctx, `INSERT INTO messages_fts (rowid, text, chat_jid, msg_id) VALUES (?, ?, ?, ?)`,
		rowid, text, chat, id); err != nil {
		return fmt.Errorf("indexing message %s: %w", id, err)
	}
	return nil
}

// unindex removes a message from the search index.
func unindex(ctx context.Context, x execer, chat, id string) error {
	_, err := x.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid IN (
		SELECT rowid FROM messages WHERE chat_jid = ? AND id = ?)`, chat, id)
	return err
}

// ftsQuery turns user input into an FTS5 query: every word must appear
// (as a literal, so punctuation can't break the syntax), and the last word
// may be a prefix, for search-as-you-type.
func ftsQuery(input string) string {
	words := strings.Fields(input)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	if len(words) > 0 {
		words[len(words)-1] += "*"
	}
	return strings.Join(words, " ")
}

// Search finds messages containing every word of query (newest first, at
// most limit).
func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if !s.indexing() {
		return nil, ErrSearchUnavailable
	}
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.chat_jid, m.id, m.sender_jid, m.ts, m.from_me, m.kind,
		       snippet(messages_fts, 0, ?, ?, '…', 12),
		       COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(m.push_name, ''), ct.business_name, '')
		FROM messages_fts f
		JOIN messages m ON m.rowid = f.rowid
		LEFT JOIN contacts ct ON ct.jid = m.sender_jid
		WHERE messages_fts MATCH ?
		ORDER BY m.ts DESC, m.id DESC LIMIT ?`, MatchStart, MatchEnd, q, limit)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		var ts int64
		if err := rows.Scan(&h.Message.Chat, &h.Message.ID, &h.Message.Sender, &ts, &h.Message.FromMe, &h.Message.Kind, &h.Snippet, &h.Message.SenderName); err != nil {
			return nil, fmt.Errorf("searching: %w", err)
		}
		h.Message.Timestamp = time.Unix(ts, 0)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}
	if len(hits) == 0 {
		return nil, nil
	}
	chats, err := s.Chats(ctx)
	if err != nil {
		return nil, err
	}
	byJID := make(map[string]ChatSummary, len(chats))
	for _, c := range chats {
		byJID[c.JID] = c
	}
	for i := range hits {
		hits[i].Chat = byJID[hits[i].Message.Chat]
		hits[i].Message.IsGroup = hits[i].Chat.IsGroup
	}
	return hits, nil
}
