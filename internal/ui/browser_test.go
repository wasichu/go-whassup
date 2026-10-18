package ui

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// browseFixture is a folder with a subfolder, an image, a document and a
// dotfile.
func browseFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Pictures"), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, solid(40, 30, color.RGBA{B: 255, A: 255})); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"cat.png": buf.Bytes(), "notes.txt": []byte("hi"), ".secret": []byte("x")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// waitPreview lets the preview delay pass and, when the highlighted file
// is an image, its decode finish (slower under -race, so it polls).
func (h *harness) waitPreview() {
	h.t.Helper()
	time.Sleep(previewDelay + 20*time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		h.settle()
		b := h.model().browser
		e, ok := b.current()
		if !ok || !canPreview(e.name) || b.shown != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("preview never arrived")
}

func TestBrowserPreviewsAndPicks(t *testing.T) {
	dir := browseFixture(t)
	h := newHarness(t, true)
	h.withGraphics()
	m := h.model()
	m.browseDir = dir
	h.m = m
	h.key("enter") // into the message box
	h.key("ctrl+t")
	h.settle()
	if h.model().screen != screenBrowse {
		t.Fatalf("with image support, a should open the browser:\n%s", h.view())
	}
	v := ansi.Strip(h.view())
	if !strings.Contains(v, "Pictures/") || !strings.Contains(v, "cat.png") || strings.Contains(v, ".secret") {
		t.Fatalf("listing (folders first, no dotfiles):\n%s", v)
	}
	if strings.Index(v, "Pictures/") > strings.Index(v, "cat.png") {
		t.Errorf("folders should come first:\n%s", v)
	}

	h.key("down") // cat.png
	h.waitPreview()
	id := fmt.Sprintf("i=%d", previewImageID)
	if !h.rawContains("a=T") || !h.rawContains(id) || !strings.Contains(h.view(), "\U0010EEEE") {
		t.Fatalf("preview not sent and drawn (raw %d):\n%s", len(h.raw), ansi.Strip(h.view()))
	}

	h.raw = nil
	h.key("down") // notes.txt: the image goes
	h.waitPreview()
	if !h.rawContains("a=d") || strings.Contains(h.view(), "\U0010EEEE") || !strings.Contains(h.view(), "Sends as a document") {
		t.Errorf("leaving an image should delete its preview:\n%s", ansi.Strip(h.view()))
	}

	h.key(".") // dotfiles
	if v := ansi.Strip(h.view()); !strings.Contains(v, ".secret") {
		t.Errorf(". should show hidden files:\n%s", v)
	}
	h.key(".")

	h.key("down") // . went back to the top: cat.png is next; pick it
	h.waitPreview()
	h.key("enter")
	h.settle()
	a := h.model().attach
	if h.model().screen != screenAttach || a.path.Value() != filepath.Join(dir, "cat.png") || !a.onCaption {
		t.Fatalf("picking should fill the path and focus the caption: screen=%v path=%q caption=%v", h.model().screen, a.path.Value(), a.onCaption)
	}
	if h.model().browser.shown != "" {
		t.Error("the preview should be freed on leaving the browser")
	}

	h.key("esc")
	h.key("enter")  // into the message box
	h.key("ctrl+t") // reopens where we were
	h.settle()
	if h.model().browser.dir != dir {
		t.Errorf("browser reopened in %q, want %q", h.model().browser.dir, dir)
	}
	h.key("/") // type a path instead
	h.settle()
	if a := h.model().attach; h.model().screen != screenAttach || a.path.Value() != dir+string(filepath.Separator) || a.onCaption {
		t.Errorf("/ should switch to typing from this folder: %q", a.path.Value())
	}
}

func TestBrowserFolders(t *testing.T) {
	dir := browseFixture(t)
	h := newHarness(t, true)
	h.withGraphics()
	m := h.model()
	m.browseDir = dir
	h.m = m
	h.key("enter") // into the message box
	h.key("ctrl+t")
	h.settle()
	h.key("enter") // into Pictures/
	h.settle()
	if b := h.model().browser; b.dir != filepath.Join(dir, "Pictures") || !strings.Contains(ansi.Strip(h.view()), "Empty folder") {
		t.Fatalf("enter on a folder: dir=%q\n%s", b.dir, ansi.Strip(h.view()))
	}
	h.key("left") // back up
	h.settle()
	if h.model().browser.dir != dir {
		t.Errorf("left should go to the parent, got %q", h.model().browser.dir)
	}
	h.key("esc")
	if h.model().screen != screenMain {
		t.Error("esc should leave the browser")
	}
}

func TestAttachWithoutImagesIsTyped(t *testing.T) {
	h := newHarness(t, true) // no graphics
	h.key("enter")           // into the message box
	h.key("ctrl+t")
	if h.model().screen != screenAttach {
		t.Errorf("without image support, a should open the typed form (screen %v)", h.model().screen)
	}
}
