package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/wasichu/go-whassup/internal/config"
	"github.com/wasichu/go-whassup/internal/logging"
	"github.com/wasichu/go-whassup/internal/media"
	"github.com/wasichu/go-whassup/internal/paths"
	"github.com/wasichu/go-whassup/internal/store"
)

// errReplaced ends a background re-seal that a newer change superseded (or
// shutdown stopped); it resumes from where it got to.
var errReplaced = errors.New("re-sealing stopped before it finished")

// encryptor owns at-rest encryption while the app runs: the unlocked key,
// the background re-seal, and what must change with it (downloads go to a
// temporary directory and whatsmeow's debug log is capped while encrypted).
// The same steps serve startup (config changed while closed, or a re-seal
// to resume) and live changes from Settings, so both land on the same
// on-disk states. See docs/storage.md ("Encryption decisions").
type encryptor struct {
	base   context.Context // the app's; re-seals stop when it ends
	st     *store.Store
	p      paths.Paths
	ks     keySources
	log    *slog.Logger
	logCap *logging.Cap
	cache  *media.Switchable

	beforeBatch func(context.Context) // tests: hold a re-seal mid-way

	mu     sync.Mutex
	codec  *store.KeyCodec // the unlocked data key, if any
	tmp    string          // temporary media directory, once created
	stop   func()          // cancels the running re-seal and waits for it
	done   chan error      // the running re-seal's outcome
	closed bool
}

func newEncryptor(base context.Context, st *store.Store, p paths.Paths, ks keySources, log *slog.Logger) *encryptor {
	return &encryptor{base: base, st: st, p: p, ks: ks, log: log, logCap: &logging.Cap{}, cache: media.NewSwitchable(p.MediaDir)}
}

// Start unlocks the stored key, starts any change the config asks for,
// and resumes an unfinished one in the background. transitions=false (db
// commands) only unlocks.
func (e *encryptor) Start(ctx context.Context, cfg config.Config, transitions bool) (encryption, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	enc, err := e.setUp(ctx, cfg, transitions)
	if err == nil && enc.finish != nil {
		e.run(enc.finish, cfg.Encryption.Enabled, enc.pending)
	}
	return enc, err
}

// setUp is Start without starting the re-seal.
func (e *encryptor) setUp(ctx context.Context, cfg config.Config, transitions bool) (encryption, error) {
	key, err := e.st.Key(ctx)
	if err != nil {
		return encryption{}, err
	}
	want := cfg.Encryption.Enabled

	// 1. A key exists: unlock it from the source it was created with.
	if key != nil {
		if e.codec, err = unlock(ctx, key, e.ks); err != nil {
			return encryption{}, err
		}
		e.st.Unlock(e.codec, want || !transitions)
		if transitions && !want {
			e.st.WritePlaintext() // turning it off: decrypt in the background
		}
	}
	// 2. Turning it on.
	if key == nil && want && transitions {
		if err := e.enable(ctx, cfg.Encryption.KeySource, e.ks); err != nil {
			return encryption{}, err
		}
	}

	// "On" as long as any key exists: while decrypting, sealed values remain.
	enc := encryption{on: want || key != nil}
	if enc.on {
		if err := e.protect(); err != nil {
			return encryption{}, err
		}
	}
	if !transitions {
		return enc, nil
	}
	if enc.pending, err = e.st.Pending(ctx); err != nil {
		return encryption{}, err
	}
	enc.finish = finishStep(e.st, want, key, enc.pending)
	return enc, nil
}

// enable turns encryption on: no plaintext media may remain next to
// encrypted messages, then the key is created and stored (AddKey also
// empties the search index), then writes switch to it.
func (e *encryptor) enable(ctx context.Context, source string, ks keySources) error {
	if err := os.RemoveAll(e.p.MediaDir); err != nil {
		return fmt.Errorf("removing unencrypted media cache: %w", err)
	}
	codec, err := createKey(ctx, source, e.st, ks)
	if err != nil {
		return err
	}
	e.codec = codec
	e.st.Unlock(codec, true)
	e.log.Info("encryption enabled", "key", codec.ID())
	return nil
}

