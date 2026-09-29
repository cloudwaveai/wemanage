package web

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"testing"
)

// encryptLikeFeishu is Feishu's side of an encrypted callback.
func encryptLikeFeishu(plain []byte, key string) string {
	k := sha256.Sum256([]byte(key))
	block, _ := aes.NewCipher(k[:])
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{7}, aes.BlockSize)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(append(iv, out...))
}

func TestFeishuDecryptAndSignature(t *testing.T) {
	msg := []byte(`{"type":"url_verification","challenge":"c"}`)
	got, err := feishuDecrypt(encryptLikeFeishu(msg, "ek"), "ek")
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("decrypt: %q %v", got, err)
	}
	if got, err := feishuDecrypt(encryptLikeFeishu(msg, "ek"), "other"); err == nil && bytes.Equal(got, msg) {
		t.Fatal("wrong key decrypted")
	}
	body := []byte(`{"encrypt":"x"}`)
	sum := sha256.Sum256([]byte("1700000000" + "n1" + "ek" + string(body)))
	h := http.Header{"X-Lark-Request-Timestamp": {"1700000000"}, "X-Lark-Request-Nonce": {"n1"}, "X-Lark-Signature": {hex.EncodeToString(sum[:])}}
	if !feishuSignatureOK(h, body, "ek") {
		t.Fatal("good signature refused")
	}
	h.Set("X-Lark-Signature", "00")
	if feishuSignatureOK(h, body, "ek") {
		t.Fatal("bad signature accepted")
	}
}
