package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte, purpose string) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("external key must contain 32 bytes")
	}
	derived := sha256.Sum256(append(append([]byte{}, key...), []byte("agent-task-bridge:"+purpose)...))
	block, e := aes.NewCipher(derived[:])
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	return &Box{aead}, e
}
func (b *Box) Seal(raw []byte, aad string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, e := io.ReadFull(rand.Reader, nonce); e != nil {
		return nil, e
	}
	return b.aead.Seal(nonce, nonce, raw, []byte(aad)), nil
}
func (b *Box) Open(raw []byte, aad string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(raw) < n {
		return nil, errors.New("invalid sealed value")
	}
	return b.aead.Open(nil, raw[:n], raw[n:], []byte(aad))
}
func Token() (string, error) {
	raw := make([]byte, 32)
	_, e := rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw), e
}
func Hash(raw string) string { v := sha256.Sum256([]byte(raw)); return hex.EncodeToString(v[:]) }
