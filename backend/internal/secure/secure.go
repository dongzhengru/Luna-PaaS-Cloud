package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	return &Box{aead: a}, e
}
func (b *Box) Seal(s string) (string, error) {
	n := make([]byte, b.aead.NonceSize())
	if _, e := io.ReadFull(rand.Reader, n); e != nil {
		return "", e
	}
	out := b.aead.Seal(n, n, []byte(s), nil)
	return base64.RawURLEncoding.EncodeToString(out), nil
}
func (b *Box) Open(s string) (string, error) {
	raw, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("invalid ciphertext")
	}
	p, e := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	return string(p), e
}
func Token() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
