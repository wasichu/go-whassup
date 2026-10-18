package wa

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var (
	pnAda  = types.NewJID("15551234567", types.DefaultUserServer)
	lidAda = types.NewJID("987654321", types.HiddenUserServer)
	pnMe   = types.NewJID("15550000000", types.DefaultUserServer)
	group  = types.NewJID("120363000000000000", types.GroupServer)
)

// stubResolver maps lidAda to pnAda (as the LID store would) and otherwise
// prefers a phone-number alt, mirroring WhatsApp.resolveKey.
func stubResolver(jid, alt types.JID) string {
	jid = jid.ToNonAD()
	if jid.Server != types.HiddenUserServer {
		return jid.String()
	}
	if alt.Server == types.DefaultUserServer {
		return alt.ToNonAD().String()
	}
	if jid == lidAda {
		return pnAda.String()
	}
	return jid.String()
}

func textEvent(src types.MessageSource, text string) *events.Message {
	return &events.Message{
		Info:    types.MessageInfo{MessageSource: src, ID: "M1", Timestamp: time.Unix(1700000000, 0)},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

func TestMessageFromEventKeys(t *testing.T) {
	unknownLID := types.NewJID("111", types.HiddenUserServer)
	tests := []struct {
		name       string
		src        types.MessageSource
		wantChat   string
		wantSender string
	}{
		{"phone-number DM", types.MessageSource{Chat: pnAda, Sender: pnAda}, pnAda.String(), pnAda.String()},
		{"LID DM incoming uses SenderAlt",
			types.MessageSource{Chat: lidAda, Sender: lidAda, SenderAlt: pnAda},
			pnAda.String(), pnAda.String()},
		{"LID DM outgoing uses RecipientAlt",
			types.MessageSource{Chat: lidAda, Sender: pnMe, IsFromMe: true, RecipientAlt: pnAda},
			pnAda.String(), pnMe.String()},
		{"LID without alt falls back to mapping store",
			types.MessageSource{Chat: lidAda, Sender: lidAda},
			pnAda.String(), pnAda.String()},
		{"unresolvable LID is kept, not dropped",
			types.MessageSource{Chat: unknownLID, Sender: unknownLID},
			unknownLID.String(), unknownLID.String()},
		{"group ignores sender alt for chat",
			types.MessageSource{Chat: group, Sender: lidAda, SenderAlt: pnAda, IsGroup: true},
			group.String(), pnAda.String()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := messageFromEvent(textEvent(tt.src, "hi"), stubResolver)
			if !ok {
				t.Fatal("message was skipped")
			}
			if msg.Chat != tt.wantChat || msg.Sender != tt.wantSender {
				t.Errorf("chat, sender = %q, %q; want %q, %q", msg.Chat, msg.Sender, tt.wantChat, tt.wantSender)
			}
		})
	}
}

func TestMessageFromEventSkipsStatus(t *testing.T) {
	evt := textEvent(types.MessageSource{Chat: types.StatusBroadcastJID, Sender: pnAda}, "my story")
	if _, ok := messageFromEvent(evt, stubResolver); ok {
		t.Error("status update was not skipped")
	}
}

func webMsg(id, text string, fromMe bool) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(fromMe)},
		Message:          &waE2E.Message{Conversation: proto.String(text)},
		MessageTimestamp: proto.Uint64(1700000000),
	}}
}

// stubParser stands in for Client.ParseWebMessage.
func stubParser(chat types.JID, wm *waWeb.WebMessageInfo) (*events.Message, error) {
	if wm.GetKey().GetID() == "bad" {
		return nil, errors.New("unparseable")
	}
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: wm.GetKey().GetFromMe()},
			ID:            wm.GetKey().GetID(),
			Timestamp:     time.Unix(int64(wm.GetMessageTimestamp()), 0),
		},
		Message: wm.GetMessage(),
	}, nil
}

