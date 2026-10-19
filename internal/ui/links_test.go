package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/wa"
)

func TestTrimURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://go.dev":                                "https://go.dev",
		"https://go.dev.":                               "https://go.dev",
		"https://go.dev/doc?x=1,":                       "https://go.dev/doc?x=1",
		"https://en.wikipedia.org/wiki/Go_(language)":   "https://en.wikipedia.org/wiki/Go_(language)",
		"https://en.wikipedia.org/wiki/Go_(language)).": "https://en.wikipedia.org/wiki/Go_(language)",
		"https://go.dev)":                               "https://go.dev",
	} {
		if got, _ := trimURL(in); got != want {
			t.Errorf("trimURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinksAreClickable(t *testing.T) {
	h := newHarness(t, true)
	if _, err := h.st.SaveMessages(t.Context(), []wa.Message{{
		ID: "l1", Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: wa.KindText,
		Text: "slides: https://example.com/talk. and (see http://example.org)",
	}}); err != nil {
		t.Fatal(err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
	h.selectChat(ada)
	raw := h.view()
	for _, url := range []string{"https://example.com/talk", "http://example.org"} {
		if !strings.Contains(raw, ansi.SetHyperlink(url)) {
			t.Errorf("%s should be a hyperlink", url)
		}
	}
	v := ansi.Strip(raw)
	for _, want := range []string{"slides: https://example.com/talk. and (see", "http://example.org)"} { // wrapped
		if !strings.Contains(v, want) {
			t.Errorf("the text should read unchanged (%q):\n%s", want, v)
		}
	}
}
