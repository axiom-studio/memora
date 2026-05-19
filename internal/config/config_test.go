package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.Addr != ":7777" {
		t.Errorf("expected :7777, got %s", cfg.Server.Addr)
	}
	if cfg.Storage.MetadataDriver != "sqlite" {
		t.Errorf("expected sqlite, got %s", cfg.Storage.MetadataDriver)
	}
}

func TestLoad_FileNotFound_Defaults(t *testing.T) {
	t.Setenv("MEMORA_CONFIG", "")
	cfg, err := Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "" {
		t.Errorf("expected empty source, got %s", cfg.Source)
	}
	if cfg.Server.Addr != ":7777" {
		t.Errorf("expected default addr, got %s", cfg.Server.Addr)
	}
}

func TestLoad_ValidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[server]
addr = ":9999"
mode = "multi-tenant"

[storage]
data_dir = "/tmp/memora-test"
metadata_driver = "postgres"

[embedding]
model = "openai:text-embedding-3-small"

[telemetry]
log_level = "debug"
`
	os.WriteFile(path, []byte(content), 0o644)
	t.Setenv("MEMORA_CONFIG", path)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":9999" {
		t.Errorf("expected :9999, got %s", cfg.Server.Addr)
	}
	if cfg.Server.Mode != "multi-tenant" {
		t.Errorf("expected multi-tenant, got %s", cfg.Server.Mode)
	}
	if cfg.Storage.MetadataDriver != "postgres" {
		t.Errorf("expected postgres, got %s", cfg.Storage.MetadataDriver)
	}
	if cfg.Embedding.Model != "openai:text-embedding-3-small" {
		t.Errorf("expected openai model, got %s", cfg.Embedding.Model)
	}
	if cfg.Source != path {
		t.Errorf("expected source %s, got %s", path, cfg.Source)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	os.WriteFile(path, []byte(`[server`), 0o644)
	t.Setenv("MEMORA_CONFIG", path)

	_, err := Load("")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestValidate_TLS_MissingCert(t *testing.T) {
	cfg := Defaults()
	cfg.Server.TLS.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for TLS without cert")
	}
}

func TestValidate_TLS_AutoSelfSigned(t *testing.T) {
	cfg := Defaults()
	cfg.Server.TLS.Enabled = true
	cfg.Server.TLS.AutoSelfSigned = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_MutuallyExclusiveAPIKey(t *testing.T) {
	cfg := Defaults()
	cfg.Server.APIKey = "key"
	cfg.Server.APIKeyFile = "/path"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for both api_key and api_key_file")
	}
}

func TestValidate_BadMode(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Mode = "clustered"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestValidate_FederationNeedsID(t *testing.T) {
	cfg := Defaults()
	cfg.Federation.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for federation without id")
	}
}

func TestValidate_PeerNeedsURL(t *testing.T) {
	cfg := Defaults()
	cfg.Federation.Peers = []PeerConfig{{Name: "peer1"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for peer without URL")
	}
}

func TestResolveAPIKey_Inline(t *testing.T) {
	cfg := Defaults()
	cfg.Server.APIKey = "secret123"
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "secret123" {
		t.Errorf("expected secret123, got %s", key)
	}
}

func TestResolveAPIKey_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apikey")
	os.WriteFile(path, []byte("  file-secret \n"), 0o600)

	cfg := Defaults()
	cfg.Server.APIKeyFile = path
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "file-secret" {
		t.Errorf("expected file-secret, got %q", key)
	}
}

func TestResolveAPIKey_WorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apikey")
	os.WriteFile(path, []byte("secret"), 0o644)

	cfg := Defaults()
	cfg.Server.APIKeyFile = path
	_, err := cfg.ResolveAPIKey()
	if err == nil {
		t.Fatal("expected error for world-readable secret file")
	}
}

func TestApplyEnv(t *testing.T) {
	t.Setenv("MEMORA_ADDR", ":8888")
	t.Setenv("MEMORA_MODE", "multi-tenant")
	t.Setenv("MEMORA_METADATA_DRIVER", "postgres")

	cfg := Defaults()
	cfg.ApplyEnv()
	if cfg.Server.Addr != ":8888" {
		t.Errorf("expected :8888, got %s", cfg.Server.Addr)
	}
	if cfg.Server.Mode != "multi-tenant" {
		t.Errorf("expected multi-tenant, got %s", cfg.Server.Mode)
	}
	if cfg.Storage.MetadataDriver != "postgres" {
		t.Errorf("expected postgres, got %s", cfg.Storage.MetadataDriver)
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got := expandTilde("~/foo/bar")
	want := filepath.Join(home, "foo/bar")
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
	if expandTilde("/abs/path") != "/abs/path" {
		t.Error("absolute path should not be changed")
	}
}

func TestLoad_WithFederation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[federation]
enabled = true
federation_id = "fed_01HQ1234ABCDEF"

[[federation.peers]]
name = "us-west"
url = "https://west.memora.internal:7777"
api_key = "peer-key-1"

[[federation.peers]]
name = "eu-central"
url = "https://eu.memora.internal:7777"
tls_cert = "/etc/memora/eu.pem"
`
	os.WriteFile(path, []byte(content), 0o644)
	t.Setenv("MEMORA_CONFIG", path)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Federation.Enabled {
		t.Error("expected federation enabled")
	}
	if len(cfg.Federation.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg.Federation.Peers))
	}
	if cfg.Federation.Peers[0].Name != "us-west" {
		t.Errorf("expected us-west, got %s", cfg.Federation.Peers[0].Name)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestSearchPath_Precedence(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env.toml")
	flagPath := filepath.Join(dir, "flag.toml")
	os.WriteFile(envPath, []byte("[server]\naddr = \":1111\"\n"), 0o644)
	os.WriteFile(flagPath, []byte("[server]\naddr = \":2222\"\n"), 0o644)

	t.Setenv("MEMORA_CONFIG", envPath)
	cfg, err := Load(flagPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":1111" {
		t.Errorf("expected env to win over flag in search path; got addr=%s", cfg.Server.Addr)
	}
}