// Set turns encryption on or off now, from Settings. pass is the new
// passphrase when turning on with key source "prompt" (it's zeroed after
// use); turning off never needs one, since the key is already unlocked. The
// re-seal runs in the background; done reports how it ended. A change
// while one is running replaces it (that one's done gets errReplaced).
func (e *encryptor) Set(ctx context.Context, on bool, source string, pass []byte) (done <-chan error, err error) {
	defer clear(pass)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("shutting down")
	}
	e.stopRunning()
	key, err := e.st.Key(ctx)
	if err != nil {
		return nil, err
	}
	if !on {
		if key == nil {
			return finished(), nil // nothing is encrypted
		}
		e.st.WritePlaintext()
		return e.startPending(ctx, false, key)
	}
	switch {
	case key == nil:
		ks := e.ks
		if source == config.KeySourcePrompt {
			if len(pass) == 0 {
				return nil, errors.New("a passphrase is needed")
			}
			ks.passphrase = func(context.Context, string, bool) ([]byte, error) { return append([]byte(nil), pass...), nil }
		}
		if err := e.enable(ctx, source, ks); err != nil {
			return nil, err
		}
	case e.codec != nil: // turned off and on again before decrypting finished
		e.st.Unlock(e.codec, true)
	default:
		return nil, errors.New("the stored key isn't unlocked")
	}
	if err := e.protect(); err != nil {
		return nil, err
	}
	return e.startPending(ctx, true, key)
}

// NeedsPassphrase reports whether turning encryption on with source needs
// a new passphrase from the user (only "prompt", and only when no key
// exists yet: re-enabling during a decrypt reuses the unlocked key).
func (e *encryptor) NeedsPassphrase(ctx context.Context, on bool, source string) (bool, error) {
	if !on || source != config.KeySourcePrompt {
		return false, nil
	}
	key, err := e.st.Key(ctx)
	return key == nil, err
}

// Running is the outcome of the re-seal started at launch, if any.
func (e *encryptor) Running() <-chan error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.done
}

// startPending starts the re-seal toward on, if anything is left to do.
func (e *encryptor) startPending(ctx context.Context, on bool, key *store.StoredKey) (<-chan error, error) {
	pending, err := e.st.Pending(ctx)
	if err != nil {
		return nil, err
	}
	finish := finishStep(e.st, on, key, pending)
	if finish == nil {
		return finished(), nil
	}
	return e.run(finish, on, pending), nil
}

// run starts the background re-seal; e.mu is held.
func (e *encryptor) run(finish func(context.Context) error, on bool, pending int) chan error {
	ctx, cancel := context.WithCancel(e.base)
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := e.reseal(ctx, finish, pending)
		if err == nil && !on {
			e.unprotect() // decrypted: back to the normal cache and log
		}
		done <- err
		close(done)
	}()
	e.stop = func() { cancel(); wg.Wait() }
	e.done = done
	return done
}

// reseal re-seals in batches until done (then runs finish) or ctx ends. It
// stops on the first error rather than retrying: a value that can't be
// opened would fail forever.
func (e *encryptor) reseal(ctx context.Context, finish func(context.Context) error, pending int) error {
	total := 0
	for ctx.Err() == nil {
		if e.beforeBatch != nil {
			e.beforeBatch(ctx)
		}
		n, err := e.st.ResealBatch(ctx, resealBatch)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			e.log.Error("re-sealing stopped; it resumes next start", "done", total, "err", err)
			return err
		}
		if n == 0 {
			break
		}
		total += n
		e.log.Info("re-sealing", "done", total, "of", pending)
	}
	if ctx.Err() != nil {
		e.log.Info("re-sealing paused; it resumes when started again", "done", total)
		return errReplaced
	}
	if err := finish(ctx); err != nil {
		e.log.Error("finishing encryption change", "err", err)
		return err
	}
	e.log.Info("encryption change complete", "values", total)
	return nil
}

// stopRunning stops a running re-seal; e.mu is held.
func (e *encryptor) stopRunning() {
	if e.stop != nil {
		e.stop()
		e.stop = nil
	}
}

// protect keeps plaintext out of the media cache and the log while
// encrypted: downloads go to a temporary directory (removed on Close) and
// whatsmeow's debug output is dropped. e.mu is held.
func (e *encryptor) protect() error {
	if e.tmp == "" {
		tmp, err := os.MkdirTemp("", "go-whassup-media-")
		if err != nil {
			return fmt.Errorf("creating temporary media dir: %w", err)
		}
		e.tmp = tmp
	}
	e.cache.SetDir(e.tmp)
	e.logCap.Set(slog.LevelInfo)
	return nil
}

// unprotect undoes protect (atomically; no lock needed).
func (e *encryptor) unprotect() {
	e.cache.SetDir(e.p.MediaDir)
	e.logCap.Clear()
}

// Close stops any re-seal (it resumes next start) and removes the
// temporary media directory. Call it before closing the store.
func (e *encryptor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.stopRunning()
	if e.tmp == "" {
		return nil
	}
	return os.RemoveAll(e.tmp)
}

// finished is a done channel for a change with nothing left to do.
func finished() <-chan error {
	done := make(chan error, 1)
	done <- nil
	close(done)
	return done
}
