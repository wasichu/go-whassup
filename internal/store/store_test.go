package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/sqlite"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

const (
	ada   = "15551234567@s.whatsapp.net"
	bob   = "15559876543@s.whatsapp.net"
	group = "120363000000000000@g.us"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T, path string, codec Codec) *Store {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "messages.db")
	}
	st, err := Open(t.Context(), path, codec)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func msg(chat, id string, at time.Time, text string) wa.Message {
	return wa.Message{ID: id, Chat: chat, Sender: chat, Timestamp: at, Kind: wa.KindText, Text: text}
}

func ids(msgs []wa.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func mustSave(t *testing.T, st *Store, msgs ...wa.Message) int {
	t.Helper()
	n, err := st.SaveMessages(t.Context(), msgs)
	if err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
	return n
}

func TestSaveMessagesDedupesAndOrders(t *testing.T) {
	st := openStore(t, "", nil)
	if n := mustSave(t, st, msg(ada, "2", t0.Add(time.Minute), "second"), msg(ada, "1", t0, "first")); n != 2 {
		t.Errorf("inserted = %d, want 2", n)
	}
	// History and live delivery overlap: the same ID again is ignored.
	if n := mustSave(t, st, msg(ada, "1", t0, "first"), msg(ada, "3", t0.Add(2*time.Minute), "third")); n != 1 {
		t.Errorf("inserted = %d, want 1", n)
	}

	got, err := st.Messages(t.Context(), ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if want := []string{"1", "2", "3"}; !slices.Equal(ids(got), want) {
		t.Errorf("order = %v, want %v", ids(got), want)
	}
	if got[0].Text != "first" || got[0].Chat != ada || got[0].Kind != wa.KindText {
		t.Errorf("first message = %+v", got[0])
	}

	newest, err := st.Messages(t.Context(), ada, 2)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if want := []string{"2", "3"}; !slices.Equal(ids(newest), want) {
		t.Errorf("limit 2 = %v, want newest two oldest-first %v", ids(newest), want)
	}
}

func TestChatsOrderNamesAndCounts(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "a1", t0, "hi"), msg(bob, "b1", t0.Add(time.Hour), "yo"))
	g := msg(group, "g1", t0.Add(30*time.Minute), "hello all")
	g.IsGroup = true
	mustSave(t, st, g)

	if err := st.SaveChats(ctx, []wa.Chat{{JID: group, Name: "Book club", IsGroup: true}, {JID: ada, Name: ""}}); err != nil {
		t.Fatalf("SaveChats: %v", err)
	}
	if err := st.SavePushNames(ctx, map[string]string{ada: "Ada", bob: ""}); err != nil {
		t.Fatalf("SavePushNames: %v", err)
	}
	// An empty name must not erase a known one.
	if err := st.SaveChats(ctx, []wa.Chat{{JID: group, IsGroup: true}}); err != nil {
		t.Fatalf("SaveChats: %v", err)
	}

	chats, err := st.Chats(ctx)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	var titles []string
	for _, c := range chats {
		titles = append(titles, c.Title())
	}
	// bob (no name: his number), Book club, Ada (push name fallback)
	if want := []string{"+15559876543", "Book club", "Ada"}; !slices.Equal(titles, want) {
		t.Errorf("titles = %v, want %v", titles, want)
	}
	if !chats[1].IsGroup || chats[1].Messages != 1 {
		t.Errorf("group summary = %+v", chats[1])
	}
}

func TestFindChat(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "a", t0, "x"), msg(bob, "b", t0, "x"), msg(group, "g", t0, "x"))
	if err := st.SaveChats(ctx, []wa.Chat{{JID: ada, Name: "Ada Lovelace"}, {JID: bob, Name: "Adam Bob"}, {JID: group, Name: "Book club", IsGroup: true}}); err != nil {
		t.Fatalf("SaveChats: %v", err)
	}
	tests := []struct {
		query, want, wantErr string
	}{
		{query: ada, want: ada},
		{query: "+1 (555) 123-4567", want: ada},
		{query: "book", want: group},
		{query: "lovelace", want: ada},
		{query: "ada", wantErr: "matches several chats"},
		{query: "nobody", wantErr: "no matching chat"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			c, err := st.FindChat(ctx, tt.query)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || c.JID != tt.want {
				t.Fatalf("FindChat = %q, %v; want %q", c.JID, err, tt.want)
			}
		})
	}
	if _, err := st.FindChat(ctx, "nobody"); !errors.Is(err, ErrChatNotFound) {
		t.Errorf("not-found error = %v, want ErrChatNotFound", err)
	}
}

