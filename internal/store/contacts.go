package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wasichu/go-whassup/internal/wa"
)

// SavePushNames records the display names users chose for themselves.
func (s *Store) SavePushNames(ctx context.Context, names map[string]string) error {
	contacts := make([]wa.Contact, 0, len(names))
	for jid, name := range names {
		contacts = append(contacts, wa.Contact{JID: jid, PushName: name})
	}
	return s.SaveContacts(ctx, contacts)
}

// SaveContacts upserts contact names. Empty fields leave stored values alone,
// so partial updates (e.g. only a new push name) never erase anything.
func (s *Store) SaveContacts(ctx context.Context, contacts []wa.Contact) error {
	return s.inTx(ctx, "saving contacts", func(tx *sql.Tx) error {
		for _, c := range contacts {
			if c.JID == "" || c.Name == "" && c.PushName == "" && c.BusinessName == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO contacts (jid, name, push_name, business_name) VALUES (?, ?, ?, ?)
				ON CONFLICT (jid) DO UPDATE SET
					name          = CASE WHEN excluded.name          != '' THEN excluded.name          ELSE name          END,
					push_name     = CASE WHEN excluded.push_name     != '' THEN excluded.push_name     ELSE push_name     END,
					business_name = CASE WHEN excluded.business_name != '' THEN excluded.business_name ELSE business_name END`,
				c.JID, c.Name, c.PushName, c.BusinessName); err != nil {
				return fmt.Errorf("contact %s: %w", c.JID, err)
			}
		}
		return nil
	})
}
