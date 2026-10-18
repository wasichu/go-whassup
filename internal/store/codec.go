package store

import "fmt"

// Codec transforms message text at rest. Every stored body records the ID of
// the codec that sealed it, so plaintext and encrypted rows can coexist and
// enabling encryption needs no migration. See docs/storage.md.
type Codec interface {
	// ID is stored per row. 0 is reserved for Plaintext.
	ID() int
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// Plaintext stores text unchanged. It is the default codec.
type Plaintext struct{}

func (Plaintext) ID() int                           { return 0 }
func (Plaintext) Seal(plain []byte) ([]byte, error) { return plain, nil }
func (Plaintext) Open(sealed []byte) ([]byte, error) {
	return sealed, nil
}

// seal is the single write site for sensitive message data (text, media
// keys): everything stored at rest goes through the current codec.
func (s *Store) seal(plain []byte) (sealed []byte, codecID int, err error) {
	sealed, err = s.codec.Seal(plain)
	if err != nil {
		return nil, 0, fmt.Errorf("sealing message data: %w", err)
	}
	return sealed, s.codec.ID(), nil
}

// open is the single read site for sealed message data.
func (s *Store) open(sealed []byte, codecID int) ([]byte, error) {
	c, ok := s.codecs[codecID]
	if !ok {
		return nil, fmt.Errorf("message sealed with unknown codec %d", codecID)
	}
	plain, err := c.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("opening message data (codec %d): %w", codecID, err)
	}
	return plain, nil
}
