// Package fuzzy scores how well a short query matches a name, for
// type-to-find lists (the chat finder, emoji search).
package fuzzy

import (
	"strings"
	"unicode"
)

// Score matches query as a case-insensitive subsequence of target. A
// higher score is a better match: consecutive runs and word starts earn
// bonuses, and shorter targets win ties. ok is false if it doesn't match.
func Score(query, target string) (score int, ok bool) {
	t := []rune(strings.ToLower(target))
	ti, last := 0, -2
	for _, qr := range strings.ToLower(query) {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] != qr {
				continue
			}
			score++
			if ti == last+1 {
				score += 3 // consecutive
			}
			if ti == 0 || !unicode.IsLetter(t[ti-1]) && !unicode.IsDigit(t[ti-1]) {
				score += 5 // start of a word
			}
			last, found = ti, true
			ti++
			break
		}
		if !found {
			return 0, false
		}
	}
	return score*100 - len(t), true
}
