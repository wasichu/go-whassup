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

// ext picks a file extension so viewers recognise the file.
func ext(m wa.Media) string {
	if e := filepath.Ext(m.FileName); e != "" && len(e) <= 10 {
		return strings.ToLower(e)
	}
	mt, _, _ := strings.Cut(m.Mime, ";")
	switch mt { // mime.ExtensionsByType's first pick varies by OS for these
	case "image/jpeg":
		return ".jpg"
	case "audio/ogg":
		return ".ogg"
	}
	if exts, err := mime.ExtensionsByType(mt); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// Lookup returns the cached file for a message, if any.
func (c Cache) Lookup(chat, id string) (string, bool) {
	matches, err := filepath.Glob(c.base(chat, id) + ".*")
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
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
	matches, err := filepath.Glob(c.base(chat, id) + ".*")
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

// Open hands path to the system's default application. The command's
// output is discarded (it must not write over the TUI), and it may not
// hang the caller.
func Open(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	name, args := openCommand(runtime.GOOS, path)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("opening %s with %s: %w", filepath.Base(path), name, err)
	}
	return nil
}
