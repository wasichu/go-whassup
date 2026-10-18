package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wasichu/go-whassup/internal/wa"
)

type loginStep int

const (
	loginChoose     loginStep = iota // pick a method, or re-link when paired
	loginConfirm                     // confirm re-link
	loginUnlinking                   // Logout in flight
	loginPhone                       // entering the phone number
	loginRequesting                  // waiting for the server's pairing code
	loginCode                        // code shown; waiting for the phone
	loginQR                          // QR shown; waiting for a scan
	loginFinishing                   // linked; waiting for Connected
	loginFailed
)

type loginOption int

const (
	optPhone loginOption = iota
	optQR
	optRelink
)

// login is the login screen's state. It lives in Model and changes only
// in Update.
type login struct {
	step    loginStep
	choice  int
	phone   textinput.Model
	code    string // phone pairing code
	qr      string // current QR payload
	notice  string // context or result, e.g. "Logged out by the phone"
	err     string
	jid     string // linked account, after PairSuccess
	unlinkd bool   // last re-link removed the device from the phone
	image   bool   // an inline QR image is loaded in the terminal
}

func newLogin() login {
	in := textinput.New()
	in.Prompt = "Phone: "
	in.Placeholder = "+1 555 123 4567"
	in.SetVirtualCursor(true)
	in.CharLimit = 32
	in.SetWidth(24)
	return login{phone: in}
}

// options lists what the choose step offers.
func (l login) options(paired bool) []loginOption {
	if paired {
		return []loginOption{optRelink}
	}
	return []loginOption{optPhone, optQR}
}

func (o loginOption) label() string {
	switch o {
	case optPhone:
		return "Link with a phone number code"
	case optQR:
		return "Link with a QR code"
	default:
		return "Re-link this device (unlink it, then pair again)"
	}
}

// Commands. Pairing uses the app-lifetime ctx: it bounds the connection a
// successful pairing leaves behind. Cancelling uses CancelPairing instead.
type (
	pairCodeMsg struct {
		code string
		err  error
	}
	pairQRStartedMsg struct{ err error }
	logoutMsg        struct {
		phoneUnlinked bool
		err           error
	}
)

func pairPhone(ctx context.Context, client wa.Client, phone string) tea.Cmd {
	return func() tea.Msg {
		code, err := client.Pair(ctx, phone)
		return pairCodeMsg{code: code, err: err}
	}
}

func pairQR(ctx context.Context, client wa.Client) tea.Cmd {
	return func() tea.Msg { return pairQRStartedMsg{err: client.PairQR(ctx)} }
}

func logout(ctx context.Context, client wa.Client) tea.Cmd {
	return func() tea.Msg {
		unlinked, err := client.Logout(ctx)
		return logoutMsg{phoneUnlinked: unlinked, err: err}
	}
}

func cancelPairing(client wa.Client) tea.Cmd {
	return func() tea.Msg { client.CancelPairing(); return nil }
}

// openLogin shows the login screen, starting from the choose step.
func (m Model) openLogin(notice string) Model {
	m.screen = screenLogin
	m.login = newLogin()
	m.login.notice = notice
	m.focus = focusList
	m.conv.input.Blur()
	return m
}

func (m Model) loginKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	l := &m.login
	waiting := l.step == loginRequesting || l.step == loginCode || l.step == loginQR

	if key.Matches(msg, keys.Back) {
		switch {
		case waiting:
			// First esc cancels the pairing; a second leaves the screen.
			l.step, l.code, l.qr, l.err = loginChoose, "", "", ""
			m, hide, _ := m.hideQRImage()
			return m, tea.Batch(cancelPairing(m.deps.Client), hide)
		case l.step == loginPhone || l.step == loginConfirm || l.step == loginFailed:
			l.step, l.err = loginChoose, ""
			return m, nil
		case l.step == loginUnlinking || l.step == loginFinishing:
			return m, nil // can't abandon these halfway
		}
		m.screen = screenMain
		return m, nil
	}

	switch l.step {
	case loginChoose:
		opts := l.options(m.deps.Client.IsPaired())
		switch {
		case key.Matches(msg, keys.Up):
			l.choice = max(l.choice-1, 0)
		case key.Matches(msg, keys.Down):
			l.choice = min(l.choice+1, len(opts)-1)
		case key.Matches(msg, keys.Select):
			switch opts[min(l.choice, len(opts)-1)] {
			case optPhone:
				l.step = loginPhone
				return m, l.phone.Focus()
			case optQR:
				l.step, l.err = loginQR, ""
				return m, pairQR(m.ctx, m.deps.Client)
			case optRelink:
				l.step = loginConfirm
			}
		}
	case loginConfirm:
		if msg.String() == "y" {
			l.step = loginUnlinking
			return m, logout(m.ctx, m.deps.Client)
		}
		if msg.String() == "n" {
			l.step = loginChoose
		}
	case loginPhone:
		if key.Matches(msg, keys.Select) && msg.String() == "enter" {
			if _, err := wa.NormalizePhone(l.phone.Value()); err != nil {
				l.err = err.Error()
				return m, nil
			}
			l.step, l.err = loginRequesting, ""
			return m, pairPhone(m.ctx, m.deps.Client, l.phone.Value())
		}
		var cmd tea.Cmd
		l.phone, cmd = l.phone.Update(msg)
		return m, cmd
	case loginFailed:
		if key.Matches(msg, keys.Select) {
			l.step, l.err = loginChoose, ""
		}
	}
	return m, nil
}

