// Package crypt holds the at-rest encryption primitives: AES-256-GCM sealing
// bound to associated data, envelope wrapping of the data key, and Argon2id
// for passphrases. See docs/storage.md ("Encryption decisions").
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// KeySize is the size of data keys and key-encryption keys.
const KeySize = 32

// ErrDecrypt means a blob failed authentication: wrong key, corrupted
// data, or a blob moved from another row (its associated data differs).
var ErrDecrypt = errors.New("decryption failed (wrong key or tampered data)")

// AEAD seals blobs with AES-256-GCM. Each blob is a random 12-byte nonce
// followed by the ciphertext and tag.
type AEAD struct {
	aead cipher.AEAD
}

// NewAEAD returns an AEAD for a 32-byte key.
func NewAEAD(key []byte) (*AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &AEAD{aead: gcm}, nil
}

// Seal encrypts plain, authenticating aad (which must be supplied again to
// Open, so a blob can't be moved to a context with different aad).
func (a *AEAD) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, a.aead.NonceSize(), a.aead.NonceSize()+len(plain)+a.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return a.aead.Seal(nonce, nonce, plain, aad), nil
}

// Open decrypts a blob from Seal with the same aad.
func (a *AEAD) Open(sealed, aad []byte) ([]byte, error) {
	n := a.aead.NonceSize()
	if len(sealed) < n+a.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := a.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// NewKey returns a random 32-byte key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	return k, nil
}

// wrapAAD binds wrapped keys to their purpose and format version.
var wrapAAD = []byte("go-whassup data key v1")

// Wrap encrypts the data key dek under the key-encryption key kek.
func Wrap(kek, dek []byte) ([]byte, error) {
	a, err := NewAEAD(kek)
	if err != nil {
		return nil, err
	}
	return a.Seal(dek, wrapAAD)
}

// Unwrap recovers a data key; ErrDecrypt usually means a wrong passphrase
// or a different keychain secret.
func Unwrap(kek, wrapped []byte) ([]byte, error) {
	a, err := NewAEAD(kek)
	if err != nil {
		return nil, err
	}
	dek, err := a.Open(wrapped, wrapAAD)
	if err != nil {
		return nil, err
	}
	if len(dek) != KeySize {
		return nil, ErrDecrypt
	}
	return dek, nil
}

// KDFParams are Argon2id parameters; they're stored with each wrapped key
// so they can be raised later without breaking existing keys.
type KDFParams struct {
	Time      uint32 // passes
	MemoryKiB uint32
	Threads   uint8
}

// DefaultKDF follows the Argon2 RFC's second recommendation (64 MiB).
var DefaultKDF = KDFParams{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// NewSalt returns a random 16-byte salt.
func NewSalt() ([]byte, error) {
	s := make([]byte, 16)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return s, nil
}

// DeriveKEK turns a passphrase into a key-encryption key.
func DeriveKEK(passphrase, salt []byte, p KDFParams) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("empty passphrase")
	}
	if len(salt) < 16 || p.Time == 0 || p.MemoryKiB < 8*1024 || p.Threads == 0 {
		return nil, fmt.Errorf("unsafe key derivation parameters %+v", p)
	}
	return argon2.IDKey(passphrase, salt, p.Time, p.MemoryKiB, p.Threads, KeySize), nil
}
