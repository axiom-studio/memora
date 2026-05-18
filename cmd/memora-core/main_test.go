package main

import (
	"errors"
	"strings"
	"testing"
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
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
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
