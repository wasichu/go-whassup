package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wasichu/go-whassup/internal/crypt"
)

// enable runs the enable sequence like the app does, returning the codec.
func enable(t *testing.T, st *Store, batch int) *KeyCodec {
	t.Helper()
	ctx := t.Context()
	dek, err := crypt.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	kek := bytes.Repeat([]byte{5}, crypt.KeySize)
	wrapped, err := crypt.Wrap(kek, dek)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.AddKey(ctx, StoredKey{Alg: "aes-256-gcm", Source: "keyring", Wrapped: wrapped})
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	c, err := NewKeyCodec(id, dek)
	if err != nil {
		t.Fatal(err)
	}
	st.Unlock(c, true)
	for {
		n, err := st.ResealBatch(ctx, batch)
		if err != nil {
			t.Fatalf("ResealBatch: %v", err)
		}
		if n == 0 {
			break
		}
	}
	return c
}

func TestEncryptAllRowsAndDecryptBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	st := openStore(t, path, nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "1", t0, "lunch plans"), mediaMsg(ada, "2", t0.Add(time.Minute)), msg(bob, "3", t0, "tea"))
	if got := searchIDs(t, st, "lunch"); len(got) != 1 {
		t.Fatalf("setup search = %v", got)
	}

	c := enable(t, st, 2) // small batches: exercises several passes
	if _, err := st.Search(ctx, "lunch", 10); !errors.Is(err, ErrSearchUnavailable) {
		t.Errorf("search while keyed = %v", err)
	}
	if err := st.FinishEncrypting(ctx); err != nil {
		t.Fatalf("FinishEncrypting: %v", err)
	}
	var plain, fts int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE codec = 0 OR (media IS NOT NULL AND media_codec = 0)`).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("plaintext values left: %d (%v)", plain, err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM messages_fts`).Scan(&fts); err != nil || fts != 0 {
		t.Errorf("search index must be emptied: %d rows (%v)", fts, err)
	}
	var raw []byte
	if err := st.db.QueryRowContext(ctx, `SELECT body FROM messages WHERE id = '1'`).Scan(&raw); err != nil || bytes.Contains(raw, []byte("lunch")) {
		t.Errorf("body still readable on disk: %q (%v)", raw, err)
	}
	// Reads and new writes go through the key.
	mustSave(t, st, msg(ada, "4", t0.Add(time.Hour), "after"))
	if m := onlyMessage(t, st, ada, "1"); m.Text != "lunch plans" {
		t.Errorf("read back = %q", m.Text)
	}
	if m := onlyMessage(t, st, ada, "2"); m.Media == nil {
		t.Error("media key lost in re-seal")
	}

	// A fresh Store without the key can't read sealed rows (refuse, don't garble).
	locked := openStore(t, path, nil)
	if _, err := locked.Messages(ctx, ada, 0); err == nil || !strings.Contains(err.Error(), "unknown codec") {
		t.Errorf("locked read = %v", err)
	}
	k, err := locked.Key(ctx)
	if err != nil || k == nil || k.ID != c.ID() || k.KDF != nil {
		t.Fatalf("Key = %+v, %v", k, err)
	}

	// Turning it off: decrypt back, index again, forget the key.
	st.WritePlaintext()
	for {
		n, err := st.ResealBatch(ctx, 100)
		if err != nil {
			t.Fatalf("decrypting: %v", err)
		}
		if n == 0 {
			break
		}
	}
	if err := st.FinishDecrypting(ctx); err != nil {
		t.Fatalf("FinishDecrypting: %v", err)
	}
	if got := searchIDs(t, st, "lunch"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("search after decrypting = %v", got)
	}
	if k, err := st.Key(ctx); err != nil || k != nil {
		t.Errorf("key should be gone: %+v, %v", k, err)
	}
}

func TestSealedValueIsBoundToItsRow(t *testing.T) {
	st := openStore(t, "", nil)
	ctx := t.Context()
	mustSave(t, st, msg(ada, "1", t0, "first"), msg(ada, "2", t0.Add(time.Minute), "second"))
	enable(t, st, 10)
	// Move message 2's sealed body into message 1's row: must not decrypt.
	if _, err := st.db.ExecContext(ctx, `UPDATE messages SET body = (SELECT body FROM messages WHERE id = '2') WHERE id = '1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Messages(ctx, ada, 0); !errors.Is(err, crypt.ErrDecrypt) {
		t.Errorf("swapped body = %v, want ErrDecrypt", err)
	}
}

func TestResealResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	st := openStore(t, path, nil)
	ctx := t.Context()
	for i := range 5 {
		mustSave(t, st, msg(ada, string(rune('a'+i)), t0.Add(time.Duration(i)*time.Minute), "x"))
	}
	dek, _ := crypt.NewKey()                                   //nolint:errcheck // rand failure is fatal elsewhere
	wrapped, _ := crypt.Wrap(bytes.Repeat([]byte{5}, 32), dek) //nolint:errcheck // fixed-size keys
	id, err := st.AddKey(ctx, StoredKey{Alg: "aes-256-gcm", Source: "keyring", Wrapped: wrapped})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewKeyCodec(id, dek) //nolint:errcheck // valid inputs
	st.Unlock(c, true)
	if n, err := st.ResealBatch(ctx, 2); err != nil || n != 2 {
		t.Fatalf("first batch = %d, %v", n, err)
	}
	if err := st.FinishEncrypting(ctx); err == nil {
		t.Error("finishing with values left must fail (VACUUM only at the end)")
	}

	// "Crash": reopen, unlock, resume.
	again := openStore(t, path, nil)
	again.Unlock(c, true)
	if n, err := again.Pending(ctx); err != nil || n != 3 {
		t.Fatalf("pending after restart = %d, %v", n, err)
	}
	if _, err := again.Search(ctx, "x", 10); !errors.Is(err, ErrSearchUnavailable) {
		t.Errorf("search during a resumed enable = %v", err)
	}
	for {
		n, err := again.ResealBatch(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if err := again.FinishEncrypting(ctx); err != nil {
		t.Errorf("FinishEncrypting after resume: %v", err)
	}
}

func TestKeyStoresKDFParams(t *testing.T) {
	st := openStore(t, "", nil)
	p := crypt.DefaultKDF
	if _, err := st.AddKey(t.Context(), StoredKey{Alg: "aes-256-gcm", Source: "prompt", Salt: []byte("0123456789abcdef"), KDF: &p, Wrapped: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	k, err := st.Key(t.Context())
	if err != nil || k.KDF == nil || *k.KDF != crypt.DefaultKDF || string(k.Salt) != "0123456789abcdef" {
		t.Errorf("key = %+v, %v", k, err)
	}
}
