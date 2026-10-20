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
	if v := ansi.Strip(h2.view()); !strings.Contains(v, "Not opened: unrecognised file type. Press d to save a copy.") ||
		strings.Contains(v, h2.cache.Dir) {
		t.Errorf("unrecognised-type notice should say what to do, not where the cache file is:\n%s", v)
	}

	h3 := newHarness(t, true)
	h3.key("enter") // into the message box
	h3.key("ctrl+o")
	if h3.model().screen != screenMain || !strings.Contains(h3.view(), "No attachments") {
		t.Errorf("empty picker should just say so:\n%s", h3.view())
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
	h.waitFor("the saved copy", func() bool { return strings.HasPrefix(h.model().notice.text, "Saved to ") })
	data, err := os.ReadFile(filepath.Join(save, "report.pdf"))
	if err != nil || string(data) != "%PDF-1.7" {
		t.Fatalf("saved copy = %q, %v", data, err)
	}
	if n := h.model().notice.text; !strings.HasPrefix(n, "Saved to ") || !strings.HasSuffix(n, "report.pdf") {
		t.Errorf("notice = %q", n)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "esc back") || !strings.Contains(v, "d save a copy") {
		t.Errorf("the attachments view should show its keys, esc included:\n%s", v)
	}
	h.key("d") // again: a second copy, not an overwrite
	h.waitFor("the second copy", func() bool { return strings.HasSuffix(h.model().notice.text, "report (1).pdf") })
	if _, err := os.Stat(filepath.Join(save, "report (1).pdf")); err != nil {
		t.Errorf("second save: %v", err)
	}
	if h.client.Downloads() != 1 {
		t.Errorf("downloads = %d; the second save should use the cached file", h.client.Downloads())
	}
}

func TestOpenedNoticeIsShortAndClearsOnMove(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	h.seedAttachment("doc1", "report.pdf")
	h.seedAttachment("doc2", "notes.pdf")
	h.key("enter") // into the message box
	h.key("ctrl+o")
	h.key("enter") // open the newest
	h.settle()
	if n := h.model().notice.text; n != "File opened." {
		t.Fatalf("notice = %q, want just \"File opened.\" (no cache file name)", n)
	}
	h.key("down")
	if n := h.model().notice.text; n != "" {
		t.Errorf("moving to another attachment should clear the notice, got %q", n)
	}
}

func TestOpenedNoticeGoesWhenLeaving(t *testing.T) {
	h := newHarness(t, true)
	h.client.DownloadData = []byte("%PDF-1.7")
	h.seedAttachment("doc1", "report.pdf")
	h.key("enter") // into the message box
	h.key("ctrl+o")
	h.key("enter")
	h.settle()
	if h.model().notice.text != "File opened." {
		t.Fatalf("notice = %q", h.model().notice.text)
	}
	h.key("esc")
	if n := h.model().notice.text; n != "" || h.model().screen != screenMain {
		t.Errorf("esc back to the chat should clear the notice, got %q", n)
	}

	// A download that finishes after leaving doesn't announce itself.
	h.key("ctrl+o")
	h.key("esc")
	h.send(mediaOpenedMsg{chat: ada, id: "doc1", path: "/cache/abc.pdf"})
	if n := h.model().notice.text; n != "" {
		t.Errorf("late \"File opened.\" on the chat screen: %q", n)
	}
}
