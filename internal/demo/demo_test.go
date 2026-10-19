package demo

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/store"
)

// The demo must look the same whatever the time of day: CI caught it
// collapsing just after midnight, when "today's" messages lay in the future.
func TestSeed(t *testing.T) {
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	for _, now := range []time.Time{day.Add(40 * time.Minute), day.Add(10*time.Hour + 35*time.Minute), day.Add(11 * time.Hour), day.Add(23*time.Hour + 59*time.Minute)} {
		t.Run(now.Format("15:04"), func(t *testing.T) { testSeedAt(t, now) })
	}
}

func testSeedAt(t *testing.T, now time.Time) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "messages.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck // test cleanup
	if err := seed(t.Context(), st, now); err != nil {
		t.Fatal(err)
	}
	chats, err := st.Chats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.ChatSummary{}
	for _, c := range chats {
		got[c.Title()] = c
	}
	for title, unread := range map[string]int{
		"Ada Lovelace": 0, "Grace Hopper": 2, "Book club": 1, "Alan Turing": 0,
		"Hiking crew": 3, "~Katherine J.": 0,
	} {
		c, ok := got[title]
		if !ok {
			t.Errorf("chat %q missing (have %v)", title, keys(got))
			continue
		}
		if c.Unread != unread {
			t.Errorf("%s: %d unread, want %d", title, c.Unread, unread)
		}
		if c.LastMessageAt.After(now) {
			t.Errorf("%s: last message in the future (%v)", title, c.LastMessageAt)
		}
	}
	if !got["Hiking crew"].Muted(now) || got["Book club"].Muted(now) {
		t.Error("only the hiking group should be muted")
	}
	if hits, err := st.Search(t.Context(), "moth", 5); err != nil || len(hits) != 1 {
		t.Errorf("search in the demo = %v, %v", hits, err)
	}
	msgs, err := st.Messages(t.Context(), ada, 0)
	if err != nil || len(msgs) != 5 || msgs[2].Media == nil || msgs[2].Media.Thumbnail == nil || !msgs[4].Edited {
		t.Errorf("Ada's chat: photo with thumbnail and an edit expected, got %d messages, %v", len(msgs), err)
	}
}

// Everyone in the demo must have a number reserved for fiction.
func TestOnlyFictionalNumbers(t *testing.T) {
	fictional := regexp.MustCompile(`^1\d{3}55501\d\d@s\.whatsapp\.net$`)
	for _, jid := range []string{me, ada, grace, alan, katherine, hedy} {
		if !fictional.MatchString(jid) {
			t.Errorf("%s isn't in the 555-01xx range", jid)
		}
	}
}

func keys(m map[string]store.ChatSummary) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