func TestPrune(t *testing.T) {
	ctx := t.Context()
	now := t0.AddDate(0, 0, 10)

	st := openStore(t, "", nil)
	mustSave(t, st,
		msg(ada, "old", t0, "x"), // 10 days old
		msg(ada, "mid", now.AddDate(0, 0, -3), "x"),
		msg(ada, "new", now.Add(-time.Hour), "x"),
		msg(bob, "b-old", t0, "x"))

	removed, err := st.Prune(ctx, Retention{KeepDays: 7}, now)
	if err != nil || len(removed) != 2 {
		t.Fatalf("Prune keep_days = %v, %v; want 2", removed, err)
	}
	if !slices.Contains(removed, MessageRef{Chat: bob, ID: "b-old"}) {
		t.Errorf("removed = %v, should name what went", removed)
	}
	removed, err = st.Prune(ctx, Retention{MaxPerChat: 1}, now)
	if err != nil || len(removed) != 1 {
		t.Fatalf("Prune max_per_chat = %v, %v; want 1", removed, err)
	}
	got, err := st.Messages(ctx, ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if want := []string{"new"}; !slices.Equal(ids(got), want) {
		t.Errorf("remaining = %v, want %v", ids(got), want)
	}
	if removed, err := st.Prune(ctx, Retention{}, now); err != nil || len(removed) != 0 {
		t.Errorf("zero retention pruned %v, %v", removed, err)
	}
}

// reverseCodec is a stand-in for real encryption: it proves bodies pass
// through the codec and that each row remembers which codec sealed it.
type reverseCodec struct{}

func (reverseCodec) ID() int { return 7 }
func (reverseCodec) Seal(p, _ []byte) ([]byte, error) {
	out := bytes.Clone(p)
	slices.Reverse(out)
	return out, nil
}
func (c reverseCodec) Open(s, aad []byte) ([]byte, error) { return c.Seal(s, aad) }

func TestCodecSeamAndPerRowMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	ctx := t.Context()

	plain := openStore(t, path, nil)
	mustSave(t, plain, msg(ada, "p", t0, "plain text"))

	sealed := openStore(t, path, reverseCodec{})
	mustSave(t, sealed, msg(ada, "s", t0.Add(time.Minute), "secret"))

	var raw []byte
	var codecID int
	if err := sealed.db.QueryRowContext(ctx, `SELECT body, codec FROM messages WHERE id = 's'`).Scan(&raw, &codecID); err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if string(raw) != "terces" || codecID != 7 {
		t.Errorf("stored body = %q codec %d, want sealed bytes and codec 7", raw, codecID)
	}

	// A store with the codec reads both kinds of row.
	got, err := sealed.Messages(ctx, ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got[0].Text != "plain text" || got[1].Text != "secret" {
		t.Errorf("texts = %q, %q", got[0].Text, got[1].Text)
	}
	// Without the codec, sealed rows fail loudly instead of returning garbage.
	if _, err := plain.Messages(ctx, ada, 0); err == nil || !strings.Contains(err.Error(), "unknown codec 7") {
		t.Errorf("plaintext store reading sealed row: err = %v", err)
	}
}

func TestMigrateIsIdempotentAndRejectsNewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	st := openStore(t, path, nil)
	mustSave(t, st, msg(ada, "1", t0, "kept"))
	again := openStore(t, path, nil) // reopen: no re-migration, data intact
	if got, err := again.Messages(t.Context(), ada, 0); err != nil || len(got) != 1 {
		t.Fatalf("after reopen: %v, %v", got, err)
	}

	if _, err := st.db.ExecContext(t.Context(), "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	if _, err := Open(t.Context(), path, nil); err == nil || !strings.Contains(err.Error(), "newer than this build") {
		t.Errorf("opening newer schema: err = %v", err)
	}
}

func TestRecorder(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{}
	rec := &Recorder{
		Store:     st,
		Retention: Retention{MaxPerChat: 2},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return t0.Add(time.Hour) },
	}
	remove := rec.Attach(client)

	live := msg(ada, "live", t0.Add(time.Minute), "hello")
	live.PushName = "Ada"
	reaction := msg(ada, "r", t0.Add(2*time.Minute), "👍")
	reaction.Kind = wa.KindReaction
	client.Emit(live)
	client.Emit(reaction)
	client.Emit(wa.HistorySync{
		Chats:     []wa.Chat{{JID: group, Name: "Book club", IsGroup: true}},
		PushNames: map[string]string{bob: "Bob"},
		Messages: []wa.Message{
			msg(ada, "h1", t0.Add(-3*time.Minute), "old 1"),
			msg(ada, "h2", t0.Add(-2*time.Minute), "old 2"),
			msg(bob, "b1", t0, "hey"),
		},
	})
	remove()
	client.Emit(msg(bob, "after-remove", t0, "ignored"))

	ctx := context.Background()
	got, err := st.Messages(ctx, ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	// Reaction skipped; MaxPerChat 2 pruned the oldest after the history batch.
	if want := []string{"h2", "live"}; !slices.Equal(ids(got), want) {
		t.Errorf("ada = %v, want %v", ids(got), want)
	}
	chats, err := st.Chats(ctx)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	titles := map[string]string{}
	for _, c := range chats {
		titles[c.JID] = c.Title()
	}
	if titles[ada] != "Ada" || titles[bob] != "Bob" || titles[group] != "Book club" {
		t.Errorf("titles = %v", titles)
	}
	if b, err := st.Messages(ctx, bob, 0); err != nil || len(b) != 1 {
		t.Errorf("bob messages = %v, %v; want only b1 (handler removed)", ids(b), err)
	}
}

func TestRecorderStoresSentMessages(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{Paired: true}
	rec := &Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec.Attach(client)

	if err := client.SendText(t.Context(), ada, "sent from here"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	got, err := st.Messages(t.Context(), ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 1 || !got[0].FromMe || got[0].Text != "sent from here" {
		t.Errorf("stored = %+v", got)
	}
}

func TestRecorderSetRetention(t *testing.T) {
	st := openStore(t, "", nil)
	rec := &Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return t0 }}
	mustSave(t, st, msg(ada, "1", t0.Add(-time.Minute), "a"), msg(ada, "2", t0, "b"))
	rec.Prune(t.Context()) // no policy: nothing deleted
	rec.SetRetention(Retention{MaxPerChat: 1})
	rec.Prune(t.Context())
	if got, err := st.Messages(t.Context(), ada, 0); err != nil || len(got) != 1 || got[0].ID != "2" {
		t.Errorf("after SetRetention: %v, %v", ids(got), err)
	}
}

