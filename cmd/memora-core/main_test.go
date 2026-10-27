package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
)

func TestBindsToLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"", true},
		{"127.0.0.1:7777", true},
		{"localhost:7777", true},
		{"[::1]:7777", true},
		{":7777", false},
		{"0.0.0.0:7777", false},
		{"10.0.0.1:7777", false},
		{"memora.example.com:7777", false},
	}
	for _, c := range cases {
		if got := bindsToLoopback(c.addr); got != c.want {
			t.Errorf("bindsToLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestValidateAuthMode(t *testing.T) {
	cases := []struct {
		name        string
		addr        string
		apiKey      string
		allowNoAuth bool
		wantErr     bool
	}{
		{"key set, any bind, ok", "0.0.0.0:7777", "secret", false, false},
		{"no key, loopback, ok", "127.0.0.1:7777", "", false, false},
		{"no key, bare port, refuse", ":7777", "", false, true},
		{"no key, all interfaces, refuse", "0.0.0.0:7777", "", false, true},
		{"no key, external host, refuse", "memora.example.com:7777", "", false, true},
		{"no key, non-loopback, allowNoAuth opt-in, ok", "0.0.0.0:7777", "", true, false},
		{"no key, empty addr, ok", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAuthMode(c.addr, c.apiKey, c.allowNoAuth)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateAuthMode(%q,%q,%v) err=%v wantErr=%v", c.addr, c.apiKey, c.allowNoAuth, err, c.wantErr)
			}
			if c.wantErr {
				if !errors.Is(err, errNoAuthNonLoopback) {
					t.Errorf("error must wrap errNoAuthNonLoopback, got %v", err)
				}
				if !strings.Contains(err.Error(), c.addr) {
					t.Errorf("error message must mention addr %q, got %q", c.addr, err.Error())
				}
			}
		})
	}
}

func TestHashTag(t *testing.T) {
	// Stable, length-8, deterministic, hex-only.
	tag := hashTag("hunter2")
	if len(tag) != 8 {
		t.Fatalf("hashTag len = %d, want 8", len(tag))
	}
	for _, r := range tag {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("hashTag(%q)=%q is not lowercase hex", "hunter2", tag)
		}
	}
	if hashTag("hunter2") != tag {
		t.Errorf("hashTag is not deterministic")
	}
	if hashTag("hunter2") == hashTag("hunter3") {
		t.Errorf("distinct keys must produce distinct tags (probabilistically; collision here is a sha256 break)")
	}
}

func TestApplyServeFlags_AsyncEmbed(t *testing.T) {
	cfg := config.Defaults()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	asyncEmbed := fs.Bool("async-embed", false, "")
	if err := fs.Parse([]string{"--async-embed"}); err != nil {
		t.Fatal(err)
	}
	applyServeFlags(fs, &cfg, "", "", "", "", "", "", "", "", "", "", "", false, false, *asyncEmbed)
	if !cfg.Embedding.Async {
		t.Fatal("--async-embed did not set embedding.async")
	}

	// Unset, the flag leaves a config-file value alone.
	cfg.Embedding.Async = true
	fs = flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Bool("async-embed", false, "")
	_ = fs.Parse(nil)
	applyServeFlags(fs, &cfg, "", "", "", "", "", "", "", "", "", "", "", false, false, false)
	if !cfg.Embedding.Async {
		t.Fatal("an unset --async-embed overrode embedding.async = true")
	}
}

// TestNewPeerKey: the printed SHA-256 is the hash of the printed key, so
// pasting it into inbound_key_sha256 authenticates exactly that key.
func TestNewPeerKey(t *testing.T) {
	k1, h1, err := newPeerKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, _, err := newPeerKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 43 || k1 == k2 {
		t.Fatalf("keys %q, %q: want two distinct 43-char keys", k1, k2)
	}
	sum := sha256.Sum256([]byte(k1))
	if h1 != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash %s is not SHA-256 of the key", h1)
	}
}
