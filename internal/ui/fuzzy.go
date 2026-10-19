package ui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/wasichu/go-whassup/internal/fuzzy"
	"github.com/wasichu/go-whassup/internal/store"
)

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

// filterDirectory ranks entries against query by title (so "you" finds the
// chat with yourself), or by phone number when the query is mostly digits.
// An empty query keeps the given order.
func filterDirectory(entries []store.ChatSummary, query, own string) []store.ChatSummary {
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
			score, ok = fuzzy.Score(query, chatTitle(c, own))
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
