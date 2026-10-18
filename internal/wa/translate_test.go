package wa

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
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
				Messages: []*waHistorySync.HistorySyncMsg{webMsg("G1", "chapter 3?", true)},
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
		{JID: group.String(), Name: "Book club", IsGroup: true},
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
	w, err := Open(t.Context(), filepath.Join(t.TempDir(), "session.db"), log)
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
	h, ok := historyFromProto(data, w.cli.ParseWebMessage, w.resolveKey, log)
	if !ok || len(h.Messages) != 1 {
		t.Fatalf("history = %+v, ok = %v", h, ok)
	}
	m := h.Messages[0]
	if m.ID != "R1" || m.Chat != pnAda.String() || m.Text != "from history" || m.Timestamp.Unix() != 1700000000 {
		t.Errorf("message = %+v", m)
	}
}
