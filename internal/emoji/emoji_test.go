package emoji

import (
	"slices"
	"testing"
)

func glyphs(es []Emoji) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Glyph)
	}
	return out
}

func TestEmbeddedData(t *testing.T) {
	if n := len(All()); n < 1500 {
		t.Fatalf("only %d emoji embedded", n)
	}
	for _, e := range All() {
		if e.Glyph == "" || e.Name == "" {
			t.Fatalf("bad entry %+v", e)
		}
	}
}

func TestSearch(t *testing.T) {
	for _, tt := range []struct {
		query string
		want  string
		top   int // within the first top results
	}{
		{"heart", "❤️", 1},
		{"thumbs", "👍", 1},
		{"laugh", "😂", 3},
		{"lol", "😂", 3},
		{"LAUGH", "😂", 3}, // case doesn't matter
		{"tears of", "😂", 3},
		{"pizz", "🍕", 1},
	} {
		got := glyphs(Search(tt.query, tt.top))
		if !slices.Contains(got, tt.want) {
			t.Errorf("Search(%q) top %d = %v, want %s among them", tt.query, tt.top, got, tt.want)
		}
	}
	if got := Search("qzxqzx", 10); len(got) != 0 {
		t.Errorf("nonsense matched %v", glyphs(got))
	}
	if got := Search("", 3); len(got) != 3 || got[0].Glyph != "😀" {
		t.Errorf("empty query should list emoji in Unicode order, got %v", glyphs(got))
	}
}
