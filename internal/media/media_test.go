package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

type fakeDownloader struct {
	data  string
	err   error
	calls int
}

func (f *fakeDownloader) Download(_ context.Context, _ wa.Media, dst *os.File) error {
	f.calls++
	if f.err != nil {
		_, _ = dst.WriteString("partial") //nolint:errcheck // simulating a failed download
		return f.err
	}
	_, err := dst.WriteString(f.data)
	return err
}

func TestFetchDownloadsOnceAndCaches(t *testing.T) {
	c := Cache{Dir: filepath.Join(t.TempDir(), "media")}
	d := &fakeDownloader{data: "JPEGDATA"}
	m := wa.Media{Mime: "image/jpeg"}

	path, err := c.Fetch(t.Context(), d, "1@s.whatsapp.net", "ABC", m)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.HasSuffix(path, ".jpg") || strings.Contains(path, "whatsapp") {
		t.Errorf("path = %s; want a hashed name with a viewer-friendly extension", path)
	}
	if got, _ := os.ReadFile(path); string(got) != "JPEGDATA" { //nolint:errcheck // compared below
		t.Errorf("content = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if dir, err := os.Stat(c.Dir); err != nil || dir.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v (%v), want 0700", dir.Mode().Perm(), err)
	}

	again, err := c.Fetch(t.Context(), d, "1@s.whatsapp.net", "ABC", m)
	if err != nil || again != path || d.calls != 1 {
		t.Errorf("second fetch = %s, %v, downloads=%d; want the cached file", again, err, d.calls)
	}
}

func TestFetchFailureLeavesNothing(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	_, err := c.Fetch(t.Context(), &fakeDownloader{err: wa.ErrMediaExpired}, "c", "id", wa.Media{FileName: "a.pdf"})
	if !errors.Is(err, wa.ErrMediaExpired) {
		t.Fatalf("err = %v, want ErrMediaExpired", err)
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("failed download left files: %v (%v)", entries, err)
	}
	if _, ok := c.Lookup("c", "id"); ok {
		t.Error("failed download is cached")
	}
}

func TestPutAndRemove(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(src, []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("c", "id", src, wa.Media{FileName: "report.pdf"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	path, ok := c.Lookup("c", "id")
	if !ok || filepath.Ext(path) != ".pdf" {
		t.Fatalf("Lookup = %s, %v", path, ok)
	}
	if err := c.Remove("c", "id"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := c.Lookup("c", "id"); ok {
		t.Error("still cached after Remove")
	}
	if err := c.Remove("c", "missing"); err != nil {
		t.Errorf("removing nothing should be fine: %v", err)
	}
}

func TestExtAndOpenCommand(t *testing.T) {
	for _, tt := range []struct {
		m    wa.Media
		want string
	}{
		{wa.Media{FileName: "Notes.PDF"}, ".pdf"},
		{wa.Media{Mime: "image/jpeg"}, ".jpg"},
		{wa.Media{Mime: "image/png"}, ".png"},
		{wa.Media{Mime: "audio/ogg; codecs=opus"}, ".ogg"},
		{wa.Media{Mime: "application/x-unknown-thing"}, ".bin"},
		// Sender-chosen names must never smuggle in an executable extension.
		{wa.Media{FileName: "invoice.command"}, ".bin"},
		{wa.Media{FileName: "x.pdf.command"}, ".bin"},
		{wa.Media{FileName: "page.html"}, ".bin"},
		{wa.Media{FileName: "app.desktop"}, ".bin"},
		{wa.Media{FileName: "run.sh", Mime: "application/x-sh"}, ".bin"},
		{wa.Media{FileName: "evil.command", Mime: "application/pdf"}, ".pdf"},
	} {
		if got := ext(tt.m); got != tt.want {
			t.Errorf("ext(%+v) = %q, want %q", tt.m, got, tt.want)
		}
	}
	if name, args := openCommand("darwin", "/x/a.jpg"); name != "open" || args[0] != "/x/a.jpg" {
		t.Errorf("darwin = %s %v", name, args)
	}
	if name, _ := openCommand("linux", "/x/a.jpg"); name != "xdg-open" {
		t.Errorf("linux = %s", name)
	}
}

func TestOpenRefusesUnlistedTypes(t *testing.T) {
	for _, p := range []string{"/tmp/x.bin", "/tmp/x.command", "/tmp/x.desktop", "/tmp/noext"} {
		if err := Open(p); !errors.Is(err, ErrNotOpenable) {
			t.Errorf("Open(%s) = %v, want ErrNotOpenable", p, err)
		}
	}
}

func TestCacheDirWithGlobCharacters(t *testing.T) {
	c := Cache{Dir: filepath.Join(t.TempDir(), "we[ird]*dir")}
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("c", "id", src, wa.Media{FileName: "a.txt"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := c.Lookup("c", "id"); !ok {
		t.Error("lookup failed in a dir with glob metacharacters")
	}
	if err := c.Remove("c", "id"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := c.Lookup("c", "id"); ok {
		t.Error("remove failed in a dir with glob metacharacters")
	}
}

func TestSwitchableFollowsDir(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	s := NewSwitchable(a)
	src := filepath.Join(t.TempDir(), "x.pdf")
	if err := os.WriteFile(src, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("c", "1", src, wa.Media{FileName: "x.pdf"}); err != nil {
		t.Fatal(err)
	}
	s.SetDir(b)
	if _, ok := s.Lookup("c", "1"); ok || s.Dir() != b {
		t.Error("after SetDir, lookups should use the new directory")
	}
	s.SetDir(a)
	if p, ok := s.Lookup("c", "1"); !ok || filepath.Dir(p) != a {
		t.Errorf("back in the first directory: %q %v", p, ok)
	}
}

func TestSaveNameAndCopy(t *testing.T) {
	at := time.Date(2026, 10, 18, 17, 38, 12, 0, time.Local)
	for _, tt := range []struct {
		m    wa.Media
		kind string
		want string
	}{
		{wa.Media{FileName: "Report Q3.pdf"}, wa.KindDocument, "Report Q3.pdf"},
		{wa.Media{FileName: "../../etc/passwd.txt"}, wa.KindDocument, "passwd.txt"},
		{wa.Media{FileName: "invoice.command", Mime: "application/pdf"}, wa.KindDocument, "invoice.command.pdf"},
		{wa.Media{FileName: ".bashrc"}, wa.KindDocument, "bashrc.bin"},
		{wa.Media{Mime: "image/jpeg"}, wa.KindImage, "WhatsApp Image 2026-10-18 at 17.38.12.jpg"},
		{wa.Media{Mime: "audio/ogg; codecs=opus"}, "audio", "WhatsApp Audio 2026-10-18 at 17.38.12.ogg"},
	} {
		if got := SaveName(tt.m, tt.kind, at); got != tt.want {
			t.Errorf("SaveName(%+v) = %q, want %q", tt.m, got, tt.want)
		}
	}

	src := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(src, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "Downloads", "go-whassup")
	first, err := SaveCopy(src, dir, "Report.pdf")
	if err != nil || filepath.Base(first) != "Report.pdf" {
		t.Fatalf("first save = %q, %v", first, err)
	}
	second, err := SaveCopy(src, dir, "Report.pdf")
	if err != nil || filepath.Base(second) != "Report (1).pdf" {
		t.Fatalf("second save = %q, %v; must not overwrite", second, err)
	}
	if fi, err := os.Stat(second); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", fi.Mode().Perm(), err)
	}
	if d, err := os.Stat(dir); err != nil || d.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, %v", d.Mode().Perm(), err)
	}
}
