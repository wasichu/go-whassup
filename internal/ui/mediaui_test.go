package ui

import (
	"bytes"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/wa"
	"github.com/wasichu/go-whassup/internal/wa/watest"
)

// seedAttachment stores an incoming document in ada's chat.
func (h *harness) seedAttachment(id, name string) {
	h.t.Helper()
	m := wa.Message{ID: id, Chat: ada, Sender: ada, Timestamp: day.Add(time.Hour), Kind: wa.KindDocument, Text: name, Media: watest.SizedDocumentMedia(name, 2_500_000)}
	if _, err := h.st.SaveMessages(h.t.Context(), []wa.Message{m}); err != nil {
		h.t.Fatalf("seed: %v", err)
	}
	h.client.Emit(wa.HistorySync{})
	h.settle()
}

func TestOpenAttachment(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	h.seedAttachment("doc1", "report.pdf")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "report.pdf · 2.4 MB") || strings.Contains(v, "✓") {
		t.Fatalf("attachment should show its size, not cached yet:\n%s", v)
	}

	h.key("enter") // into the message box
	h.key("ctrl+o")
	if h.model().screen != screenPicker || !strings.Contains(h.view(), "report.pdf") {
		t.Fatalf("picker not shown:\n%s", h.view())
	}
	h.key("enter")
	h.settle()
	if len(h.opened) != 1 || filepath.Ext(h.opened[0]) != ".pdf" {
		t.Fatalf("opened = %v", h.opened)
	}
	if data, err := os.ReadFile(h.opened[0]); err != nil || string(data) != "%PDF-1.7" {
		t.Errorf("downloaded content = %q, %v", data, err)
	}
	h.key("esc")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "✓") {
		t.Errorf("cached attachment should show ✓:\n%s", v)
	}
	if len(h.model().downloading) != 0 {
		t.Error("download still marked in flight")
	}
}

func TestOpenAttachmentProblems(t *testing.T) {
	h := newHarness(t, true)
	h.seedAttachment("gone", "old.pdf")
	h.client.DownloadErr = wa.ErrMediaExpired
	h.key("enter") // into the message box
	h.key("ctrl+o")
	h.key("enter")
	h.settle()
	if v := h.view(); !strings.Contains(v, "no longer on WhatsApp's servers") {
		t.Errorf("expired notice missing:\n%s", v)
	}

	h2 := newHarness(t, true)
	h2.client.DownloadData = []byte("#!/bin/sh")
	h2.openErr = media.ErrNotOpenable
	h2.seedAttachment("sh", "run.command")
	h2.key("enter") // into the message box
	h2.key("ctrl+o")
	h2.key("enter")
	h2.settle()
	if v := h2.view(); !strings.Contains(v, "Not opened (unrecognised file type)") {
		t.Errorf("unrecognised-type notice missing:\n%s", v)
	}

	h3 := newHarness(t, true)
	h3.key("enter") // into the message box
	h3.key("ctrl+o")
	if h3.model().screen != screenMain || !strings.Contains(h3.view(), "No attachments") {
		t.Errorf("empty picker should just say so:\n%s", h3.view())
	}
}

func TestAttachSendsAndCaches(t *testing.T) {
	h := newHarness(t, true)
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.key("enter") // into the message box
	h.key("ctrl+t")
	if h.model().screen != screenAttach {
		t.Fatalf("attach screen not open:\n%s", h.view())
	}
	h.key(src)
	h.send(tea.KeyPressMsg{Code: tea.KeyTab})
	h.key("for you")
	h.key("enter")
	h.settle()

	sent := h.client.SentFiles()
	if len(sent) != 1 || sent[0].Path != src || sent[0].Caption != "for you" || sent[0].Chat != ada {
		t.Fatalf("sent = %+v", sent)
	}
	if _, ok := h.cache.Lookup(ada, "FILE1"); !ok {
		t.Error("own attachment should be cached under its message ID")
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "notes.txt") {
		t.Errorf("sent file not shown:\n%s", v)
	}
}

func TestAttachRejectsMissingFile(t *testing.T) {
	h := newHarness(t, true)
	h.key("enter") // into the message box
	h.key("ctrl+t")
	h.key("/nope/missing.jpg")
	h.key("enter")
	if h.model().screen != screenAttach || !strings.Contains(h.view(), "No such file") {
		t.Errorf("missing file should be reported in place:\n%s", h.view())
	}
	if len(h.client.SentFiles()) != 0 {
		t.Error("sent a missing file")
	}
}

