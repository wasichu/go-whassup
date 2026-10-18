// Package media keeps downloaded attachments on disk and hands them to the
// system's viewer. Files live in a user-only directory under the data dir,
// named by a hash of the message so chat IDs don't appear in file names.
// Cached files are plain on disk; see docs/storage.md.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sync/atomic"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Downloader fetches an attachment; wa.Client satisfies it.
type Downloader interface {
	Download(ctx context.Context, m wa.Media, dst *os.File) error
}

// Cache is the attachment cache in Dir.
type Cache struct {
	Dir string
}

func (c Cache) base(chat, id string) string {
	sum := sha256.Sum256([]byte(chat + "/" + id))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:12]))
}

// openable lists the extensions we write and hand to the system opener.
// The OS picks a handler by extension, and senders choose file names, so a
// document called "invoice.command" (macOS runs it in Terminal) or
// "x.desktop" (xdg-open can execute it) must never keep its extension.
// Anything not listed is saved as .bin and not opened automatically.
var openable = map[string]bool{
	// images
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true, ".heif": true, ".bmp": true, ".tiff": true,
	// video and audio
	".mp4": true, ".mov": true, ".m4v": true, ".3gp": true, ".webm": true, ".mkv": true,
	".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true, ".flac": true, ".amr": true,
	// documents
	".pdf": true, ".txt": true, ".md": true, ".csv": true, ".rtf": true, ".epub": true, ".vcf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true, ".pages": true, ".numbers": true, ".key": true,
	".zip": true,
}

// ErrNotOpenable means the file was saved but has a type we won't hand to
// the system opener.
var ErrNotOpenable = errors.New("unrecognised file type, not opened automatically")

// ext picks an extension viewers recognise, from the allowlist only.
func ext(m wa.Media) string {
	if e := strings.ToLower(filepath.Ext(m.FileName)); openable[e] {
		return e
	}
	mt, _, _ := strings.Cut(m.Mime, ";")
	switch mt { // mime.ExtensionsByType's first pick varies by OS for these
	case "image/jpeg":
		return ".jpg"
	case "audio/ogg":
		return ".ogg"
	}
	if exts, err := mime.ExtensionsByType(mt); err == nil {
		for _, e := range exts {
			if openable[e] {
				return e
			}
		}
	}
	return ".bin"
}

// files lists the cached files for a message (any extension). It scans the
// directory rather than using filepath.Glob, whose pattern syntax would
// misread a data dir containing '[' or '*'.
func (c Cache) files(chat, id string) ([]string, error) {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(c.base(chat, id)) + "."
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(c.Dir, e.Name()))
		}
	}
	return out, nil
}

// Lookup returns the cached file for a message, if any.
func (c Cache) Lookup(chat, id string) (string, bool) {
	files, err := c.files(chat, id)
	if err != nil || len(files) == 0 {
		return "", false
	}
	return files[0], true
}

// Fetch returns the cached file for a message, downloading it first if
// needed. Downloads go to a temp file renamed into place, so a failed or
// interrupted download never leaves a partial file behind.
func (c Cache) Fetch(ctx context.Context, d Downloader, chat, id string, m wa.Media) (string, error) {
	if path, ok := c.Lookup(chat, id); ok {
		return path, nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", fmt.Errorf("creating media dir: %w", err)
	}
	tmp, err := os.CreateTemp(c.Dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("creating download file: %w", err)
	}
	// Best effort: after a successful rename the temp name no longer exists.
	defer os.Remove(tmp.Name())
	derr := d.Download(ctx, m, tmp)
	cerr := tmp.Close()
	if err := errors.Join(derr, cerr); err != nil {
		return "", err
	}
	path := c.base(chat, id) + ext(m)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("saving download: %w", err)
	}
	return path, nil
}

// Put caches a copy of a local file (e.g. one the user just sent) for a
// message, so opening it needs no download.
func (c Cache) Put(chat, id, src string, m wa.Media) (err error) {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("creating media dir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(c.base(chat, id)+ext(m), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("caching %s: %w", src, err)
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("caching %s: %w", src, err)
	}
	return nil
}

// Remove deletes a message's cached file, if any.
func (c Cache) Remove(chat, id string) error {
	matches, err := c.files(chat, id)
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// openCommand is the system "open this file" command for goos.
func openCommand(goos, path string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", path}
	default:
		return "xdg-open", []string{path}
	}
}

// openWait is how long Open waits for the opener to report an error. Some
// openers (xdg-open's fallbacks) stay running while the viewer is open, so
// still running after this counts as success.
const openWait = 2 * time.Second

// Open hands path to the system's default application. Files with an
// extension outside the allowlist are refused (ErrNotOpenable). The
// command's output is discarded so it can't write over the TUI.
func Open(path string) error {
	if !openable[strings.ToLower(filepath.Ext(path))] {
		return ErrNotOpenable
	}
	name, args := openCommand(runtime.GOOS, path)
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening %s with %s: %w", filepath.Base(path), name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }() // also reaps the process if it outlives us
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("opening %s with %s: %w", filepath.Base(path), name, err)
		}
	case <-time.After(openWait):
	}
	return nil
}

// Switchable is a Cache whose directory can change while the app runs:
// turning encryption on moves downloads to a temporary directory (cached
// files are plaintext), turning it off moves them back. Safe for
// concurrent use; an operation uses the directory current when it starts.
type Switchable struct{ dir atomic.Pointer[string] }

// NewSwitchable returns a Switchable caching in dir.
func NewSwitchable(dir string) *Switchable {
	s := &Switchable{}
	s.SetDir(dir)
	return s
}

// SetDir switches the cache to dir. Files already cached elsewhere stay
// there (the caller removes them if needed).
func (s *Switchable) SetDir(dir string) { s.dir.Store(&dir) }

// Dir is the current directory.
func (s *Switchable) Dir() string { return *s.dir.Load() }

func (s *Switchable) cache() Cache { return Cache{Dir: s.Dir()} }

func (s *Switchable) Lookup(chat, id string) (string, bool) { return s.cache().Lookup(chat, id) }
func (s *Switchable) Put(chat, id, src string, m wa.Media) error {
	return s.cache().Put(chat, id, src, m)
}
func (s *Switchable) Remove(chat, id string) error { return s.cache().Remove(chat, id) }
func (s *Switchable) Fetch(ctx context.Context, d Downloader, chat, id string, m wa.Media) (string, error) {
	return s.cache().Fetch(ctx, d, chat, id, m)
}
