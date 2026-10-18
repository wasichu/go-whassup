package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	// bob (no name: JID), Book club, Ada (push name fallback)
	if want := []string{bob, "Book club", "Ada"}; !slices.Equal(titles, want) {
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

	n, err := st.Prune(ctx, Retention{KeepDays: 7}, now)
	if err != nil || n != 2 {
		t.Fatalf("Prune keep_days = %d, %v; want 2", n, err)
	}
	n, err = st.Prune(ctx, Retention{MaxPerChat: 1}, now)
	if err != nil || n != 1 {
		t.Fatalf("Prune max_per_chat = %d, %v; want 1", n, err)
	}
	got, err := st.Messages(ctx, ada, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if want := []string{"new"}; !slices.Equal(ids(got), want) {
		t.Errorf("remaining = %v, want %v", ids(got), want)
	}
	if n, err := st.Prune(ctx, Retention{}, now); err != nil || n != 0 {
		t.Errorf("zero retention pruned %d, %v", n, err)
	}
}

// reverseCodec is a stand-in for real encryption: it proves bodies pass
// through the codec and that each row remembers which codec sealed it.
type reverseCodec struct{}

func (reverseCodec) ID() int { return 7 }
func (reverseCodec) Seal(p []byte) ([]byte, error) {
	out := bytes.Clone(p)
	slices.Reverse(out)
	return out, nil
}
func (c reverseCodec) Open(s []byte) ([]byte, error) { return c.Seal(s) }

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
