package ui

import (
	"slices"
	"testing"

	"github.com/wasichu/go-whassup/internal/store"
)

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("xyz", "Ada Lovelace"); ok {
		t.Error("non-subsequence matched")
	}
	if _, ok := fuzzyScore("adl", "Ada Lovelace"); !ok {
		t.Error("subsequence across words should match")
	}
	prefix, _ := fuzzyScore("ada", "Ada Lovelace")
	middle, _ := fuzzyScore("ada", "Canada Dry")
	if prefix <= middle {
		t.Errorf("word-start match should score higher: %d vs %d", prefix, middle)
	}
	initials, _ := fuzzyScore("al", "Ada Lovelace")
	scattered, _ := fuzzyScore("al", "Mabel Wallace")
	if initials <= scattered {
		t.Errorf("initials should beat scattered letters: %d vs %d", initials, scattered)
	}
}

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
	if got := titles(filterDirectory(dir, "")); !slices.Equal(got, titles(dir)) {
		t.Errorf("empty query reordered: %v", got)
	}
	if got := titles(filterDirectory(dir, "ada")); !slices.Equal(got, []string{"Ada Lovelace", "Canada Dry"}) {
		t.Errorf("ada = %v", got)
	}
	if got := titles(filterDirectory(dir, "bo")); len(got) != 2 || got[0] != "Bob" {
		t.Errorf("bo = %v", got)
	}
	if got := titles(filterDirectory(dir, "+1 555-987")); !slices.Equal(got, []string{"Bob"}) {
		t.Errorf("number search = %v", got)
	}
}