func TestHistoryFromProto(t *testing.T) {
	data := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Pushnames: []*waHistorySync.Pushname{
			{ID: proto.String(lidAda.String()), Pushname: proto.String("Ada")},
			{ID: proto.String("not a jid@@"), Pushname: proto.String("x")},
		},
		Conversations: []*waHistorySync.Conversation{
			{ // LID-addressed DM with a phone-number alt
				ID: proto.String(lidAda.String()), PnJID: proto.String(pnAda.String()),
				DisplayName: proto.String("Ada L"),
				Messages:    []*waHistorySync.HistorySyncMsg{webMsg("A1", "hi", false), webMsg("bad", "x", false)},
			},
			{
				ID: proto.String(group.String()), Name: proto.String("Book club"),
				MuteEndTime: proto.Uint64(1700000000), // history carries seconds
				Messages:    []*waHistorySync.HistorySyncMsg{webMsg("G1", "chapter 3?", true)},
			},
			{ID: proto.String(types.StatusBroadcastJID.String()), Messages: []*waHistorySync.HistorySyncMsg{webMsg("S1", "story", false)}},
		},
	}

	h, ok := historyFromProto(data, stubParser, stubResolver, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Fatal("history skipped")
	}
	wantChats := []Chat{
		{JID: pnAda.String(), Name: "Ada L"},
		{JID: group.String(), Name: "Book club", IsGroup: true, MutedUntil: time.Unix(1700000000, 0)},
	}
	if len(h.Chats) != len(wantChats) {
		t.Fatalf("chats = %+v, want %+v", h.Chats, wantChats)
	}
	for i := range wantChats {
		if h.Chats[i] != wantChats[i] {
			t.Errorf("chat %d = %+v, want %+v", i, h.Chats[i], wantChats[i])
		}
	}
	if len(h.Messages) != 2 {
		t.Fatalf("messages = %+v, want 2 (bad and status skipped)", h.Messages)
	}
	if m := h.Messages[0]; m.ID != "A1" || m.Chat != pnAda.String() || m.Text != "hi" {
		t.Errorf("first message = %+v", m)
	}
	if m := h.Messages[1]; m.ID != "G1" || m.Chat != group.String() || !m.FromMe {
		t.Errorf("second message = %+v", m)
	}
	if got := h.PushNames[pnAda.String()]; got != "Ada" {
		t.Errorf("push names = %v, want Ada keyed by phone number", h.PushNames)
	}
}

func TestHistoryFromProtoSkipsStatusBootstrap(t *testing.T) {
	data := &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_INITIAL_STATUS_V3.Enum()}
	if _, ok := historyFromProto(data, stubParser, stubResolver, slog.New(slog.NewTextHandler(io.Discard, nil))); ok {
		t.Error("status bootstrap was not skipped")
	}
}

// TestHistoryWithRealParser guards against whatsmeow changing
// ParseWebMessage in ways the stub would hide.
func TestHistoryWithRealParser(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), log, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	data := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_RECENT.Enum(),
		Conversations: []*waHistorySync.Conversation{{
			ID:       proto.String(pnAda.String()),
			Messages: []*waHistorySync.HistorySyncMsg{webMsg("R1", "from history", false)},
		}},
	}
	h, ok := historyFromProto(data, w.client().ParseWebMessage, w.resolveKey, log)
	if !ok || len(h.Messages) != 1 {
		t.Fatalf("history = %+v, ok = %v", h, ok)
	}
	m := h.Messages[0]
	if m.ID != "R1" || m.Chat != pnAda.String() || m.Text != "from history" || m.Timestamp.Unix() != 1700000000 {
		t.Errorf("message = %+v", m)
	}
}