func TestContactNamesTitlesAndSenders(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	g := msg(group, "g1", t0, "hello all")
	g.IsGroup, g.Sender, g.PushName = true, bob, "bobby"
	mustSave(t, st, msg(ada, "a1", t0, "hi"), g)
	if err := st.SaveChats(ctx, []wa.Chat{{JID: ada, Name: "ada from history"}, {JID: group, Name: "Book club", IsGroup: true}}); err != nil {
		t.Fatalf("SaveChats: %v", err)
	}

	title := func(jid string) string {
		t.Helper()
		chats, err := st.Chats(ctx)
		if err != nil {
			t.Fatalf("Chats: %v", err)
		}
		for _, c := range chats {
			if c.JID == jid {
				return c.Title()
			}
		}
		return ""
	}
	if got := title(ada); got != "ada from history" {
		t.Errorf("before contacts: %q", got)
	}

	// Address-book name wins over history's chat name; partial updates keep it.
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: ada, Name: "Ada Lovelace", PushName: "ada"}}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: ada, PushName: "ada 2.0"}}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	if got := title(ada); got != "Ada Lovelace" {
		t.Errorf("address-book name should win, got %q", got)
	}
	// A contact's name never renames a group.
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: group, Name: "not the subject"}}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	if got := title(group); got != "Book club" {
		t.Errorf("group title = %q", got)
	}

	// Group senders resolve to the best known name.
	msgs, err := st.Messages(ctx, group, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Messages: %v, %v", msgs, err)
	}
	if msgs[0].SenderName != "bobby" {
		t.Errorf("sender name from message push name = %q", msgs[0].SenderName)
	}
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: bob, Name: "Bob Builder"}}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	msgs, err = st.Messages(ctx, group, 0)
	if err != nil || len(msgs) != 1 || msgs[0].SenderName != "Bob Builder" {
		t.Errorf("sender name from address book = %+v, %v", msgs, err)
	}
}

func TestDirectory(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "a1", t0, "hi"))
	if err := st.SaveContacts(ctx, []wa.Contact{
		{JID: ada, Name: "Ada"},
		{JID: "15550000003@s.whatsapp.net", PushName: "zed"},
		{JID: "15550000002@s.whatsapp.net", Name: "Carol"},
	}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	dir, err := st.Directory(ctx)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	var titles []string
	for _, c := range dir {
		titles = append(titles, c.Title())
	}
	// Chats first, then contacts without a chat, alphabetically.
	if want := []string{"Ada", "Carol", "zed"}; !slices.Equal(titles, want) {
		t.Errorf("directory = %v, want %v", titles, want)
	}
	if dir[1].Messages != 0 || dir[1].JID != "15550000002@s.whatsapp.net" {
		t.Errorf("contact entry = %+v", dir[1])
	}
}

func TestMigrateV1ToV2KeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	ctx := t.Context()
	for _, q := range []string{migrations[0], "PRAGMA user_version = 1", `INSERT INTO contacts (jid, push_name) VALUES ('` + ada + `', 'Ada')`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("v1 setup %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st := openStore(t, path, nil)
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: ada, Name: "Ada Lovelace"}}); err != nil {
		t.Fatalf("SaveContacts after upgrade: %v", err)
	}
	var name, push string
	if err := st.db.QueryRowContext(ctx, `SELECT name, push_name FROM contacts WHERE jid = ?`, ada).Scan(&name, &push); err != nil {
		t.Fatalf("read: %v", err)
	}
	if name != "Ada Lovelace" || push != "Ada" {
		t.Errorf("after upgrade name=%q push=%q", name, push)
	}
}

func TestRecorderStoresContacts(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{}
	(&Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Attach(client)
	client.Emit(wa.Contacts{List: []wa.Contact{{JID: ada, Name: "Ada"}}, Snapshot: true})
	dir, err := st.Directory(t.Context())
	if err != nil || len(dir) != 1 || dir[0].Name != "Ada" {
		t.Errorf("directory = %+v, %v", dir, err)
	}
}

func unreadOf(t *testing.T, st *Store, jid string) int {
	t.Helper()
	chats, err := st.Chats(t.Context())
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	for _, c := range chats {
		if c.JID == jid {
			return c.Unread
		}
	}
	t.Fatalf("chat %s not found", jid)
	return 0
}

func TestUnreadCountsAndMarkRead(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mine := msg(ada, "m", t0.Add(time.Minute), "from me")
	mine.FromMe = true
	mustSave(t, st, msg(ada, "1", t0, "a"), mine, msg(ada, "2", t0.Add(2*time.Minute), "b"))
	if got := unreadOf(t, st, ada); got != 2 {
		t.Fatalf("unread = %d, want 2 (own messages never count)", got)
	}
	unread, err := st.UnreadMessages(ctx, ada)
	if err != nil || len(unread) != 2 || unread[0].ID != "1" {
		t.Fatalf("UnreadMessages = %v, %v", ids(unread), err)
	}

	if err := st.MarkRead(ctx, ada, t0.Add(90*time.Second)); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if got := unreadOf(t, st, ada); got != 1 {
		t.Errorf("after partial read = %d, want 1", got)
	}
	if err := st.MarkRead(ctx, ada, t0); err != nil { // older: ignored
		t.Fatalf("MarkRead: %v", err)
	}
	if got := unreadOf(t, st, ada); got != 1 {
		t.Errorf("read state moved backwards: %d", got)
	}
}

func TestSetUnreadFromPhone(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "1", t0, "a"), msg(ada, "2", t0.Add(time.Minute), "b"), msg(ada, "3", t0.Add(2*time.Minute), "c"))
	for _, tt := range []struct{ phone, want int }{{2, 2}, {0, 0}, {5, 3}, {1, 1}} {
		if err := st.SetUnreadFromPhone(ctx, ada, tt.phone); err != nil {
			t.Fatalf("SetUnreadFromPhone(%d): %v", tt.phone, err)
		}
		if got := unreadOf(t, st, ada); got != tt.want {
			t.Errorf("phone says %d unread: got %d, want %d", tt.phone, got, tt.want)
		}
	}
}

