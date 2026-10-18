package store

import (
	"fmt"

	"github.com/wasichu/go-whassup/internal/crypt"
)

// Codec seals message data at rest. Each sealed value records the ID of the
// codec (= the key, see the keys table) that sealed it in its row, so
// plaintext and encrypted rows coexist and turning encryption on or off is
// a resumable re-seal, not a migration. See docs/storage.md.
type Codec interface {
	// ID is the key ID stored per row. 0 is reserved for Plaintext.
	ID() int
	// Seal and Open bind the value to aad (its row and column), so a sealed
	// value can't be moved to another row or column.
	Seal(plain, aad []byte) ([]byte, error)
	Open(sealed, aad []byte) ([]byte, error)
}

// Plaintext stores data unchanged. It is the default codec.
type Plaintext struct{}

func (Plaintext) ID() int                              { return 0 }
func (Plaintext) Seal(plain, _ []byte) ([]byte, error) { return plain, nil }
func (Plaintext) Open(sealed, _ []byte) ([]byte, error) {
	return sealed, nil
}

// KeyCodec encrypts with AES-256-GCM under a data key from the keys table.
type KeyCodec struct {
	id   int
	aead *crypt.AEAD
}

// NewKeyCodec returns the codec for key id with data key dek.
func NewKeyCodec(id int, dek []byte) (*KeyCodec, error) {
	if id <= 0 {
		return nil, fmt.Errorf("key id must be positive, got %d", id)
	}
	a, err := crypt.NewAEAD(dek)
	if err != nil {
		return nil, err
	}
	return &KeyCodec{id: id, aead: a}, nil
}

func (k *KeyCodec) ID() int                                 { return k.id }
func (k *KeyCodec) Seal(plain, aad []byte) ([]byte, error)  { return k.aead.Seal(plain, aad) }
func (k *KeyCodec) Open(sealed, aad []byte) ([]byte, error) { return k.aead.Open(sealed, aad) }

// Columns whose values are sealed, for associated data.
const (
	colBody  = "body"
	colMedia = "media"
)

// reactionColumn is the associated-data column for a sender's reaction, so
// one person's reaction can't be passed off as another's.
func reactionColumn(sender string) string { return "reaction:" + sender }

// rowAAD binds a sealed value to its message and column.
func rowAAD(chat, id, column string) []byte {
	return []byte(chat + "\x00" + id + "\x00" + column)
}

// seal is the single write site for sensitive message data (text, media
// keys): everything stored at rest goes through the current codec.
func (s *Store) seal(plain []byte, chat, id, column string) (sealed []byte, codecID int, err error) {
	c := s.current()
	sealed, err = c.Seal(plain, rowAAD(chat, id, column))
	if err != nil {
		return nil, 0, fmt.Errorf("sealing message data: %w", err)
	}
	return sealed, c.ID(), nil
}

// open is the single read site for sealed message data.
func (s *Store) open(sealed []byte, codecID int, chat, id, column string) ([]byte, error) {
	s.mu.RLock()
	c, ok := s.codecs[codecID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("message sealed with unknown codec %d (is the key unlocked?)", codecID)
	}
	plain, err := c.Open(sealed, rowAAD(chat, id, column))
	if err != nil {
		return nil, fmt.Errorf("opening message data (codec %d): %w", codecID, err)
	}
	return plain, nil
}