func TestContactsFromStore(t *testing.T) {
	bob := types.NewJID("15559876543", types.DefaultUserServer)
	all := map[types.JID]types.ContactInfo{
		pnAda:  {Found: true, FullName: "Ada Lovelace", PushName: "ada"},
		lidAda: {Found: true, PushName: "ada (lid)", BusinessName: "Analytical Engines"}, // same person via LID
		bob:    {Found: true, FirstName: "Bob"},
		types.NewJID("777", types.HiddenUserServer): {Found: true}, // no names: dropped
		group: {Found: true, FullName: "not a person"}, // groups aren't contacts
	}
	got := contactsFromStore(all, stubResolver)
	want := []Contact{
		{JID: pnAda.String(), Name: "Ada Lovelace", PushName: "ada", BusinessName: "Analytical Engines"},
		{JID: bob.String(), Name: "Bob"},
	}
	if len(got) != len(want) {
		t.Fatalf("contacts = %+v, want %+v", got, want)
	}
	for i := range want {
		// PushName of the merged entry depends on map order; either is right.
		if got[i].JID != want[i].JID || got[i].Name != want[i].Name || got[i].BusinessName != want[i].BusinessName {
			t.Errorf("contact %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[0].PushName == "" {
		t.Error("merged contact lost its push name")
	}
}

func protoEvent(src types.MessageSource, msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info:    types.MessageInfo{MessageSource: src, ID: "P1", Timestamp: time.Unix(1700000100, 0)},
		Message: msg,
	}
}

func TestClassifyMessage(t *testing.T) {
	dm := types.MessageSource{Chat: pnAda, Sender: pnAda}
	target := &waCommon.MessageKey{ID: proto.String("T1")}
	tests := []struct {
		name string
		msg  *waE2E.Message
		want Event
	}{
		{"plain message", &waE2E.Message{Conversation: proto.String("hi")},
			Message{ID: "P1", Chat: pnAda.String(), Sender: pnAda.String(), Timestamp: time.Unix(1700000100, 0), Kind: KindText, Text: "hi"}},
		{"reaction", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: target, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(1700000200000)}},
			Reaction{Chat: pnAda.String(), MessageID: "T1", Sender: pnAda.String(), Emoji: "👍", At: time.UnixMilli(1700000200000)}},
		{"reaction removed", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: target, Text: proto.String("")}},
			Reaction{Chat: pnAda.String(), MessageID: "T1", Sender: pnAda.String(), At: time.Unix(1700000100, 0)}},
		{"revoke", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: target}},
			MessageDeleted{Chat: pnAda.String(), ID: "T1", At: time.Unix(1700000100, 0)}},
		{"edit", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: target, TimestampMS: proto.Int64(1700000300000),
			EditedMessage: &waE2E.Message{Conversation: proto.String("hi (fixed)")},
		}}, MessageEdited{Chat: pnAda.String(), ID: "T1", Kind: KindText, Text: "hi (fixed)", At: time.UnixMilli(1700000300000)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := classifyMessage(protoEvent(dm, tt.msg), stubResolver, noDecrypt)
			if !ok {
				t.Fatal("not classified")
			}
			if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", tt.want) {
				t.Errorf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
	if _, ok := classifyMessage(protoEvent(dm, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_EPHEMERAL_SETTING.Enum()}}), stubResolver, noDecrypt); ok {
		t.Error("other protocol messages should be skipped")
	}
}

func TestHistoryReactions(t *testing.T) {
	web := webMsg("H1", "great news", false)
	web.Message.Reactions = []*waWeb.Reaction{
		{Key: &waCommon.MessageKey{FromMe: proto.Bool(true)}, Text: proto.String("🎉"), SenderTimestampMS: proto.Int64(1700000400000)},
	}
	data := &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_RECENT.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(pnAda.String()), Messages: []*waHistorySync.HistorySyncMsg{web}}},
	}
	h, ok := historyFromProto(data, stubParser, stubResolver, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok || len(h.Reactions) != 1 {
		t.Fatalf("reactions = %+v", h.Reactions)
	}
	if r := h.Reactions[0]; r.MessageID != "H1" || r.Emoji != "🎉" || !r.FromMe || r.Chat != pnAda.String() {
		t.Errorf("reaction = %+v", r)
	}
}

func noDecrypt(*events.Message) (*waE2E.Message, error) { return nil, errors.New("no secret") }

