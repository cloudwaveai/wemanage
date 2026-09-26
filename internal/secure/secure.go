// Package secure provides cryptographic primitives: API key generation/hashing,
// random tokens, and authenticated encryption for secrets at rest.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// APIKeyPrefix is the fixed prefix of every issued employee key.
const APIKeyPrefix = "sk-emp-"

// keyPrefixLen is how many leading characters of the plaintext key are stored
// (for display/identification only).
const keyPrefixLen = 12

// NewAPIKey generates a new employee API key. It returns the plaintext (shown to
// the user exactly once), its SHA-256 hash (stored), and a short display prefix.
func NewAPIKey() (plain string, hash []byte, prefix string) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		panic("secure: entropy failure: " + err.Error())
	}
	plain = APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(plain))
	prefix = plain
	if len(prefix) > keyPrefixLen {
		prefix = prefix[:keyPrefixLen]
	}
	return plain, h[:], prefix
}

// HashAPIKey returns the SHA-256 hash of a plaintext key for lookup.
func HashAPIKey(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// RandomToken returns a base64url (unpadded) string of nBytes of entropy.
func RandomToken(nBytes int) string {
	if nBytes <= 0 {
		nBytes = 32
	}
	b := make([]byte, nBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("secure: entropy failure: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Box performs AES-256-GCM authenticated encryption with a fixed key.
type Box struct {
	aead cipher.AEAD
}

// NewBox creates a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secure: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext, returning nonce||ciphertext. Each call uses a fresh
// random nonce, so sealing the same plaintext twice yields different output.
func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic("secure: entropy failure: " + err.Error())
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

// ErrDecrypt is returned when sealed data cannot be authenticated/decrypted.
var ErrDecrypt = errors.New("secure: decryption failed")

// Open reverses Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns {
		return nil, ErrDecrypt
	}
	nonce, ct := sealed[:ns], sealed[ns:]
	plain, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