// loginResult handles the login commands' results.
func (m Model) loginResult(msg tea.Msg) (Model, tea.Cmd) {
	l := &m.login
	switch msg := msg.(type) {
	case pairCodeMsg:
		if l.step != loginRequesting {
			return m, nil // cancelled meanwhile
		}
		if msg.err != nil {
			l.step, l.err = loginFailed, msg.err.Error()
			return m, nil
		}
		l.step, l.code = loginCode, msg.code
	case pairQRStartedMsg:
		if msg.err != nil && l.step == loginQR {
			l.step, l.err = loginFailed, msg.err.Error()
		}
	case logoutMsg:
		if msg.err != nil && !errors.Is(msg.err, wa.ErrNotPaired) {
			l.step, l.err = loginFailed, "unlinking: "+msg.err.Error()
			return m, nil
		}
		l.unlinkd = msg.phoneUnlinked
		l.notice = "Unlinked. Your stored messages are kept."
		if !msg.phoneUnlinked {
			l.notice = "Couldn't reach WhatsApp, so only the local session was deleted.\nRemove the old go-whassup entry from your phone's Linked devices."
		}
		l.step, l.choice = loginChoose, 0
		m.conn = connNotPaired
	}
	return m, nil
}

// loginEvent updates the login screen from a protocol event. done reports
// that pairing finished and the main screen should take over.
func (m Model) loginEvent(evt wa.Event) (Model, tea.Cmd, bool) {
	l := &m.login
	switch v := evt.(type) {
	case wa.PairQRCode:
		if l.step == loginQR {
			l.qr = v.Code
			cmd := m.showQRImage() // mutates m: call before returning m
			return m, cmd, false
		}
	case wa.PairSuccess:
		l.step, l.jid = loginFinishing, v.JID
		return m.hideQRImage()
	case wa.PairFailed:
		if l.step == loginCode || l.step == loginQR || l.step == loginRequesting {
			l.step, l.err = loginFailed, v.Reason
			return m.hideQRImage()
		}
	case wa.Connected:
		if l.step == loginFinishing {
			return m, nil, true
		}
	}
	return m, nil, false
}

// inlineImages reports whether to show the QR as a Kitty-protocol image.
// Placeholders identify the image by their 24-bit foreground color, so a
// downsampled color profile would show nothing: require true color.
func (m Model) inlineImages() bool {
	return m.deps.Graphics.Kitty && m.trueColor && m.deps.Config.UI.InlineImages
}

// showQRImage uploads the current QR as an inline image, replacing any
// previous one (same id), when inline images are in use.
func (m *Model) showQRImage() tea.Cmd {
	if !m.inlineImages() || m.login.qr == "" {
		return nil
	}
	png, err := qrPNG(m.login.qr)
	if err != nil {
		m.login.err = err.Error()
		return nil
	}
	m.login.image = true
	return tea.Raw(kittyTransmit(qrImageID, png, qrImageCols, qrImageRows, m.deps.Graphics.Tmux))
}

// hideQRImage frees the inline QR image, if one is loaded.
func (m Model) hideQRImage() (Model, tea.Cmd, bool) {
	if !m.login.image {
		return m, nil, false
	}
	m.login.image = false
	return m, tea.Raw(kittyDelete(qrImageID, m.deps.Graphics.Tmux)), false
}

