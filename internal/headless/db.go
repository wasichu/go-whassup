package headless

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/wasichu/go-whassup/internal/store"
)

// Chats prints the stored chat list, most recent first.
func (c Console) Chats(ctx context.Context, st *store.Store) error {
	chats, err := st.Chats(ctx)
	if err != nil {
		return err
	}
	if len(chats) == 0 {
		c.printf("No chats stored yet. Run `wa-tui listen` (or `login`) to sync.\n")
		return nil
	}
	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LAST ACTIVE\tCHAT\tMESSAGES\tJID")
	for _, ch := range chats {
		last := "-"
		if ch.LastMessageAt.Unix() > 0 {
			last = ch.LastMessageAt.Local().Format("2006-01-02 15:04")
		}
		title := ch.Title()
		if ch.IsGroup {
			title += " (group)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", last, title, ch.Messages, ch.JID)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// Messages prints the newest limit messages of the chat matching query
// (name, phone number or JID), oldest first.
func (c Console) Messages(ctx context.Context, st *store.Store, query string, limit int) error {
	chat, err := st.FindChat(ctx, query)
	if err != nil {
		return err
	}
	msgs, err := st.Messages(ctx, chat.JID, limit)
	if err != nil {
		return err
	}
	c.printf("%s (%s), %d of %d messages\n\n", chat.Title(), chat.JID, len(msgs), chat.Messages)
	for _, m := range msgs {
		c.printf("%s  %s: %s\n", m.Timestamp.Local().Format("2006-01-02 15:04:05"), senderLabel(m), m.Summary())
	}
	return nil
}
