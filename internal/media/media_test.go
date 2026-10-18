package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
