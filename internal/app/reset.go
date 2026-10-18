package app

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/wasichu/go-whassup/internal/paths"
)

// resetMessages is `go-whassup db reset`: after an explicit confirmation it
// deletes messages.db (and its journal files) and the media cache. The
// session stays, so the next login doesn't need re-pairing, but history
// older than what the phone re-syncs is gone.
func resetMessages(p paths.Paths, in io.Reader, out io.Writer) error {
	if _, err := fmt.Fprintf(out, "This deletes %s and the media cache.\nStored history older than what your phone sends at the next re-link is lost.\nType yes to continue: ", p.MessagesDB); err != nil {
		return err
	}
	var answer string
	if _, err := fmt.Fscanln(in, &answer); err != nil || answer != "yes" {
		_, perr := fmt.Fprintln(out, "Not deleted.")
		return perr
	}
	var errs []error
	for _, f := range []string{p.MessagesDB, p.MessagesDB + "-wal", p.MessagesDB + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	errs = append(errs, os.RemoveAll(p.MediaDir))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("resetting: %w", err)
	}
	_, err := fmt.Fprintln(out, "Deleted. Run `go-whassup login -relink` to fetch history again. A keychain entry named go-whassup, if any, can be removed in Keychain Access.")
	return err
}