func (m Model) loginView(width, height int) string {
	l := m.login
	pad := lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(0, 1)
	title := paneTitleStyle.Render("Link go-whassup to WhatsApp")
	if l.step == loginQR {
		// The QR needs every row it can get: title, blank line, then the code.
		// A tip on enabling inline images costs two rows, so only when they'd
		// actually be used (setting on) and the text QR still fits.
		qr := m.qrView(width-2, height-2)
		if hint := m.deps.Graphics.Hint; hint != "" && m.deps.Config.UI.InlineImages {
			if withTip := m.qrView(width-2, height-4); !strings.Contains(withTip, "doesn't fit") || strings.Contains(qr, "doesn't fit") {
				qr = withTip + "\n\n" + faintStyle.Render("Tip: "+hint)
			}
		}
		return pad.Render(title + "\n\n" + qr)
	}

	var body []string
	if l.notice != "" && (l.step == loginChoose || l.step == loginConfirm) {
		body = append(body, faintStyle.Render(l.notice), "")
	}
	switch l.step {
	case loginChoose:
		paired := m.deps.Client.IsPaired()
		if paired {
			body = append(body, "This device is linked.", "")
		}
		for i, o := range l.options(paired) {
			line := "  " + o.label()
			if i == l.choice {
				line = activeSelStyle.Render("› " + o.label())
			}
			body = append(body, line)
		}
		if !paired {
			body = append(body, "", faintStyle.Render("A QR code needs a fairly large window (about 35 rows)."))
		}
	case loginConfirm:
		body = append(body,
			"Re-link removes go-whassup from your phone's Linked devices and deletes",
			"the session here, then pairs again. Your stored messages are kept, and",
			"the phone sends a fresh history sync.",
			"", senderStyle.Render("Re-link now? (y/n)"))
	case loginUnlinking:
		body = append(body, "Unlinking…")
	case loginPhone:
		body = append(body, "Your WhatsApp number, with country code:", "", l.phone.View())
		if l.err != "" {
			body = append(body, "", errorStyle.Render(l.err))
		}
	case loginRequesting:
		body = append(body, "Requesting a pairing code…")
	case loginCode:
		body = append(body,
			"On your phone: Settings › Linked devices › Link a device ›",
			"“Link with phone number instead”, then enter:", "",
			codeStyle.Render(spaced(l.code)), "",
			faintStyle.Render("The code expires in about 3 minutes."))
	case loginFinishing:
		body = append(body, onlineStyle.Render("Linked as "+l.jid), "Finishing setup…")
	case loginFailed:
		body = append(body, errorStyle.Render("Pairing failed: "+l.err), "", "Press enter to try again.")
	}
	return pad.Render(title + "\n\n" + strings.Join(body, "\n"))
}

// qrInstructions sit beside the QR when there's room, above it otherwise.
var qrInstructions = []string{
	"On your phone:",
	"Settings › Linked devices ›",
	"Link a device, then scan.",
	"",
	"The code refreshes every",
	"~20 seconds; keep this open.",
}

// qrView lays out the current QR in width x height: beside the instructions
// if the window is wide enough, else below a one-line instruction. It tries
// a smaller quiet zone before giving up, and then says exactly what's short.
func (m Model) qrView(width, height int) string {
	l := m.login
	if l.qr == "" {
		return "Generating a QR code…"
	}
	const gap = 3
	side := strings.Join(qrInstructions, "\n")
	sideW := lipgloss.Width(side)
	if m.inlineImages() {
		img := kittyPlaceholders(qrImageID, qrImageCols, qrImageRows)
		if qrImageCols+gap+sideW <= width {
			return lipgloss.JoinHorizontal(lipgloss.Top, img, strings.Repeat(" ", gap), side)
		}
		return "On your phone: Settings › Linked devices › Link a device, then scan:\n\n" + img
	}
	var w, h int
	for _, quiet := range qrQuietZones {
		qr, qw, qh, err := renderQR(l.qr, quiet)
		if err != nil {
			return errorStyle.Render(err.Error())
		}
		w, h = qw, qh
		if qw+gap+sideW <= width && qh <= height {
			return lipgloss.JoinHorizontal(lipgloss.Top, qr, strings.Repeat(" ", gap), side)
		}
		if qw <= width && qh+2 <= height {
			return "On your phone: Settings › Linked devices › Link a device, then scan:\n\n" + qr
		}
	}

	// Doesn't fit even with the smallest border: say exactly what's short.
	var short []string
	if rows := h - height; rows > 0 {
		short = append(short, fmt.Sprintf("%d more rows", rows))
	}
	if cols := w - width; cols > 0 {
		short = append(short, fmt.Sprintf("%d more columns", cols))
	}
	return strings.Join([]string{
		errorStyle.Render("The QR code doesn't fit: it needs " + strings.Join(short, " and ") + "."),
		"",
		"Make the window bigger or zoom out (Cmd − on macOS, Ctrl − on most",
		"Linux terminals). The code appears as soon as it fits; pairing keeps",
		"running meanwhile.",
		"",
		faintStyle.Render("Or press esc and link with a phone number code instead."),
	}, "\n")
}

// spaced renders "ABCD-EFGH" as "A B C D - E F G H" for easier reading.
func spaced(code string) string {
	return strings.Join(strings.Split(code, ""), " ")
}
