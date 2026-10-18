package media

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wasichu/go-whassup/internal/wa"
)

// SaveName is the file name a saved copy gets: the sender's file name
// (cleaned of path separators and control characters), else a name like
// WhatsApp's ("WhatsApp Image 2026-10-18 at 17.38.12.jpg"). The extension
// is always one Open accepts (see ext), so a sender can't plant, say, a
// .command file in the user's downloads; a name with another extension
// keeps it and gets the safe one added.
func SaveName(m wa.Media, kind string, at time.Time) string {
	e := ext(m)
	name := cleanName(filepath.Base(strings.ReplaceAll(m.FileName, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		label := map[string]string{ // "audio" and "sticker" are derived kinds (see wa.messageContent)
			wa.KindImage: "Image", wa.KindVideo: "Video", "audio": "Audio",
			"sticker": "Sticker", wa.KindDocument: "Document",
		}[kind]
		if label == "" {
			label = "File"
		}
		return "WhatsApp " + label + " " + at.Local().Format("2006-01-02 at 15.04.05") + e
	}
	if !strings.EqualFold(filepath.Ext(name), e) {
		name += e
	}
	return name
}

// cleanName drops control characters and leading dots (no hidden files).
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == '/' {
			return -1
		}
		return r
	}, s)
	return strings.TrimLeft(strings.TrimSpace(s), ".")
}

// SaveCopy copies src into dir as name (creating dir 0700), never
// overwriting: if name exists it tries "name (1).ext", "name (2).ext", …
// The copy is 0600. It returns the path written.
func SaveCopy(src, dir, name string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	base, e := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, e)
		}
		path := filepath.Join(dir, candidate)
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, in); err != nil {
			return "", errors.Join(fmt.Errorf("saving %s: %w", path, err), out.Close(), os.Remove(path))
		}
		return path, out.Close()
	}
	return "", fmt.Errorf("too many files named %s in %s", name, dir)
}
