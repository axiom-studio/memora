package federation

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealer_RoundTripAndBinding(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal("peer-api-key-123", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "peer-api-key-123") {
		t.Fatal("sealed value contains the plaintext")
	}
	if got, err := s.Open(sealed, "node-a"); err != nil || got != "peer-api-key-123" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open(sealed, "node-b"); err == nil {
		t.Fatal("a key sealed for node-a opened for node-b")
	}
	other, _ := NewSealer(make([]byte, 32))
	if _, err := other.Open(sealed, "node-a"); err == nil {
		t.Fatal("a key opened with a different secret")
	}
	if sealed, _ := s.Seal("", "node-a"); sealed != nil {
		t.Fatal("empty key sealed to non-nil")
	}
	if got, err := s.Open(nil, "node-a"); err != nil || got != "" {
		t.Fatalf("Open(nil) = %q, %v", got, err)
	}
}

func TestLoadSealer_KeyFileAndEnv(t *testing.T) {
	t.Setenv(SecretEnv, "")
	path := filepath.Join(t.TempDir(), "sub", "federation.key")
	s1, err := LoadSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Fatalf("key file = %v, %v; want 32 bytes, 0600", info, err)
	}
	sealed, _ := s1.Seal("k", "n")
	s2, err := LoadSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Open(sealed, "n"); err != nil || got != "k" {
		t.Fatalf("reloaded sealer Open = %q, %v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSealer(path); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Fatalf("world-readable key: err = %v", err)
	}

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	t.Setenv(SecretEnv, base64.StdEncoding.EncodeToString(key))
	s3, err := LoadSealer(filepath.Join(t.TempDir(), "unused.key"))
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := NewSealer(key)
	sealed, _ = s3.Seal("x", "n")
	if got, err := direct.Open(sealed, "n"); err != nil || got != "x" {
		t.Fatalf("env secret not used: %q, %v", got, err)
	}
	t.Setenv(SecretEnv, "c2hvcnQ=")
	if _, err := LoadSealer(path); err == nil {
		t.Fatal("a short env secret was accepted")
	}
}
