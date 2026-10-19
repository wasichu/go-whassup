package ui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// Web links in messages are underlined and made clickable with OSC 8
// hyperlinks: the terminal opens them (often on cmd- or ctrl-click);
// go-whassup itself opens nothing. The text shown is always the whole URL,
// so a link can't hide where it goes.

// linkRE matches http(s) URLs: up to whitespace or any control character,
// so nothing can break out of the escape sequence (an ESC or BEL would end
// it early).
var linkRE = regexp.MustCompile(`(?i)\bhttps?://[^\x00-\x20\x7f]+`)

var linkStyle = lipgloss.NewStyle().Underline(true)

// renderLinks makes each URL in text a clickable, underlined link.
func renderLinks(text string) string {
	return linkRE.ReplaceAllStringFunc(text, func(match string) string {
		url, rest := trimURL(match)
		return linkStyle.Hyperlink(url).Render(url) + rest
	})
}

// trimURL splits sentence punctuation off the end of a matched URL:
// "https://x.com." links "https://x.com", and a closing bracket only stays
// if the URL opened one ("…/Go_(language)").
func trimURL(s string) (url, rest string) {
	end := len(s)
	for end > 0 {
		c := s[end-1]
		switch {
		case strings.IndexByte(".,;:!?'\"*", c) >= 0:
		case c == ')' && strings.Count(s[:end], "(") < strings.Count(s[:end], ")"):
		case c == ']' && strings.Count(s[:end], "[") < strings.Count(s[:end], "]"):
		default:
			return s[:end], s[end:]
		}
		end--
	}
	return s[:end], s[end:]
}
