package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyPersistsAndCannotBeReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	first, err := loadKey(path, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadKey(path, false)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("key did not survive reload", err)
	}
	if _, err = loadKey(path, true); err == nil {
		t.Fatal("existing key overwritten")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadKey(path, false); err == nil {
		t.Fatal("public key file accepted")
	}
}

func TestCleartextOnlyLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", ":8080", "localhost:8080", "192.168.1.2:8080"} {
		if listenPolicy(address, "", "") == nil {
			t.Fatal("cleartext accepted", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if err := listenPolicy(address, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := listenPolicy("0.0.0.0:8443", "cert", "key"); err != nil {
		t.Fatal(err)
	}
}
