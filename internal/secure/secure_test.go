package secure

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	plain, hash, prefix := NewAPIKey()
	if !strings.HasPrefix(plain, APIKeyPrefix) {
		t.Fatalf("plain %q missing prefix", plain)
	}
	if len(plain) != 50 { // "sk-emp-" (7) + 43 base64url chars for 32 bytes
		t.Fatalf("plain length = %d, want 50 (%q)", len(plain), plain)
	}
	if !bytes.Equal(hash, HashAPIKey(plain)) {
		t.Fatal("hash mismatch with HashAPIKey")
	}
	if prefix != plain[:12] {
		t.Fatalf("prefix = %q", prefix)
	}
	// Uniqueness across calls.
	p2, _, _ := NewAPIKey()
	if p2 == plain {
		t.Fatal("two keys collided")
	}
}

func TestBoxRoundTrip(t *testing.T) {
	box, err := NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("socks5://user:pass@1.2.3.4:1080")
	sealed := box.Seal(plain)
	if bytes.Contains(sealed, []byte("1.2.3.4")) {
		t.Fatal("sealed data leaks plaintext")
	}
	got, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestBoxNonceRandomized(t *testing.T) {
	box, _ := NewBox(make([]byte, 32))
	a := box.Seal([]byte("same"))
	b := box.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("sealing same plaintext twice produced identical ciphertext")
	}
}

func TestBoxTamperDetected(t *testing.T) {
	box, _ := NewBox(make([]byte, 32))
	sealed := box.Seal([]byte("secret"))
	sealed[len(sealed)-1] ^= 0xff
	if _, err := box.Open(sealed); err == nil {
		t.Fatal("expected decryption failure on tampered data")
	}
}

func TestBoxShortInput(t *testing.T) {
	box, _ := NewBox(make([]byte, 32))
	if _, err := box.Open([]byte{1, 2}); err == nil {
		t.Fatal("expected error on too-short input")
	}
}

func TestNewBoxBadKey(t *testing.T) {
	if _, err := NewBox(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestRandomToken(t *testing.T) {
	a := RandomToken(32)
	b := RandomToken(32)
	if a == b {
		t.Fatal("tokens collided")
	}
	if strings.ContainsAny(a, "+/=") {
		t.Fatalf("token not url-safe unpadded: %q", a)
	}
}
