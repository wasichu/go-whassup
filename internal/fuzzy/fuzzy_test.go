package fuzzy

import "testing"

func TestFuzzyScore(t *testing.T) {
	if _, ok := Score("xyz", "Ada Lovelace"); ok {
		t.Error("non-subsequence matched")
	}
	if _, ok := Score("adl", "Ada Lovelace"); !ok {
		t.Error("subsequence across words should match")
	}
	prefix, _ := Score("ada", "Ada Lovelace")
	middle, _ := Score("ada", "Canada Dry")
	if prefix <= middle {
		t.Errorf("word-start match should score higher: %d vs %d", prefix, middle)
	}
	initials, _ := Score("al", "Ada Lovelace")
	scattered, _ := Score("al", "Mabel Wallace")
	if initials <= scattered {
		t.Errorf("initials should beat scattered letters: %d vs %d", initials, scattered)
	}
}
