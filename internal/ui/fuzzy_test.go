package ui

import (
	"slices"
	"testing"

	"github.com/wasichu/go-whassup/internal/store"
)

func TestFilterDirectory(t *testing.T) {
	dir := []store.ChatSummary{
		{JID: "15551234567@s.whatsapp.net", Name: "Ada Lovelace"},
		{JID: "15559876543@s.whatsapp.net", Name: "Bob"},
		{JID: "120363000000000000@g.us", Name: "Book club", IsGroup: true},
		{JID: "15550001111@s.whatsapp.net", Name: "Canada Dry"},
	}
	titles := func(cs []store.ChatSummary) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Title())
		}
		return out
	}
	if got := titles(filterDirectory(dir, "", "")); !slices.Equal(got, titles(dir)) {
		t.Errorf("empty query reordered: %v", got)
	}
	if got := titles(filterDirectory(dir, "ada", "")); !slices.Equal(got, []string{"Ada Lovelace", "Canada Dry"}) {
		t.Errorf("ada = %v", got)
	}
	if got := titles(filterDirectory(dir, "bo", "")); len(got) != 2 || got[0] != "Bob" {
		t.Errorf("bo = %v", got)
	}
	if got := titles(filterDirectory(dir, "+1 555-987", "")); !slices.Equal(got, []string{"Bob"}) {
		t.Errorf("number search = %v", got)
	}
}

func TestFilterDirectoryFindsYourself(t *testing.T) {
	own := "12025550100@s.whatsapp.net"
	dir := []store.ChatSummary{{JID: "15551234567@s.whatsapp.net", Name: "Ada"}, {JID: own}}
	got := filterDirectory(dir, "you", own)
	if len(got) != 1 || got[0].JID != own {
		t.Errorf("you = %+v", got)
	}
}
