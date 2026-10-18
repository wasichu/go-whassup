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

// sealBody is the single write site for message text.
func (s *Store) sealBody(text string) (body []byte, codecID int, err error) {
	body, err = s.codec.Seal([]byte(text))
	if err != nil {
		return nil, 0, fmt.Errorf("sealing message body: %w", err)
	}
	return body, s.codec.ID(), nil
}

// openBody is the single read site for message text.
func (s *Store) openBody(body []byte, codecID int) (string, error) {
	c, ok := s.codecs[codecID]
	if !ok {
		return "", fmt.Errorf("message sealed with unknown codec %d", codecID)
	}
	plain, err := c.Open(body)
	if err != nil {
		return "", fmt.Errorf("opening message body (codec %d): %w", codecID, err)
	}
	return string(plain), nil
}
