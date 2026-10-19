package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

func TestCommunityGroupsListedUnderAHeading(t *testing.T) {
	h := newHarness(t, true)
	const (
		community = "120363000000000001@g.us"
		announce  = "120363000000000002@g.us"
		general   = "120363000000000003@g.us"
	)
	if err := h.st.SaveGroups(t.Context(), []wa.Group{
		{JID: community, Name: "Class of 2035", IsCommunity: true},
		{JID: announce, Name: "Class of 2035", Community: community, Announcements: true},
		{JID: general, Name: "General", Community: community},
	}); err != nil {
		t.Fatal(err)
	}
	// Newest first: General, then Ada, then Announcements. The community's
	// groups stay together, where General (its newest) is.
	at := day.Add(5 * time.Hour)
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{
		{ID: "g1", Chat: general, Sender: ada, Timestamp: at, Kind: wa.KindText, Text: "hi all"},
		{ID: "n1", Chat: announce, Sender: ada, Timestamp: day.Add(-time.Hour), Kind: wa.KindText, Text: "trip friday"},
	}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()

	var rows []string
	for _, line := range strings.Split(ansi.Strip(h.view()), "\n") {
		left, _, _ := strings.Cut(line, "│")
		left = badgeRE.ReplaceAllString(strings.TrimRight(left, " "), "") // unread badges
		if s := left; s != "" {
			rows = append(rows, s)
		}
	}
	got := strings.Join(rows[:min(5, len(rows))], "\n")
	want := "Chats\n Class of 2035\n   General\n   Announcements\n Ada"
	if got != want {
		t.Fatalf("list =\n%s\nwant\n%s", got, want)
	}

	// The heading can't be selected: up from General (the first group)
	// stays on General.
	h.selectChat(general)
	h.key("up")
	if jid := selectedJID(h); jid != general {
		t.Errorf("up from the first group = %s, want General", jid)
	}
	h.key("down")
	h.key("down")
	h.key("up")
	if jid := selectedJID(h); jid != announce {
		t.Errorf("after down, down, up: %s, want Announcements", jid)
	}
	// Elsewhere the full name says which community it is.
	h.key("enter")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Class of 2035 › Announcements") {
		t.Errorf("conversation header should name the community:\n%s", v)
	}
}

func selectedJID(h *harness) string {
	m := h.model()
	return m.chats.selectedJID()
}

var badgeRE = regexp.MustCompile(`\s{2,}\d+$`)
