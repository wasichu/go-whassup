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

// TestFixedVector pins our wire format (nonce || ciphertext || tag): a blob
// sealed by today's code must open with tomorrow's. It was generated with
// the same crypto/cipher calls, so it doesn't check AES-GCM itself; see
// TestKnownAnswer for that.
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

// fixedCiphertext was produced with crypto/cipher (key 0x07*32,
// nonce 000102..0b, aad "v", plaintext "pinned").
const fixedCiphertext = "68e8871e786de1a7ca90a014de9ea842d1f7f6752799"

// TestKnownAnswer checks AES-256-GCM with associated data against a known
// answer from Go's crypto/cipher test suite (aesGCMTests).
func TestKnownAnswer(t *testing.T) {
	h := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	k := h("feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308")
	nonce := h("e1934f5db57cc983e6b180e7")
	plain := h("73ed042327f70fe9c572a61545eda8b2a0c6e1d6c291ef19248e973aee6c312012f490c2c6f6166f4a59431e182663fcaea05a")
	ad := h("0a8a18a7150e940c3d87b38e73baee9a5c049ee21795663e264b694a949822b639092d0e67015e86363583fcf0ca645af9f43375f05fdb4ce84f411dcbca73c2220dea03a20115d2e51398344b16bee1ed7c499b353d6c597af8")
	result := h("fc1ae2b5dcd2c4176c3f538b4c3cc21197f79e608cc3730167936382e4b1e5a7b75ae1678bcebd876705477eb0e0fdbbcda92fb9a0dc58c8d8f84fb590e0422e6077ef")
	a, err := NewAEAD(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Open(append(nonce, result...), ad)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("known answer failed: %x, %v", got, err)
	}
}
