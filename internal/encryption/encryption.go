// Package encryption is at-rest encryption of messages.db while the app
// runs: key sources, unlocking and creating keys, and the Controller that
// re-seals stored values in the background, at startup or live from
// Settings. See docs/storage.md ("Encryption decisions").
package encryption

import (
	"context"
	"errors"
	"fmt"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/crypt"
	"github.com/wasichu/go-whassup/internal/keysource"
	"github.com/wasichu/go-whassup/internal/store"
)

const alg = "aes-256-gcm"

// Sources is how secrets are obtained: the OS keyring, and a passphrase
// (from password_command or a prompt). Tests replace both.
type Sources struct {
	Keyring interface {
		Get(ctx context.Context) ([]byte, error)
		Put(ctx context.Context, key []byte) error
	}
	Passphrase func(ctx context.Context, src string, confirm bool) ([]byte, error)
}

// ErrLocked means stored messages couldn't be unlocked; the message says
// how to recover.
var ErrLocked = errors.New("can't unlock stored messages")

// State is the outcome of Controller.Start.
type State struct {
	On      bool // messages are (being) encrypted: no message text in logs, temp media
	Pending int  // values still to re-seal in the background
	finish  func(context.Context) error
}

// Resealing reports whether a re-seal was started (or resumed).
func (s State) Resealing() bool { return s.finish != nil }

// finishStep is what to run once the background re-seal is done, if any.
func finishStep(st *store.Store, want bool, key *store.StoredKey, pending int) func(context.Context) error {
	switch {
	case want && (key == nil || pending > 0 || !key.Finished):
		// Also when every value is sealed but the final compaction never
		// completed (a crash right after the last batch).
		return st.FinishEncrypting
	case !want && key != nil:
		return st.FinishDecrypting
	}
	return nil
}

func unlock(ctx context.Context, key *store.StoredKey, ks Sources) (*store.KeyCodec, error) {
	var kek []byte
	var err error
	switch key.Source {
	case config.KeySourceKeyring:
		kek, err = ks.Keyring.Get(ctx)
		if errors.Is(err, keysource.ErrNotFound) {
			return nil, fmt.Errorf("%w: the keychain entry is missing. Restore it, or run `go-whassup db reset` to delete stored messages (history older than what your phone syncs is lost)", ErrLocked)
		}
	default:
		var pass []byte
		if pass, err = ks.Passphrase(ctx, key.Source, false); err == nil {
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
		return nil, fmt.Errorf("%w: wrong passphrase or keychain secret (key source %s). Fix the key source, or run `go-whassup db reset` to delete stored messages (history older than what your phone syncs is lost)", ErrLocked, key.Source)
	}
	if err != nil {
		return nil, err
	}
	return store.NewKeyCodec(key.ID, dek)
}

func createKey(ctx context.Context, source string, st *store.Store, ks Sources) (*store.KeyCodec, error) {
	dek, err := crypt.NewKey()
	if err != nil {
		return nil, err
	}
	stored := store.StoredKey{Alg: alg, Source: source}
	var kek []byte
	if source == config.KeySourceKeyring {
		kek, err = newKeyringKEK(ctx, ks)
	} else {
		kek, err = newPassphraseKEK(ctx, source, ks, &stored)
	}
	if err != nil {
		return nil, err
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

// newKeyringKEK makes a random key-encryption key and stores it in the
// keyring (no derivation: it's already random).
func newKeyringKEK(ctx context.Context, ks Sources) ([]byte, error) {
	kek, err := crypt.NewKey()
	if err != nil {
		return nil, err
	}
	return kek, ks.Keyring.Put(ctx, kek)
}

// newPassphraseKEK asks for a new passphrase (twice) and derives the
// key-encryption key, recording the salt and parameters on stored.
func newPassphraseKEK(ctx context.Context, source string, ks Sources, stored *store.StoredKey) ([]byte, error) {
	pass, err := ks.Passphrase(ctx, source, true)
	if err != nil {
		return nil, err
	}
	if stored.Salt, err = crypt.NewSalt(); err != nil {
		return nil, err
	}
	params := crypt.DefaultKDF
	stored.KDF = &params
	return crypt.DeriveKEK(pass, stored.Salt, params)
}

// resealBatch is how many messages each background transaction re-seals.
const resealBatch = 500
