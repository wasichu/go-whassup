package ui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/wasichu/go-whassup/internal/store"
)

// fuzzyScore matches query as a case-insensitive subsequence of target. A
// higher score is a better match: consecutive runs and word starts earn
// bonuses, and shorter targets win ties. ok is false if it doesn't match.
func fuzzyScore(query, target string) (score int, ok bool) {
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(target))
	ti, last := 0, -2
	for _, qr := range q {
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

// digitsOnly keeps the digits of s, for matching phone numbers typed with
// spaces, dashes or a leading +.
func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// filterDirectory ranks entries against query by name, or by phone number
// when the query is mostly digits. An empty query keeps the given order.
func filterDirectory(entries []store.ChatSummary, query string) []store.ChatSummary {
	query = strings.TrimSpace(query)
	if query == "" {
		return entries
	}
	digits := digitsOnly(query)
	byNumber := len(digits) >= 3 && len(digits)*2 >= len(query)
	type scored struct {
		c     store.ChatSummary
		score int
	}
	var hits []scored
	for _, c := range entries {
		var score int
		var ok bool
		if byNumber {
			user, _, _ := strings.Cut(c.JID, "@")
			if strings.Contains(user, digits) {
				score, ok = 1000-len(user), true
			}
		} else {
			score, ok = fuzzyScore(query, c.Title())
		}
		if ok {
			hits = append(hits, scored{c, score})
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
	out := make([]store.ChatSummary, len(hits))
	for i, h := range hits {
		out[i] = h.c
	}
	return out
}
