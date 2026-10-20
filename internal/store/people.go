package store

import (
	"strings"

	"github.com/wasichu/go-whassup/internal/wa"
)

// How people are shown, in one place for the TUI and the headless
// commands. In order: your address book's name for them; the name they
// chose for themselves, marked "~" as WhatsApp does; their number; and for
// a privacy ID (which isn't a number) "unknown member". The SQL side of the
// same rule is personName (chats.go).

// UnknownPerson names someone WhatsApp identifies only by a privacy ID
// ("…@lid": a code, not their number) who hasn't told us a name.
const UnknownPerson = "unknown member"

// IsPrivacyID reports whether jid is a privacy ID rather than a number.
func IsPrivacyID(jid string) bool { return strings.HasSuffix(jid, "@lid") }

// NumberOf is how someone without a name is shown: "+15551234567", or
// UnknownPerson for a privacy ID or an empty JID. Other JIDs (a group
// without a subject) are returned as they are.
func NumberOf(jid string) string {
	if jid == "" || IsPrivacyID(jid) {
		return UnknownPerson
	}
	if user, ok := strings.CutSuffix(jid, "@s.whatsapp.net"); ok {
		return "+" + user
	}
	return jid
}

// PersonLabel picks how to show someone: name (already chosen by
// personName, so it may carry "~"), else "~" and the push name they sent,
// else their number. named is false for a number or UnknownPerson, which
// the TUI dims so real names stand out.
func PersonLabel(name, pushName, jid string) (label string, named bool) {
	switch {
	case name != "":
		return name, true
	case pushName != "":
		return "~" + pushName, true
	}
	return NumberOf(jid), false
}

// Author labels who sent m (not "me": callers say that their own way).
func Author(m wa.Message) (label string, named bool) {
	return PersonLabel(m.SenderName, m.PushName, m.Sender)
}
