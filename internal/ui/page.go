package ui

import (
	"context"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/wasichu/go-whassup/internal/store"
	"github.com/wasichu/go-whassup/internal/wa"
)

// page is a full-screen view opened from the main screen: finder, search,
// attachments, send file, chat info and settings. Its state lives in a
// Model field (so it survives Update's value copies); the root routes keys
// and any message it doesn't handle itself to the active page, and renders
// it. The main and login screens stay in the root: they react to protocol
// events and own focus, drafts and pairing.
type page interface {
	// update handles a key or other message; done returns to the main
	// screen.
	update(r rootAPI, msg tea.Msg) (done bool, cmd tea.Cmd)
	view(r rootAPI, width, height int) string
	help() []key.Binding
}

// page returns the active page, or nil on the main and login screens.
func (m *Model) page() page {
	switch m.screen {
	case screenSettings:
		return &m.settings
	case screenFinder:
		return &m.finder
	case screenPicker:
		return &m.picker
	case screenAttach:
		return &m.attach
	case screenInfo:
		return &m.info
	case screenSearch:
		return &m.search
	}
	return nil
}

// updatePage hands msg to the active page; ok is false if there is none.
func (m Model) updatePage(msg tea.Msg) (_ Model, _ tea.Cmd, ok bool) {
	p := m.page()
	if p == nil {
		return m, nil, false
	}
	done, cmd := p.update(rootAPI{&m}, msg)
	if done {
		m.screen = screenMain
	}
	return m, cmd, true
}

// rootAPI is what a page may use from the root model. It points at the
// Model value being updated or rendered, so it must not outlive the call
// (never capture it in a tea.Cmd).
type rootAPI struct{ m *Model }

func (r rootAPI) ctx() context.Context            { return r.m.ctx }
func (r rootAPI) deps() Deps                      { return r.m.deps }
func (r rootAPI) own() string                     { return r.m.ownJID }
func (r rootAPI) chat() store.ChatSummary         { return r.m.conv.chat }
func (r rootAPI) cached(id string) bool           { return r.m.conv.cached[id] }
func (r rootAPI) downloading(id string) bool      { return r.m.downloading[id] }
func (r rootAPI) notify(text string)              { r.m.notice, r.m.noticeIsErr = text, false }
func (r rootAPI) setError(what string, err error) { r.m.setError(what, err) }

// openChat leaves the page for chat c (see Model.openChat).
func (r rootAPI) openChat(c store.ChatSummary) tea.Cmd {
	var cmd tea.Cmd
	*r.m, cmd = r.m.openChat(c)
	return cmd
}

// jumpTo opens a search hit's chat at the message (see Model.jumpTo).
func (r rootAPI) jumpTo(hit store.SearchHit) tea.Cmd {
	var cmd tea.Cmd
	*r.m, cmd = r.m.jumpTo(hit)
	return cmd
}

// download fetches and opens an attachment (see Model.download).
func (r rootAPI) download(item wa.Message) tea.Cmd { return r.m.download(item) }
