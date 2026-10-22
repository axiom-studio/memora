package federation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sealer encrypts the outbound peer keys stored in the metadata store
// (AES-256-GCM). The key lives outside the database, so a database dump
// or backup alone does not reveal peer keys. Each sealed key is bound to
// its peer's node ID (as GCM additional data), so a sealed value copied
// onto another peer's row does not open.
type Sealer struct {
	aead cipher.AEAD
}

// SecretEnv names the environment variable that may hold the federation
// secret: 32 bytes, base64 (standard or URL alphabet).
const SecretEnv = "MEMORA_FEDERATION_SECRET"

// NewSealer builds a Sealer from a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("federation secret must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// LoadSealer returns the node's Sealer. The key comes from
// $MEMORA_FEDERATION_SECRET when set; otherwise from keyFile, which is
// created (32 random bytes, mode 0600) on first use. A key file readable
// by group or others is refused.
func LoadSealer(keyFile string) (*Sealer, error) {
	if v := strings.TrimSpace(os.Getenv(SecretEnv)); v != "" {
		key, err := decodeSecret(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", SecretEnv, err)
		}
		return NewSealer(key)
	}
	key, err := os.ReadFile(keyFile)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyFile, key, 0o600); err != nil {
			return nil, fmt.Errorf("create federation secret %s: %w", keyFile, err)
		}
		return NewSealer(key)
	}
	if err != nil {
		return nil, fmt.Errorf("read federation secret %s: %w", keyFile, err)
	}
	if info, err := os.Stat(keyFile); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("federation secret %s is readable by others (mode %04o); chmod 600", keyFile, info.Mode().Perm())
	}
	return NewSealer(key)
}

func decodeSecret(v string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// Seal encrypts plaintext for the peer nodeID. An empty plaintext seals
// to nil (no key).
func (s *Sealer) Seal(plaintext, nodeID string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(nodeID)), nil
}

// Open decrypts a value from Seal for the same nodeID. nil opens to "".
func (s *Sealer) Open(sealed []byte, nodeID string) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("sealed peer key is truncated")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(nodeID))
	if err != nil {
		return "", errors.New("sealed peer key does not open with this node's federation secret")
	}
	return string(plain), nil
}
