package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations[i] upgrades the schema from user_version i to i+1. Append only;
// never edit a migration that has shipped.
var migrations = []string{
	// 1: chats, messages, contacts (push names).
	`
CREATE TABLE chats (
	jid             TEXT PRIMARY KEY,
	name            TEXT NOT NULL DEFAULT '',
	is_group        INTEGER NOT NULL DEFAULT 0,
	last_message_at INTEGER NOT NULL DEFAULT 0  -- unix seconds
) STRICT;

CREATE TABLE messages (
	chat_jid   TEXT NOT NULL,
	id         TEXT NOT NULL,
	sender_jid TEXT NOT NULL,
	push_name  TEXT NOT NULL DEFAULT '',
	ts         INTEGER NOT NULL,                -- unix seconds
	from_me    INTEGER NOT NULL,
	kind       TEXT NOT NULL,
	body       BLOB NOT NULL,                   -- text sealed by codec
	codec      INTEGER NOT NULL DEFAULT 0,      -- Codec.ID; 0 = plaintext
	PRIMARY KEY (chat_jid, id)
) STRICT;

CREATE INDEX messages_by_chat_time ON messages (chat_jid, ts, id);

CREATE TABLE contacts (
	jid       TEXT PRIMARY KEY,
	push_name TEXT NOT NULL
) STRICT;
`,
	// 2: address-book and business names for contacts.
	`
ALTER TABLE contacts ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN business_name TEXT NOT NULL DEFAULT '';
`,
	// 3: read state. Chats that exist at upgrade time count as read.
	`
ALTER TABLE chats ADD COLUMN last_read_at INTEGER NOT NULL DEFAULT 0; -- unix seconds
UPDATE chats SET last_read_at = last_message_at;
`,
	// 4: edits, deletes ("for everyone" keeps a placeholder) and reactions.
	`
ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0; -- unix seconds; 0 = never
ALTER TABLE messages ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;   -- 1 = deleted for everyone
CREATE TABLE reactions (
	chat_jid   TEXT NOT NULL,
	message_id TEXT NOT NULL,
	sender_jid TEXT NOT NULL,
	from_me    INTEGER NOT NULL,
	emoji      TEXT NOT NULL,
	ts         INTEGER NOT NULL, -- unix seconds
	PRIMARY KEY (chat_jid, message_id, sender_jid)
) STRICT;
`,
	// 5: remove meta messages (edits, poll votes, pins, ...) that older builds
	// stored as chat messages under kinds derived from their field names.
	`
DELETE FROM messages WHERE kind IN (
	'secret encrypted', 'poll update', 'enc reaction', 'enc event response',
	'enc comment', 'keep in chat', 'pin in chat', 'placeholder');
`,
	// 6: attachments. media holds wa.Media.Data (it contains the media key),
	// sealed like body but tracked by its own codec column.
	`
ALTER TABLE messages ADD COLUMN media BLOB;
ALTER TABLE messages ADD COLUMN media_codec INTEGER NOT NULL DEFAULT 0;
`,
	// 7: group metadata.
	`
ALTER TABLE chats ADD COLUMN topic TEXT NOT NULL DEFAULT '';
CREATE TABLE group_participants (
	group_jid TEXT NOT NULL,
	user_jid  TEXT NOT NULL,
	is_admin  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (group_jid, user_jid)
) STRICT;
`,
	// 8: full-text search over plaintext message text (see search.go). Only
	// rows stored in plaintext (codec 0) are indexed.
	`
CREATE VIRTUAL TABLE messages_fts USING fts5(
	text, chat_jid UNINDEXED, msg_id UNINDEXED,
	tokenize = 'unicode61 remove_diacritics 2'
);
INSERT INTO messages_fts (rowid, text, chat_jid, msg_id)
	SELECT rowid, CAST(body AS TEXT), chat_jid, id FROM messages
	WHERE codec = 0 AND deleted = 0 AND length(body) > 0;
`,
	// 9: encryption keys. The messages.codec / media_codec columns hold the
	// ID of the key that sealed each value (0 = plaintext).
	`
CREATE TABLE keys (
	id         INTEGER PRIMARY KEY,
	alg        TEXT NOT NULL,       -- "aes-256-gcm"
	source     TEXT NOT NULL,       -- config key_source when created
	salt       BLOB,                -- passphrase sources only
	kdf        TEXT NOT NULL DEFAULT '', -- JSON crypt.KDFParams, passphrase sources only
	wrapped    BLOB NOT NULL,       -- data key sealed under the key-encryption key
	created_at INTEGER NOT NULL
) STRICT;
`,
	// 10: reactions are content, so their emoji is sealed like message text:
	// emoji becomes a BLOB with its own codec (key ID) column.
	`
CREATE TABLE reactions_v10 (
	chat_jid   TEXT NOT NULL,
	message_id TEXT NOT NULL,
	sender_jid TEXT NOT NULL,
	from_me    INTEGER NOT NULL,
	emoji      BLOB NOT NULL,
	codec      INTEGER NOT NULL DEFAULT 0,
	ts         INTEGER NOT NULL,
	PRIMARY KEY (chat_jid, message_id, sender_jid)
) STRICT;
INSERT INTO reactions_v10 (chat_jid, message_id, sender_jid, from_me, emoji, codec, ts)
	SELECT chat_jid, message_id, sender_jid, from_me, CAST(emoji AS BLOB), 0, ts FROM reactions;
DROP TABLE reactions;
ALTER TABLE reactions_v10 RENAME TO reactions;
`,
	// 11: when FinishEncrypting completed for a key (so a crash between the
	// last re-seal batch and the final VACUUM is finished on next start).
	`
ALTER TABLE keys ADD COLUMN finished_at INTEGER NOT NULL DEFAULT 0;
`,
	// 12: chat mute setting (unix seconds; 0 = not muted), so notifications
	// can skip muted chats.
	`
ALTER TABLE chats ADD COLUMN muted_until INTEGER NOT NULL DEFAULT 0;
`,
	// 13: what a reply answers: the quoted message's ID and author. Only the
	// pointer: the quote is looked up when shown, so these are identifiers
	// (like reactions.message_id), not content, and aren't sealed.
	`
ALTER TABLE messages ADD COLUMN reply_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN reply_sender TEXT NOT NULL DEFAULT '';
`,
}

// migrate brings the schema up to date inside one transaction.
func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("messages db schema v%d is newer than this build (v%d)", version, len(migrations))
	}
	if version == len(migrations) {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting migration: %w", err)
	}
	defer rollback(tx)
	for v := version; v < len(migrations); v++ {
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			return fmt.Errorf("migrating messages db to v%d: %w", v+1, err)
		}
	}
	// PRAGMA can't take bound parameters; the value is our own constant.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return fmt.Errorf("setting schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}
	return nil
}

// rollback is deferred after BeginTx; after Commit it is a no-op, and a
// rollback failure leaves nothing further to clean up.
func rollback(tx *sql.Tx) {
	_ = tx.Rollback() //nolint:errcheck // see comment above
}