func TestMigrateV2ToV3TreatsExistingAsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	ctx := t.Context()
	for _, q := range []string{
		migrations[0], migrations[1], "PRAGMA user_version = 2",
		`INSERT INTO chats (jid, last_message_at) VALUES ('` + ada + `', 1759320000)`,
		`INSERT INTO messages (chat_jid, id, sender_jid, ts, from_me, kind, body) VALUES ('` + ada + `', '1', '` + ada + `', 1759320000, 0, 'text', x'')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("v2 setup %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st := openStore(t, path, nil)
	if got := unreadOf(t, st, ada); got != 0 {
		t.Errorf("existing messages should be read after upgrade, unread = %d", got)
	}
}

func TestRecorderReadState(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{Paired: true}
	(&Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Attach(client)

	client.Emit(wa.HistorySync{
		Chats:    []wa.Chat{{JID: ada, UnreadCount: 1}, {JID: bob, UnreadCount: 0}},
		Messages: []wa.Message{msg(ada, "1", t0, "a"), msg(ada, "2", t0.Add(time.Minute), "b"), msg(bob, "b1", t0, "x")},
	})
	if a, b := unreadOf(t, st, ada), unreadOf(t, st, bob); a != 1 || b != 0 {
		t.Fatalf("after history: ada %d bob %d, want 1 and 0", a, b)
	}
	client.Emit(msg(bob, "b2", t0.Add(time.Hour), "new"))
	if got := unreadOf(t, st, bob); got != 1 {
		t.Errorf("live incoming should be unread, got %d", got)
	}
	client.Emit(wa.ChatRead{Chat: bob, At: t0.Add(time.Hour)})
	if got := unreadOf(t, st, bob); got != 0 {
		t.Errorf("read on another device should clear, got %d", got)
	}
	reply := msg(ada, "r", t0.Add(2*time.Hour), "reply")
	reply.FromMe = true
	client.Emit(reply)
	if got := unreadOf(t, st, ada); got != 0 {
		t.Errorf("replying should mark the chat read, got %d", got)
	}
}

func onlyMessage(t *testing.T, st *Store, chat, id string) wa.Message {
	t.Helper()
	msgs, err := st.Messages(t.Context(), chat, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("message %s not found in %v", id, ids(msgs))
	return wa.Message{}
}

func TestEditMessage(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "1", t0, "helo"))
	found, err := st.EditMessage(ctx, wa.MessageEdited{Chat: ada, ID: "1", Kind: wa.KindText, Text: "hello", At: t0.Add(time.Minute)})
	if err != nil || !found {
		t.Fatalf("EditMessage = %v, %v", found, err)
	}
	// An older edit arriving late doesn't win.
	if _, err := st.EditMessage(ctx, wa.MessageEdited{Chat: ada, ID: "1", Kind: wa.KindText, Text: "hel", At: t0.Add(30 * time.Second)}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if m := onlyMessage(t, st, ada, "1"); m.Text != "hello" || !m.Edited {
		t.Errorf("after edits: %q edited=%v", m.Text, m.Edited)
	}
	if found, err := st.EditMessage(ctx, wa.MessageEdited{Chat: ada, ID: "missing", Text: "x", At: t0}); err != nil || found {
		t.Errorf("edit of an unknown message = %v, %v; want not found", found, err)
	}
}

func TestEditGoesThroughCodec(t *testing.T) {
	st := openStore(t, "", reverseCodec{})
	mustSave(t, st, msg(ada, "1", t0, "secret"))
	if _, err := st.EditMessage(t.Context(), wa.MessageEdited{Chat: ada, ID: "1", Kind: wa.KindText, Text: "new secret", At: t0.Add(time.Minute)}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	var raw []byte
	if err := st.db.QueryRowContext(t.Context(), `SELECT body FROM messages WHERE id = '1'`).Scan(&raw); err != nil {
		t.Fatalf("raw: %v", err)
	}
	if string(raw) != "terces wen" {
		t.Errorf("edited body stored as %q; edits must be sealed like any write", raw)
	}
}

func TestDeleteMessage(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "1", t0, "oops"), msg(ada, "2", t0.Add(time.Minute), "private"))
	if err := st.SaveReactions(ctx, []wa.Reaction{{Chat: ada, MessageID: "1", Sender: bob, Emoji: "😂", At: t0}}); err != nil {
		t.Fatalf("SaveReactions: %v", err)
	}
	if got := unreadOf(t, st, ada); got != 2 {
		t.Fatalf("setup unread = %d", got)
	}

	if err := st.DeleteMessage(ctx, wa.MessageDeleted{Chat: ada, ID: "1", At: t0.Add(time.Hour)}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	m := onlyMessage(t, st, ada, "1")
	if !m.Deleted || m.Text != "" || len(m.Reactions) != 0 {
		t.Errorf("deleted for everyone = %+v", m)
	}
	var raw []byte
	if err := st.db.QueryRowContext(ctx, `SELECT body FROM messages WHERE id = '1'`).Scan(&raw); err != nil || len(raw) != 0 {
		t.Errorf("deleted text must be wiped from disk, body = %q (%v)", raw, err)
	}
	if got := unreadOf(t, st, ada); got != 1 {
		t.Errorf("deleted messages shouldn't count as unread, got %d", got)
	}

	if err := st.DeleteMessage(ctx, wa.MessageDeleted{Chat: ada, ID: "2", ForMe: true}); err != nil {
		t.Fatalf("DeleteMessage for me: %v", err)
	}
	msgs, err := st.Messages(ctx, ada, 0)
	if err != nil || !slices.Equal(ids(msgs), []string{"1"}) {
		t.Errorf("delete for me should remove the row: %v, %v", ids(msgs), err)
	}
}

func TestReactions(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(group, "1", t0, "party?"))
	save := func(rs ...wa.Reaction) {
		t.Helper()
		if err := st.SaveReactions(ctx, rs); err != nil {
			t.Fatalf("SaveReactions: %v", err)
		}
	}
	save(
		wa.Reaction{Chat: group, MessageID: "1", Sender: ada, Emoji: "🎉", At: t0},
		wa.Reaction{Chat: group, MessageID: "1", Sender: bob, Emoji: "🎉", At: t0.Add(time.Second)},
		wa.Reaction{Chat: group, MessageID: "1", Sender: "me", FromMe: true, Emoji: "👍", At: t0.Add(2 * time.Second)},
	)
	got := onlyMessage(t, st, group, "1").Reactions
	want := []wa.ReactionCount{{Emoji: "🎉", Count: 2}, {Emoji: "👍", Count: 1, Mine: true}}
	if !slices.Equal(got, want) {
		t.Fatalf("reactions = %+v, want %+v", got, want)
	}

	// Changing a reaction replaces it; a stale change doesn't; empty removes.
	save(wa.Reaction{Chat: group, MessageID: "1", Sender: ada, Emoji: "❤️", At: t0.Add(time.Minute)})
	save(wa.Reaction{Chat: group, MessageID: "1", Sender: ada, Emoji: "😐", At: t0.Add(30 * time.Second)})
	save(wa.Reaction{Chat: group, MessageID: "1", Sender: "me", FromMe: true, Emoji: "", At: t0.Add(time.Hour)})
	got = onlyMessage(t, st, group, "1").Reactions
	want = []wa.ReactionCount{{Emoji: "🎉", Count: 1}, {Emoji: "❤️", Count: 1}}
	if !slices.Equal(got, want) {
		t.Errorf("after changes = %+v, want %+v", got, want)
	}
}

func TestPruneRemovesOrphanedReactions(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "old", t0, "x"), msg(ada, "new", t0.AddDate(0, 0, 10), "y"))
	if err := st.SaveReactions(ctx, []wa.Reaction{{Chat: ada, MessageID: "old", Sender: bob, Emoji: "👍", At: t0}}); err != nil {
		t.Fatalf("SaveReactions: %v", err)
	}
	if _, err := st.Prune(ctx, Retention{KeepDays: 5}, t0.AddDate(0, 0, 10)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM reactions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("orphaned reactions left: %d (%v)", n, err)
	}
}

func TestMigrateV3ToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	ctx := t.Context()
	for _, q := range []string{migrations[0], migrations[1], migrations[2], "PRAGMA user_version = 3",
		`INSERT INTO chats (jid, last_message_at) VALUES ('` + ada + `', 1759320000)`,
		`INSERT INTO messages (chat_jid, id, sender_jid, ts, from_me, kind, body) VALUES ('` + ada + `', '1', '` + ada + `', 1759320000, 0, 'text', CAST('kept' AS BLOB))`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("v3 setup %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st := openStore(t, path, nil)
	if m := onlyMessage(t, st, ada, "1"); m.Text != "kept" || m.Edited || m.Deleted {
		t.Errorf("after upgrade = %+v", m)
	}
}

func TestRecorderAppliesEditsDeletesReactions(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{}
	(&Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Attach(client)
	client.Emit(msg(ada, "1", t0, "first"))
	client.Emit(msg(ada, "2", t0.Add(time.Minute), "second"))
	client.Emit(wa.MessageEdited{Chat: ada, ID: "1", Kind: wa.KindText, Text: "first!", At: t0.Add(2 * time.Minute)})
	client.Emit(wa.MessageDeleted{Chat: ada, ID: "2", At: t0.Add(3 * time.Minute)})
	client.Emit(wa.Reaction{Chat: ada, MessageID: "1", Sender: ada, Emoji: "👍", At: t0.Add(4 * time.Minute)})
	client.Emit(wa.HistorySync{Reactions: []wa.Reaction{{Chat: ada, MessageID: "1", Sender: bob, Emoji: "👍", At: t0}}})

	if m := onlyMessage(t, st, ada, "1"); m.Text != "first!" || !m.Edited || len(m.Reactions) != 1 || m.Reactions[0].Count != 2 {
		t.Errorf("message 1 = %+v", m)
	}
	if m := onlyMessage(t, st, ada, "2"); !m.Deleted {
		t.Errorf("message 2 = %+v", m)
	}
}

func TestMigrateV4ToV5RemovesMetaMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	ctx := t.Context()
	insert := func(id, kind string) string {
		return `INSERT INTO messages (chat_jid, id, sender_jid, ts, from_me, kind, body) VALUES ('` + ada + `', '` + id + `', '` + ada + `', 1759320000, 1, '` + kind + `', x'')`
	}
	for _, q := range []string{migrations[0], migrations[1], migrations[2], migrations[3], "PRAGMA user_version = 4",
		`INSERT INTO chats (jid, last_message_at) VALUES ('` + ada + `', 1759320000)`,
		insert("keep", "text"), insert("junk1", "secret encrypted"), insert("junk2", "poll update"), insert("img", "image"),
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("v4 setup %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st := openStore(t, path, nil)
	msgs, err := st.Messages(ctx, ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := ids(msgs); !slices.Equal(got, []string{"img", "keep"}) {
		t.Errorf("after cleanup = %v, want [img keep]", got)
	}
}

func mediaMsg(chat, id string, at time.Time) wa.Message {
	m := msg(chat, id, at, "cat")
	m.Kind = wa.KindImage
	m.Media = &wa.Media{Data: mustMediaData()}
	return m
}

// mustMediaData is a real serialized media message (wa.ParseMedia must
// accept it): an image with a media key.
func mustMediaData() []byte {
	return []byte("\x1a\x13\x12\x0aimage/jpeg\x3a\x05mkey1")
}

func TestMediaStoredSealedAndRoundTrips(t *testing.T) {
	st := openStore(t, "", reverseCodec{})
	ctx := t.Context()
	if _, err := wa.ParseMedia(mustMediaData()); err != nil {
		t.Fatalf("test fixture isn't valid media: %v", err)
	}
	mustSave(t, st, mediaMsg(ada, "1", t0))

	var raw []byte
	var codecID int
	if err := st.db.QueryRowContext(ctx, `SELECT media, media_codec FROM messages WHERE id = '1'`).Scan(&raw, &codecID); err != nil {
		t.Fatalf("raw: %v", err)
	}
	if codecID != 7 || bytes.Equal(raw, mustMediaData()) {
		t.Errorf("media must be sealed like text: codec=%d", codecID)
	}
	m := onlyMessage(t, st, ada, "1")
	if m.Media == nil || m.Media.Mime != "image/jpeg" {
		t.Fatalf("media after round trip = %+v", m.Media)
	}

	if err := st.DeleteMessage(ctx, wa.MessageDeleted{Chat: ada, ID: "1"}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	var media []byte
	if err := st.db.QueryRowContext(ctx, `SELECT media FROM messages WHERE id = '1'`).Scan(&media); err != nil || media != nil {
		t.Errorf("deleting for everyone must wipe the media key: %q (%v)", media, err)
	}
}

type fakeCache struct{ removed []MessageRef }

func (f *fakeCache) Remove(chat, id string) error {
	f.removed = append(f.removed, MessageRef{chat, id})
	return nil
}

func TestRecorderRemovesCachedMedia(t *testing.T) {
	st := openStore(t, "", nil)
	cache := &fakeCache{}
	client := &watest.Client{}
	rec := &Recorder{Store: st, Media: cache, Retention: Retention{MaxPerChat: 1},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return t0 }}
	rec.Attach(client)
	client.Emit(mediaMsg(ada, "old", t0))
	client.Emit(mediaMsg(ada, "new", t0.Add(time.Minute)))
	client.Emit(wa.MessageDeleted{Chat: ada, ID: "new"})
	rec.Prune(t.Context())
	want := []MessageRef{{ada, "new"}, {ada, "old"}}
	if !slices.Equal(cache.removed, want) {
		t.Errorf("removed from cache = %v, want %v", cache.removed, want)
	}
}

func TestUndecodableMediaKeepsMessage(t *testing.T) {
	st := openStore(t, "", nil)
	m := msg(ada, "1", t0, "cat")
	m.Kind, m.Media = wa.KindImage, &wa.Media{Data: []byte("not a proto")}
	mustSave(t, st, m)
	got := onlyMessage(t, st, ada, "1")
	if got.Media != nil || got.Text != "cat" {
		t.Errorf("message = %+v; want it shown, without media", got)
	}
}

func TestGroupsDetailsAndNames(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	carol := "15550000002@s.whatsapp.net"
	if err := st.SaveContacts(ctx, []wa.Contact{{JID: ada, Name: "Ada"}, {JID: bob, PushName: "bobby"}}); err != nil {
		t.Fatalf("SaveContacts: %v", err)
	}
	if err := st.SaveGroups(ctx, []wa.Group{{
		JID: group, Name: "Book club", Topic: "chapter 3",
		Participants: []wa.Participant{{JID: bob}, {JID: ada, Admin: true}, {JID: carol}},
	}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	d, err := st.Details(ctx, group)
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if d.Title() != "Book club" || d.Topic != "chapter 3" || !d.IsGroup || d.MemberCount != 3 {
		t.Errorf("details = %+v", d)
	}
	var names []string
	for _, m := range d.Members {
		names = append(names, fmt.Sprintf("%s/%v", m.Name, m.Admin))
	}
	// Admins first, then named members alphabetically, unnamed last.
	if want := []string{"Ada/true", "bobby/false", "/false"}; !slices.Equal(names, want) {
		t.Errorf("members = %v, want %v", names, want)
	}

	// A later update without participants keeps the list; a rename sticks.
	if err := st.SaveGroups(ctx, []wa.Group{{JID: group, Name: "Book club 2"}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	if d, err := st.Details(ctx, group); err != nil || d.Title() != "Book club 2" || len(d.Members) != 3 {
		t.Errorf("after rename = %+v, %v", d, err)
	}
	// Membership is replaced as a whole.
	if err := st.SaveGroups(ctx, []wa.Group{{JID: group, Participants: []wa.Participant{{JID: ada}}}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	if d, err := st.Details(ctx, group); err != nil || len(d.Members) != 1 || d.Title() != "Book club 2" {
		t.Errorf("after membership change = %+v, %v", d, err)
	}

	got, err := st.Names(ctx, []string{ada, bob, carol})
	if err != nil || got[ada] != "Ada" || got[bob] != "bobby" || got[carol] != "" {
		t.Errorf("Names = %v, %v", got, err)
	}
}

func searchIDs(t *testing.T, st *Store, q string) []string {
	t.Helper()
	hits, err := st.Search(t.Context(), q, 50)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.Message.ID)
	}
	return out
}

func TestSearch(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st,
		msg(ada, "1", t0, "Café meeting tomorrow at noon"),
		msg(ada, "2", t0.Add(time.Minute), "the meeting moved"),
		msg(bob, "3", t0.Add(2*time.Minute), "Meetings are boring"),
		msg(bob, "4", t0.Add(3*time.Minute), `quote "this" (and) that: AND OR NOT *`))

	if got := searchIDs(t, st, "meet"); !slices.Equal(got, []string{"3", "2", "1"}) {
		t.Errorf("prefix search, newest first = %v", got)
	}
	if got := searchIDs(t, st, "cafe noon"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("all words, accent-insensitive = %v", got)
	}
	if got := searchIDs(t, st, `"this" (and`); !slices.Equal(got, []string{"4"}) {
		t.Errorf("punctuation must not break the query = %v", got)
	}
	if got := searchIDs(t, st, "   "); len(got) != 0 {
		t.Errorf("blank query = %v", got)
	}

	hits, err := st.Search(ctx, "noon", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if h := hits[0]; !strings.Contains(h.Snippet, MatchStart+"noon"+MatchEnd) || h.Chat.JID != ada {
		t.Errorf("hit = %+v", h)
	}

	// The index follows edits, deletes and pruning.
	if _, err := st.EditMessage(ctx, wa.MessageEdited{Chat: ada, ID: "2", Kind: wa.KindText, Text: "the lunch moved", At: t0.Add(time.Hour)}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if got := searchIDs(t, st, "lunch"); !slices.Equal(got, []string{"2"}) {
		t.Errorf("after edit, new text = %v", got)
	}
	if got := searchIDs(t, st, "meeting"); slices.Contains(got, "2") {
		t.Errorf("after edit, old text still found: %v", got)
	}
	if err := st.DeleteMessage(ctx, wa.MessageDeleted{Chat: bob, ID: "3"}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if got := searchIDs(t, st, "boring"); len(got) != 0 {
		t.Errorf("deleted message still found: %v", got)
	}
	if _, err := st.Prune(ctx, Retention{MaxPerChat: 1}, t0); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := searchIDs(t, st, "cafe"); len(got) != 0 {
		t.Errorf("pruned message still found: %v", got)
	}
}

func TestSearchUnavailableWhenSealed(t *testing.T) {
	st := openStore(t, "", reverseCodec{})
	mustSave(t, st, msg(ada, "1", t0, "secret plans"))
	if _, err := st.Search(t.Context(), "secret", 10); !errors.Is(err, ErrSearchUnavailable) {
		t.Errorf("Search under a codec = %v, want ErrSearchUnavailable", err)
	}
	var n int
	if err := st.db.QueryRowContext(t.Context(), `SELECT count(*) FROM messages_fts`).Scan(&n); err != nil || n != 0 {
		t.Errorf("sealed text must never reach the index: %d rows (%v)", n, err)
	}
}

func TestMigrateV7ToV8IndexesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	ctx := t.Context()
	setup := append(slices.Clone(migrations[:7]), "PRAGMA user_version = 7",
		`INSERT INTO chats (jid) VALUES ('`+ada+`')`,
		`INSERT INTO messages (chat_jid, id, sender_jid, ts, from_me, kind, body) VALUES ('`+ada+`', '1', '`+ada+`', 1, 0, 'text', CAST('old pancakes' AS BLOB))`)
	for _, q := range setup {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("v7 setup %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st := openStore(t, path, nil)
	if got := searchIDs(t, st, "pancake"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("existing messages should be indexed on upgrade: %v", got)
	}
}

func TestRecorderTracksMutes(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{Paired: true}
	(&Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Attach(client)
	mutedUntil := func(jid string) time.Time {
		t.Helper()
		chats, err := st.Chats(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range chats {
			if c.JID == jid {
				return c.MutedUntil
			}
		}
		t.Fatalf("chat %s not stored", jid)
		return time.Time{}
	}
	until := t0.Add(8 * time.Hour)

	client.Emit(wa.ChatMuted{Chat: ada, Until: until}) // not stored yet: skipped, not created
	if chats, err := st.Chats(t.Context()); err != nil || len(chats) != 0 {
		t.Fatalf("mute created a chat: %v, %v", chats, err)
	}
	client.Emit(wa.HistorySync{Chats: []wa.Chat{{JID: ada, MutedUntil: until}, {JID: bob}}})
	if got := mutedUntil(ada); !got.Equal(until) {
		t.Errorf("history mute = %v, want %v", got, until)
	}
	client.Emit(wa.HistorySync{Chats: []wa.Chat{{JID: ada}}}) // history doesn't know unmutes
	if got := mutedUntil(ada); !got.Equal(until) {
		t.Errorf("history without a mute cleared it: %v", got)
	}
	client.Emit(wa.ChatMuted{Chat: ada})
	if got := mutedUntil(ada); !got.IsZero() {
		t.Errorf("unmute = %v, want zero", got)
	}
	client.Emit(wa.ChatMuted{Chat: bob, Until: wa.MutedForever})
	chats, err := st.Chats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if want := c.JID == bob; c.Muted(t0) != want {
			t.Errorf("%s muted = %v, want %v", c.JID, c.Muted(t0), want)
		}
	}
}

func TestReplyPointerAndQuotes(t *testing.T) {
	st := openStore(t, "", nil)
	orig := msg(ada, "q1", t0, "lunch?")
	gone := msg(ada, "q2", t0.Add(time.Minute), "oops")
	reply := msg(ada, "r1", t0.Add(2*time.Minute), "yes!")
	reply.ReplyTo = &wa.Reply{ID: "q1", Sender: ada}
	if _, err := st.SaveMessages(t.Context(), []wa.Message{orig, gone, reply}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMessage(t.Context(), wa.MessageDeleted{Chat: ada, ID: "q2", At: t0.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Messages(t.Context(), ada, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := msgs[2].ReplyTo; r == nil || *r != (wa.Reply{ID: "q1", Sender: ada}) {
		t.Errorf("reply pointer = %+v", r)
	}
	if msgs[0].ReplyTo != nil {
		t.Errorf("a plain message has ReplyTo %+v", msgs[0].ReplyTo)
	}
	quotes, err := st.Quotes(t.Context(), ada, []string{"q1", "q2", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if q := quotes["q1"]; q.Text != "lunch?" {
		t.Errorf("quote q1 = %+v", q)
	}
	if q := quotes["q2"]; !q.Deleted {
		t.Errorf("a deleted original should come back marked deleted: %+v", q)
	}
	if _, ok := quotes["unknown"]; ok || len(quotes) != 2 {
		t.Errorf("quotes = %v, want q1 and q2 only", quotes)
	}
}

func TestOwnReactionStoredAndRemoved(t *testing.T) {
	st := openStore(t, "", nil)
	client := &watest.Client{Paired: true}
	(&Recorder{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Attach(client)
	m := msg(ada, "m1", t0, "lunch?")
	client.Emit(m)
	mine := func() []wa.ReactionCount {
		t.Helper()
		msgs, err := st.Messages(t.Context(), ada, 0)
		if err != nil {
			t.Fatal(err)
		}
		return msgs[0].Reactions
	}
	if err := client.SendReaction(t.Context(), ada, m, "👍"); err != nil {
		t.Fatal(err)
	}
	if rs := mine(); len(rs) != 1 || rs[0].Emoji != "👍" || !rs[0].Mine {
		t.Fatalf("after reacting: %+v", rs)
	}
	if err := client.SendReaction(t.Context(), ada, m, ""); err != nil {
		t.Fatal(err)
	}
	if rs := mine(); len(rs) != 0 {
		t.Errorf("an empty reaction should remove yours: %+v", rs)
	}
}

func TestCommunityChatTitles(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	const (
		community = "120363000000000001@g.us"
		announce  = "120363000000000002@g.us"
		general   = "120363000000000003@g.us"
		orphan    = "120363000000000004@g.us" // announcements of a community we haven't heard of
	)
	if err := st.SaveGroups(ctx, []wa.Group{
		{JID: community, Name: "Class of 2035", IsCommunity: true},
		{JID: announce, Name: "Class of 2035", Community: community, Announcements: true},
		{JID: general, Name: "General", Community: community},
		{JID: orphan, Name: "Choir", Community: "120363000000000009@g.us", Announcements: true},
	}); err != nil {
		t.Fatal(err)
	}
	mustSave(t, st, msg(announce, "1", t0, "a"), msg(general, "2", t0, "b"), msg(orphan, "3", t0, "c"))
	chats, err := st.Chats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range chats {
		got[c.JID] = c.Title()
	}
	want := map[string]string{
		announce: "Class of 2035 › Announcements",
		general:  "Class of 2035 › General",
		orphan:   "Choir › Announcements",
	}
	if !maps.Equal(got, want) {
		t.Errorf("titles = %v\nwant     %v (and no chat for the community itself)", got, want)
	}
	if c, err := st.FindChat(ctx, "general"); err != nil || c.JID != general {
		t.Errorf("FindChat(general) = %+v, %v", c, err)
	}
}
