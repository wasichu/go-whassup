package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/crypt"
	"github.com/wasichu/go-whassup/internal/keysource"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
)

// The at-rest encryption sequence lives here, top to bottom, so it can be
// read in one place. See docs/storage.md ("Encryption decisions").

const alg = "aes-256-gcm"

// keySources lets tests replace how secrets are obtained.
type keySources struct {
	keyring interface {
		Get(ctx context.Context) ([]byte, error)
		Put(ctx context.Context, key []byte) error
	}
	passphrase func(ctx context.Context, src string, confirm bool) ([]byte, error)
}

func realKeySources(cfg config.Config, p paths.Paths, stdio IO) keySources {
	return keySources{
		keyring: keysource.NewKeyring(),
		passphrase: func(ctx context.Context, src string, confirm bool) ([]byte, error) {
			if src == config.KeySourcePasswordCommand {
				return keysource.Command{Cmd: cfg.Encryption.PasswordCommand, ConfigPath: p.ConfigFile}.Passphrase(ctx)
			}
			in, _ := stdio.In.(*os.File)
			label := "Passphrase for stored messages: "
			if confirm {
				label = "New passphrase for stored messages (encrypts message text only; who you talk to and when stays readable): "
			}
			return keysource.Prompt{In: in, Out: stdio.Err}.Passphrase(label, confirm)
		},
	}
}

// ErrLocked means stored messages couldn't be unlocked; the message says
// how to recover.
var ErrLocked = errors.New("can't unlock stored messages")

// encryption is the outcome of setUpEncryption.
type encryption struct {
	on      bool // messages are (being) encrypted: no message text in logs, temp media
	pending int  // values still to re-seal in the background
	finish  func(context.Context) error
}

// setUpEncryption unlocks the stored key and starts any transition the
// config asks for. transitions=false (db commands) only unlocks.
func setUpEncryption(ctx context.Context, cfg config.Config, p paths.Paths, st *store.Store, ks keySources, transitions bool, log *slog.Logger) (encryption, error) {
	key, err := st.Key(ctx)
	if err != nil {
		return encryption{}, err
	}
	want := cfg.Encryption.Enabled

	// 1. A key exists: unlock it from the source it was created with.
	if key != nil {
		codec, err := unlock(ctx, key, ks)
		if err != nil {
			return encryption{}, err
		}
		st.Unlock(codec, want || !transitions)
		if transitions && !want {
			st.WritePlaintext() // turning it off: decrypt in the background
		}
	}

	// 2. Turning it on: no plaintext media may remain next to encrypted
	// messages, then create and store the key (AddKey also empties the
	// search index), then switch writes to it.
	if key == nil && want && transitions {
		if err := os.RemoveAll(p.MediaDir); err != nil {
			return encryption{}, fmt.Errorf("removing unencrypted media cache: %w", err)
		}
		codec, err := createKey(ctx, cfg.Encryption.KeySource, st, ks)
		if err != nil {
			return encryption{}, err
		}
		st.Unlock(codec, true)
		log.Info("encryption enabled", "key", codec.ID())
	}

	// "On" as long as any key exists: while decrypting, sealed values remain.
	e := encryption{on: want || key != nil}
	if !transitions {
		return e, nil
	}
	if e.pending, err = st.Pending(ctx); err != nil {
		return encryption{}, err
	}
	switch {
	case want && (key == nil || e.pending > 0):
		e.finish = st.FinishEncrypting
	case !want && key != nil:
		e.finish = st.FinishDecrypting
	}
	return e, nil
}

func unlock(ctx context.Context, key *store.StoredKey, ks keySources) (*store.KeyCodec, error) {
	var kek []byte
	var err error
	switch key.Source {
	case config.KeySourceKeyring:
		kek, err = ks.keyring.Get(ctx)
		if errors.Is(err, keysource.ErrNotFound) {
			return nil, fmt.Errorf("%w: the keychain entry is missing. Restore it, or run `wa-tui db reset` to delete stored messages (history older than what your phone syncs is lost)", ErrLocked)
		}
	default:
		var pass []byte
		if pass, err = ks.passphrase(ctx, key.Source, false); err == nil {
			if key.KDF == nil {
				return nil, fmt.Errorf("%w: key %d has no key-derivation parameters", ErrLocked, key.ID)
			}
			kek, err = crypt.DeriveKEK(pass, key.Salt, *key.KDF)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w (key source %s): %w", ErrLocked, key.Source, err)
	}
	dek, err := crypt.Unwrap(kek, key.Wrapped)
	if errors.Is(err, crypt.ErrDecrypt) {
		return nil, fmt.Errorf("%w: wrong passphrase or keychain secret (key source %s). Fix the key source, or run `wa-tui db reset` to delete stored messages (history older than what your phone syncs is lost)", ErrLocked, key.Source)
	}
	if err != nil {
		return nil, err
	}
	return store.NewKeyCodec(key.ID, dek)
}

func createKey(ctx context.Context, source string, st *store.Store, ks keySources) (*store.KeyCodec, error) {
	dek, err := crypt.NewKey()
	if err != nil {
		return nil, err
	}
	stored := store.StoredKey{Alg: alg, Source: source}
	var kek []byte
	if source == config.KeySourceKeyring {
		if kek, err = crypt.NewKey(); err != nil {
			return nil, err
		}
		if err := ks.keyring.Put(ctx, kek); err != nil {
			return nil, err
		}
	} else {
		pass, err := ks.passphrase(ctx, source, true)
		if err != nil {
			return nil, err
		}
		if stored.Salt, err = crypt.NewSalt(); err != nil {
			return nil, err
		}
		params := crypt.DefaultKDF
		stored.KDF = &params
		if kek, err = crypt.DeriveKEK(pass, stored.Salt, params); err != nil {
			return nil, err
		}
	}
	if stored.Wrapped, err = crypt.Wrap(kek, dek); err != nil {
		return nil, err
	}
	id, err := st.AddKey(ctx, stored)
	if err != nil {
		return nil, err
	}
	return store.NewKeyCodec(id, dek)
}

// resealBatch is how many messages each background transaction re-seals.
const resealBatch = 500

// startReseal re-seals in the background until done (then runs finish) or
// ctx ends. It stops on the first error rather than retrying: a value that
// can't be opened would fail forever. Wait for it before closing the store.
func startReseal(ctx context.Context, st *store.Store, e encryption, log *slog.Logger) (wait func()) {
	if e.finish == nil {
		return func() {}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		total := 0
		for ctx.Err() == nil {
			n, err := st.ResealBatch(ctx, resealBatch)
			if err != nil {
				log.Error("re-sealing stopped; it resumes next start", "done", total, "err", err)
				return
			}
			if n == 0 {
				break
			}
			total += n
			log.Info("re-sealing", "done", total, "of", e.pending)
		}
		if ctx.Err() != nil {
			log.Info("re-sealing paused; it resumes next start", "done", total)
			return
		}
		if err := e.finish(ctx); err != nil {
			log.Error("finishing encryption change", "err", err)
			return
		}
		log.Info("encryption change complete", "values", total)
	}()
	return wg.Wait
}

// resetMessages is `wa-tui db reset`: after an explicit confirmation it
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
	_, err := fmt.Fprintln(out, "Deleted. Run `wa-tui login -relink` to fetch history again. A keychain entry named go-whassup, if any, can be removed in Keychain Access.")
	return err
}
