package demo

import (
	"context"
	"time"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

// The cast. Numbers are in the fictional 555-01xx range.
const (
	me        = "12025550100@s.whatsapp.net"
	ada       = "12025550101@s.whatsapp.net"
	grace     = "12025550102@s.whatsapp.net"
	alan      = "12025550103@s.whatsapp.net"
	katherine = "12025550104@s.whatsapp.net" // not in the address book: shown by the name she chose
	hedy      = "12025550105@s.whatsapp.net"
	bookClub  = "120363000000000001@g.us"
	hiking    = "120363000000000002@g.us"
)

// seed fills st with the demo's chats, as if synced from a phone. Times are
// relative to now, so day separators and "new" dividers look natural.
func seed(ctx context.Context, st *store.Store, now time.Time) error {
	// "Today" is the latest day whose story (up to 10:31) is all in the
	// past: before 11:00 the demo's day is yesterday. Clamping times to now
	// instead would bunch messages up and undo the unread state.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if now.Before(today.Add(11 * time.Hour)) {
		today = today.AddDate(0, 0, -1)
	}
	at := func(daysAgo int, hm string) time.Time {
		t, _ := time.Parse("15:04", hm) //nolint:errcheck // fixed literals
		return today.AddDate(0, 0, -daysAgo).Add(time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute)
	}
	if err := seedPeople(ctx, st); err != nil {
		return err
	}
	var msgs []wa.Message
	for _, chat := range [][]wa.Message{adaChat(at), graceChat(at), bookClubChat(at), alanChat(at), hikingChat(at), katherineChat(at), notesToSelf(at)} {
		msgs = append(msgs, chat...)
	}
	if _, err := st.SaveMessages(ctx, msgs); err != nil {
		return err
	}
	return seedChanges(ctx, st, at, now)
}

func seedPeople(ctx context.Context, st *store.Store) error {
	if err := st.SaveContacts(ctx, []wa.Contact{
		{JID: ada, Name: "Ada Lovelace"}, {JID: grace, Name: "Grace Hopper"},
		{JID: alan, Name: "Alan Turing"}, {JID: hedy, Name: "Hedy Lamarr"},
		{JID: katherine, PushName: "Katherine J."}, {JID: me, PushName: "Sam"},
	}); err != nil {
		return err
	}
	return st.SaveGroups(ctx, []wa.Group{
		{JID: bookClub, Name: "Book club", Topic: "This month: The Left Hand of Darkness. Meetings Thursdays at 7.",
			Participants: []wa.Participant{{JID: ada, Admin: true}, {JID: grace}, {JID: alan}, {JID: me}}},
		{JID: hiking, Name: "Hiking crew", Topic: "Weekend trails",
			Participants: []wa.Participant{{JID: hedy, Admin: true}, {JID: katherine}, {JID: me}}},
	})
}

type clock func(daysAgo int, hm string) time.Time

func text(id, chat, sender string, t time.Time, body string) wa.Message {
	return wa.Message{ID: id, Chat: chat, Sender: sender, FromMe: sender == me, IsGroup: chat == bookClub || chat == hiking,
		Timestamp: t, Kind: wa.KindText, Text: body}
}

func adaChat(at clock) []wa.Message {
	photo := text("a3", ada, ada, at(1, "18:20"), "The difference engine, sketched")
	photo.Kind, photo.Media = wa.KindImage, watest.PhotoMedia(sceneJPEG(96, 72))
	return []wa.Message{
		text("a1", ada, ada, at(1, "18:02"), "Did you see the new analytical engine notes?"),
		text("a2", ada, me, at(1, "18:05"), "Not yet, send them over!"),
		photo,
		text("a4", ada, me, at(0, "09:15"), "Wow, look at all those gears"),
		text("a5", ada, ada, at(0, "09:20"), "Tea on Saturday?"),
	}
}

func graceChat(at clock) []wa.Message {
	return []wa.Message{
		text("g1", grace, me, at(2, "16:40"), "How did the demo go?"),
		text("g2", grace, grace, at(0, "10:30"), "Found the bug. Literally a moth 🦋"),
		text("g3", grace, grace, at(0, "10:31"), "Coffee later?"),
	}
}

