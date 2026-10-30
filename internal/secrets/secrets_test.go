package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSealedReceiptsBindKeyPurposeAndContext(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	box, err := New(key, "receipt")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"lease_token":"test-sensitive-value"}`)
	sealed, err := box.Seal(plaintext, "principal:request-key")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("sealed receipt contains plaintext")
	}
	reopened, _ := New(key, "receipt")
	actual, err := reopened.Open(sealed, "principal:request-key")
	if err != nil || !bytes.Equal(actual, plaintext) {
		t.Fatalf("recreated external-key context failed: %q %v", actual, err)
	}
	otherKey, _ := New(bytes.Repeat([]byte{2}, 32), "receipt")
	otherPurpose, _ := New(key, "cursor")
	for name, candidate := range map[string]*Box{"key": otherKey, "purpose": otherPurpose} {
		if _, err := candidate.Open(sealed, "principal:request-key"); err == nil {
			t.Errorf("different %s opened receipt", name)
		}
	}
	if _, err := box.Open(sealed, "another-principal:request-key"); err == nil {
		t.Fatal("cross-principal/context replay authenticated")
	}
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, err := box.Open(tampered, "principal:request-key"); err == nil {
		t.Fatal("tampered ciphertext authenticated")
	}
	for length := 0; length < len(sealed); length++ {
		if _, err := box.Open(sealed[:length], "principal:request-key"); err == nil {
			t.Errorf("truncated ciphertext of length %d authenticated", length)
		}
	}
	sealedAgain, err := box.Seal(plaintext, "principal:request-key")
	if err != nil || bytes.Equal(sealed, sealedAgain) {
		t.Fatal("sealing reused ciphertext/nonce")
	}
}

func TestKeyAndTokenShape(t *testing.T) {
	for _, length := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, length), "receipt"); err == nil {
			t.Errorf("accepted %d-byte external key", length)
		}
	}
	seen := make(map[string]bool)
	for i := 0; i < 32; i++ {
		token, err := Token()
		raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if err != nil || decodeErr != nil || len(raw) != 32 || len(token) != 43 || seen[token] {
			t.Fatalf("invalid/repeated token: error=%v decode=%v", err, decodeErr)
		}
		seen[token] = true
		if hash := Hash(token); len(hash) != 64 || hash == token {
			t.Fatal("invalid token digest")
		}
	}
}
