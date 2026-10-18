package crypt

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func TestSealOpenRoundTripAndBinding(t *testing.T) {
	a, err := NewAEAD(key(1))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal([]byte("hello"), []byte("chat/1/body"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hello")) {
		t.Fatal("plaintext visible in sealed blob")
	}
	plain, err := a.Open(sealed, []byte("chat/1/body"))
	if err != nil || string(plain) != "hello" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
	// Bound to its row and column: moving it elsewhere fails.
	for _, aad := range []string{"chat/2/body", "chat/1/media", ""} {
		if _, err := a.Open(sealed, []byte(aad)); !errors.Is(err, ErrDecrypt) {
			t.Errorf("Open with aad %q = %v, want ErrDecrypt", aad, err)
		}
	}
	// Tampering and truncation fail.
	bad := bytes.Clone(sealed)
	bad[len(bad)-1] ^= 1
	if _, err := a.Open(bad, []byte("chat/1/body")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered = %v", err)
	}
	if _, err := a.Open(sealed[:5], nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("truncated = %v", err)
	}
	// Wrong key fails.
	other, _ := NewAEAD(key(2)) //nolint:errcheck // fixed-size key can't fail
	if _, err := other.Open(sealed, []byte("chat/1/body")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong key = %v", err)
	}
	// Nonces are random: sealing twice differs.
	again, _ := a.Seal([]byte("hello"), []byte("chat/1/body")) //nolint:errcheck // checked above
	if bytes.Equal(again, sealed) {
		t.Error("two seals of the same text are identical")
	}
	if _, err := NewAEAD([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
}

// TestFixedVector pins the wire format: a blob sealed by today's code must
// open with tomorrow's.
func TestFixedVector(t *testing.T) {
	a, err := NewAEAD(key(7))
	if err != nil {
		t.Fatal(err)
	}
	// nonce(12) || ciphertext || tag, sealed once with key(7), aad "v".
	sealed, _ := hex.DecodeString("000102030405060708090a0b" + fixedCiphertext) //nolint:errcheck // constant
	plain, err := a.Open(sealed, []byte("v"))
	if err != nil || string(plain) != "pinned" {
		t.Fatalf("fixed vector = %q, %v", plain, err)
	}
}

func TestWrapUnwrap(t *testing.T) {
	dek, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := Wrap(key(3), dek)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unwrap(key(3), wrapped)
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("Unwrap = %x, %v", got, err)
	}
	if _, err := Unwrap(key(4), wrapped); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong KEK = %v, want ErrDecrypt", err)
	}
}

func TestDeriveKEK(t *testing.T) {
	salt := bytes.Repeat([]byte{9}, 16)
	start := time.Now()
	k1, err := DeriveKEK([]byte("correct horse"), salt, DefaultKDF)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("default KDF took %v; too slow for startup", d)
	}
	k2, _ := DeriveKEK([]byte("correct horse"), salt, DefaultKDF)  //nolint:errcheck // same inputs as above
	k3, _ := DeriveKEK([]byte("correct horse!"), salt, DefaultKDF) //nolint:errcheck // same inputs as above
	if !bytes.Equal(k1, k2) || bytes.Equal(k1, k3) || len(k1) != KeySize {
		t.Error("KDF must be deterministic per passphrase and differ between passphrases")
	}
	if _, err := DeriveKEK(nil, salt, DefaultKDF); err == nil {
		t.Error("empty passphrase accepted")
	}
	if _, err := DeriveKEK([]byte("x"), salt, KDFParams{Time: 1, MemoryKiB: 1, Threads: 1}); err == nil {
		t.Error("unsafe parameters accepted")
	}
}

// fixedCiphertext was produced independently with crypto/cipher (key 0x07*32,
// nonce 000102..0b, aad "v", plaintext "pinned").
const fixedCiphertext = "68e8871e786de1a7ca90a014de9ea842d1f7f6752799"