func bookClubChat(at clock) []wa.Message {
	reply := text("b3", bookClub, me, at(1, "20:15"), "Almost! No spoilers please")
	reply.ReplyTo = &wa.Reply{ID: "b2", Sender: grace}
	return []wa.Message{
		text("b1", bookClub, alan, at(1, "20:10"), "Chapter 3 is wild"),
		text("b2", bookClub, grace, at(1, "20:12"), "@"+user(me)+" are you caught up?"),
		reply,
		text("b4", bookClub, alan, at(1, "20:16"), "(spoiler, deleted)"),
		text("b5", bookClub, ada, at(0, "08:45"), "Meeting Thursday at 7? I'll bring the tea."),
	}
}

func alanChat(at clock) []wa.Message {
	doc := text("t2", alan, alan, at(3, "11:02"), "bletchley-itinerary.txt")
	doc.Kind, doc.Media = wa.KindDocument, watest.SizedDocumentMedia("bletchley-itinerary.txt", 2048)
	return []wa.Message{
		text("t1", alan, alan, at(3, "11:00"), "Here's the plan for Saturday"),
		doc,
		text("t3", alan, me, at(3, "11:30"), "Perfect, thanks!"),
	}
}

func hikingChat(at clock) []wa.Message {
	return []wa.Message{
		text("h1", hiking, hedy, at(0, "07:05"), "Trail conditions look great this weekend"),
		text("h2", hiking, katherine, at(0, "07:12"), "Calculated: 11.4 km, 620 m of climbing"),
		text("h3", hiking, hedy, at(0, "07:13"), "Of course you did 😄"),
	}
}

func katherineChat(at clock) []wa.Message {
	m := text("k1", katherine, katherine, at(5, "14:00"), "Trajectory numbers attached in the group. Check my work?")
	m.PushName = "Katherine J."
	return []wa.Message{m, text("k2", katherine, me, at(5, "14:20"), "Checked twice. Spot on.")}
}

func notesToSelf(at clock) []wa.Message {
	return []wa.Message{text("n1", me, me, at(4, "21:00"), "Shopping: oat milk, AA batteries, a new notebook")}
}

// seedChanges applies what happened after the messages: reactions, an edit,
// a delete, a mute, and what you've already read.
func seedChanges(ctx context.Context, st *store.Store, at clock, now time.Time) error {
	if err := st.SaveReactions(ctx, []wa.Reaction{
		{Chat: ada, MessageID: "a4", Sender: ada, Emoji: "❤️", At: at(0, "09:16")},
		{Chat: bookClub, MessageID: "b1", Sender: ada, Emoji: "👍", At: at(1, "20:11")},
		{Chat: bookClub, MessageID: "b1", Sender: me, FromMe: true, Emoji: "👍", At: at(1, "20:14")},
		{Chat: bookClub, MessageID: "b5", Sender: grace, Emoji: "🍵", At: at(0, "08:50")},
	}); err != nil {
		return err
	}
	if _, err := st.EditMessage(ctx, wa.MessageEdited{Chat: ada, ID: "a5", Kind: wa.KindText, Text: "Tea on Saturday? My place, 3pm", At: at(0, "09:21")}); err != nil {
		return err
	}
	if err := st.DeleteMessage(ctx, wa.MessageDeleted{Chat: bookClub, ID: "b4", At: at(1, "20:17")}); err != nil {
		return err
	}
	if err := st.SetMuted(ctx, hiking, wa.MutedForever); err != nil {
		return err
	}
	// Read up to now, except what's left unread: Grace's two messages, the
	// book club's latest and the hiking group.
	for chat, upTo := range map[string]time.Time{
		ada: now, alan: now, katherine: now, me: now,
		grace: at(0, "10:00"), bookClub: at(0, "08:00"), hiking: at(1, "23:00"),
	} {
		if err := st.MarkRead(ctx, chat, upTo); err != nil {
			return err
		}
	}
	return nil
}