func TestAttachCompletesPaths(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"Documents", "Downloads"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"report.pdf", ".hidden", "Documents/cv.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, true)
	h.key("enter") // into the message box
	h.key("ctrl+t")
	value := func() string { return h.model().attach.path.Value() }
	// typeKeys types s, setting any long directory prefix directly (each
	// typed key parks a cursor-blink command in the harness).
	typeKeys := func(s string) {
		if strings.HasPrefix(s, dir) {
			m := h.model()
			m.attach.path.SetValue(m.attach.path.Value() + dir)
			m.attach.path.CursorEnd()
			h.m = m
			s = strings.TrimPrefix(s, dir)
		}
		h.key(s)
		h.settle() // directory listings arrive asynchronously
	}
	tab := func() {
		h.send(tea.KeyPressMsg{Code: tea.KeyTab})
		h.settle()
	}

	typeKeys(dir + "/re")
	tab()
	if got := value(); got != dir+"/report.pdf" {
		t.Fatalf("unique match: %q", got)
	}

	m := h.model() // clear, then an ambiguous, wrong-case prefix

	m.attach.path.SetValue("")
	h.m = m
	typeKeys(dir + "/do")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Documents/") || !strings.Contains(v, "Downloads/") {
		t.Fatalf("both matches should be listed:\n%s", v)
	}
	tab() // shared prefix, real spelling
	if got := value(); got != dir+"/Do" {
		t.Fatalf("common prefix: %q", got)
	}
	tab() // nothing more shared: the highlighted match
	if got := value(); got != dir+"/Documents/" {
		t.Fatalf("highlighted match: %q", got)
	}
	tab() // into the folder: its only file
	if got := value(); got != dir+"/Documents/cv.pdf" {
		t.Fatalf("inside folder: %q", got)
	}
	tab() // complete: tab moves on to the caption
	if !h.model().attach.onCaption {
		t.Error("tab on a complete path should move to the caption")
	}

	m = h.model()
	m.attach.path.SetValue("")
	h.m = m
	h.send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // back to the path
	typeKeys(dir + "/")
	if v := ansi.Strip(h.view()); strings.Contains(v, ".hidden") {
		t.Errorf("hidden file offered before typing '.':\n%s", v)
	}
	typeKeys(".")
	tab()
	if got := value(); got != dir+"/.hidden" {
		t.Errorf("hidden after '.': %q", got)
	}

	m = h.model()
	m.attach.path.SetValue(dir + "/Downloads")
	h.m = m
	h.key("enter")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "That's a folder") {
		t.Errorf("sending a folder should be refused:\n%s", v)
	}
}

func TestAttachmentsPreviewThumbnails(t *testing.T) {
	var thumb bytes.Buffer
	if err := jpeg.Encode(&thumb, solid(32, 24, color.RGBA{R: 200, A: 255}), nil); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, true)
	h.withGraphics()
	photo := wa.Message{ID: "p1", Chat: ada, Sender: ada, Timestamp: day.Add(2 * time.Hour), Kind: wa.KindImage, Media: watest.PhotoMedia(thumb.Bytes())}
	if _, err := h.st.SaveMessages(h.t.Context(), []wa.Message{photo}); err != nil {
		t.Fatal(err)
	}
	h.seedAttachment("doc1", "report.pdf") // older, no thumbnail
	h.key("enter")                         // into the message box
	h.key("ctrl+o")
	time.Sleep(previewDelay + 20*time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); h.model().picker.preview.shown == "" && time.Now().Before(deadline); {
		h.settle()
		time.Sleep(10 * time.Millisecond)
	}
	if h.model().picker.preview.shown != "p1" || !strings.Contains(h.view(), "\U0010EEEE") {
		t.Fatalf("the photo's thumbnail should preview without downloading (downloads %d):\n%s", h.client.Downloads(), ansi.Strip(h.view()))
	}
	if h.client.Downloads() != 0 {
		t.Error("previewing must not download")
	}
	h.key("down") // the document: no preview
	h.settle()
	if h.model().picker.preview.shown != "" || strings.Contains(h.view(), "\U0010EEEE") {
		t.Error("moving to an attachment without a preview should drop the image")
	}
	h.raw = nil
	h.key("up")
	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should close attachments")
	}
}

func TestSaveAttachmentCopy(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	save := filepath.Join(t.TempDir(), "Downloads", "go-whassup")
	m := h.model()
	m.deps.Config.Media.SaveDir = save
	h.m = m
	h.seedAttachment("doc1", "report.pdf")
	h.key("enter") // into the message box
	h.key("ctrl+o")
	h.key("d")
	h.settle()
	data, err := os.ReadFile(filepath.Join(save, "report.pdf"))
	if err != nil || string(data) != "%PDF-1.7" {
		t.Fatalf("saved copy = %q, %v", data, err)
	}
	if n := h.model().notice; !strings.HasPrefix(n, "Saved to ") || !strings.HasSuffix(n, "report.pdf") {
		t.Errorf("notice = %q", n)
	}
	h.key("d") // again: a second copy, not an overwrite
	h.settle()
	if _, err := os.Stat(filepath.Join(save, "report (1).pdf")); err != nil {
		t.Errorf("second save: %v", err)
	}
	if h.client.Downloads() != 1 {
		t.Errorf("downloads = %d; the second save should use the cached file", h.client.Downloads())
	}
}
