package store

import "testing"

func TestPersonLabel(t *testing.T) {
	for _, tt := range []struct {
		name, push, jid string
		want            string
		named           bool
	}{
		{"Ada Lovelace", "Ada", "15551234567@s.whatsapp.net", "Ada Lovelace", true},
		{"+1 Fan", "", "15551234567@s.whatsapp.net", "+1 Fan", true}, // a name, even if it looks like a number
		{"~Ada", "", "15551234567@s.whatsapp.net", "~Ada", true},     // personName already marked it
		{"", "Ada", "15551234567@s.whatsapp.net", "~Ada", true},
		{"", "", "15551234567@s.whatsapp.net", "+15551234567", false},
		{"", "", "98765432109876@lid", UnknownPerson, false},
		{"", "", "", UnknownPerson, false},
	} {
		got, named := PersonLabel(tt.name, tt.push, tt.jid)
		if got != tt.want || named != tt.named {
			t.Errorf("PersonLabel(%q, %q, %q) = %q, %v; want %q, %v", tt.name, tt.push, tt.jid, got, named, tt.want, tt.named)
		}
	}
	if got := NumberOf("120363000000000000@g.us"); got != "120363000000000000@g.us" {
		t.Errorf("a group JID should pass through, got %q", got)
	}
}