func TestClassifySecretEncryptedEdit(t *testing.T) {
	dm := types.MessageSource{Chat: pnAda, Sender: pnMe, IsFromMe: true, RecipientAlt: pnAda}
	sem := func(typ waE2E.SecretEncryptedMessage_SecretEncType) *waE2E.Message {
		return &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
			TargetMessageKey: &waCommon.MessageKey{ID: proto.String("T1")},
			SecretEncType:    typ.Enum(),
		}}
	}
	decryptTo := func(m *waE2E.Message) secretDecrypter {
		return func(*events.Message) (*waE2E.Message, error) { return m, nil }
	}

	// The decrypted payload may be the new content itself...
	got, ok := classifyMessage(protoEvent(dm, sem(waE2E.SecretEncryptedMessage_MESSAGE_EDIT)), stubResolver,
		decryptTo(&waE2E.Message{Conversation: proto.String("fixed typo")}))
	want := MessageEdited{Chat: pnAda.String(), ID: "T1", Kind: KindText, Text: "fixed typo", At: time.Unix(1700000100, 0)}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("edit = %#v, %v; want %#v", got, ok, want)
	}
	// ...or a protocol edit wrapping it.
	got, ok = classifyMessage(protoEvent(dm, sem(waE2E.SecretEncryptedMessage_MESSAGE_EDIT)), stubResolver,
		decryptTo(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: &waE2E.Message{Conversation: proto.String("wrapped")},
		}}))
	if e, isEdit := got.(MessageEdited); !ok || !isEdit || e.Text != "wrapped" {
		t.Errorf("wrapped edit = %#v, %v", got, ok)
	}
	// Undecryptable edits and other secret types are dropped, never stored.
	if _, ok := classifyMessage(protoEvent(dm, sem(waE2E.SecretEncryptedMessage_MESSAGE_EDIT)), stubResolver, noDecrypt); ok {
		t.Error("undecryptable edit was not dropped")
	}
	if _, ok := classifyMessage(protoEvent(dm, sem(waE2E.SecretEncryptedMessage_POLL_EDIT)), stubResolver, decryptTo(&waE2E.Message{})); ok {
		t.Error("poll edit was not dropped")
	}
}

func TestMetaMessagesAreNeverMessages(t *testing.T) {
	for name, m := range map[string]*waE2E.Message{
		"secret encrypted": {SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{}},
		"poll vote":        {PollUpdateMessage: &waE2E.PollUpdateMessage{}},
		"enc reaction":     {EncReactionMessage: &waE2E.EncReactionMessage{}},
		"pin":              {PinInChatMessage: &waE2E.PinInChatMessage{}},
		"keep in chat":     {KeepInChatMessage: &waE2E.KeepInChatMessage{}},
	} {
		if _, _, ok := messageContent(m); ok {
			t.Errorf("%s would be stored as a message", name)
		}
	}
}

func typesSourceAda() types.MessageSource { return types.MessageSource{Chat: pnAda, Sender: pnAda} }

func textEventWith(msg *waE2E.Message) *events.Message {
	e := textEvent(typesSourceAda(), "")
	e.Message = msg
	return e
}

func TestGroupFromInfo(t *testing.T) {
	info := &types.GroupInfo{
		JID:        group,
		GroupName:  types.GroupName{Name: "Book club"},
		GroupTopic: types.GroupTopic{Topic: "chapter 3 this week"},
		Participants: []types.GroupParticipant{
			{JID: lidAda, PhoneNumber: pnAda, IsAdmin: true},
			{JID: lidAda}, // LID only: resolved by the mapping store
			{JID: pnMe, IsSuperAdmin: true},
		},
	}
	g := groupFromInfo(info, stubResolver)
	want := Group{JID: group.String(), Name: "Book club", Topic: "chapter 3 this week", Participants: []Participant{
		{JID: pnAda.String(), Admin: true}, {JID: pnAda.String()}, {JID: pnMe.String(), Admin: true},
	}}
	if !reflect.DeepEqual(g, want) {
		t.Errorf("group = %+v\nwant  %+v", g, want)
	}
}

func TestNormalizeMentions(t *testing.T) {
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String("hey @987654321 and @15559876543"),
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{lidAda.String(), "15559876543@s.whatsapp.net"}},
	}}
	got, ok := messageFromEvent(textEventWith(msg), stubResolver)
	if !ok || got.Text != "hey @15551234567 and @15559876543" {
		t.Errorf("text = %q", got.Text)
	}
}
