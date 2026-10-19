package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func TestValidate_GraphDriverMetadataCompatibility(t *testing.T) {
	cases := []struct {
		name           string
		metadataDriver string
		graphDriver    string
		graphDSN       string
		wantErr        bool
	}{
		{"defaults: sqlite metadata + sqlite_graph", "sqlite", "sqlite_graph", "", false},
		{"postgres metadata + sqlite_graph", "postgres", "sqlite_graph", "", true},
		{"postgres metadata + sqlite_graph with explicit dsn", "postgres", "sqlite_graph", "/data/graph.db", true},
		{"postgres metadata + graph disabled", "postgres", "", "", false},
		{"sqlite metadata + graph disabled", "sqlite", "", "", false},
		{"postgres metadata + graph none sentinel", "postgres", "none", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Storage.MetadataDriver = c.metadataDriver
			cfg.Storage.GraphDriver = c.graphDriver
			cfg.Storage.GraphDSN = c.graphDSN
			err := cfg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), `graph_driver = "none"`) {
				t.Fatalf("error %q should tell the operator how to disable the graph", err)
			}
		})
	}
}

func TestLoad_ShippedDeployConfigsValidate(t *testing.T) {
	paths, err := filepath.Glob("../../deploy/*/node*.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob deploy configs: %v (found %d)", err, len(paths))
	}
	for _, p := range paths {
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", p, err)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s does not validate: %v", p, err)
		}
	}
}

// Shipped deploy configs are copied verbatim by operators, and this repo is
// public, so they must not carry credentials (#5988, #6032): no password in a
// DSN, no inline API key.
func TestShippedDeployConfigs_ContainNoCredentials(t *testing.T) {
	paths, err := filepath.Glob("../../deploy/*/*.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob deploy configs: %v (found %d)", err, len(paths))
	}
	dsnWithPassword := regexp.MustCompile(`://[^/@\s:]+:[^/@\s]+@`)
	for _, p := range paths {
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", p, err)
		}
		s := cfg.Storage
		for name, dsn := range map[string]string{
			"metadata_dsn": s.MetadataDSN, "vector_dsn": s.VectorDSN, "ledger_dsn": s.LedgerDSN,
			"graph_dsn": s.GraphDSN, "content_dsn": s.ContentDSN,
		} {
			if dsnWithPassword.MatchString(dsn) {
				t.Errorf("%s: %s embeds a password; supply it via MEMORA_*_DSN from a secret", p, name)
			}
		}
		if cfg.Server.APIKey != "" {
			t.Errorf("%s: server.api_key is inline; supply it via MEMORA_API_KEY from a secret", p)
		}
		for i, peer := range cfg.Federation.Peers {
			if peer.APIKey != "" {
				t.Errorf("%s: federation.peers[%d].api_key is inline", p, i)
			}
		}
	}
}

// The compose files are run as-is from the README, and this repo is public
// (#6032): no secret may be inline or have a literal fallback, no DSN may
// embed a literal password, and the dev topologies publish on loopback only,
// so a stack started on a shared network is not reachable from it.
func TestShippedComposeFiles_NoCommittedSecretsAndDevLoopback(t *testing.T) {
	paths, err := filepath.Glob("../../deploy/*/docker-compose.yml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob deploy compose files: %v (found %d)", err, len(paths))
	}
	paths = append(paths, "../../docker-compose.yml")
	devTopology := map[string]bool{"../../docker-compose.yml": true, "../../deploy/federation/docker-compose.yml": true}

	const secretName = `\w*(?:KEY|PASSWORD|SECRET|TOKEN)\w*`
	defaulted := regexp.MustCompile(`\$\{` + secretName + `:?-`)
	inline := regexp.MustCompile(`^\s*` + secretName + `:\s*"?([^"\s]+)`)
	dsnPassword := regexp.MustCompile(`://[^/@\s:]+:([^/@\s]+)@`)
	portLine := regexp.MustCompile(`^\s*-\s*"?([^"\s]+)"?\s*$`)

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		inPorts := false
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			where := fmt.Sprintf("%s:%d", p, i+1)
			if defaulted.MatchString(line) {
				t.Errorf("%s: secret has a literal fallback; use ${VAR:?message}: %s", where, strings.TrimSpace(line))
			}
			if m := inline.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "${") {
				t.Errorf("%s: secret is inline: %s", where, strings.TrimSpace(line))
			}
			if m := dsnPassword.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "${") {
				t.Errorf("%s: DSN embeds a literal password", where)
			}
			if strings.TrimSpace(line) == "ports:" {
				inPorts = true
				continue
			}
			m := portLine.FindStringSubmatch(line)
			if !inPorts || m == nil {
				inPorts = false
				continue
			}
			if devTopology[p] && !strings.HasPrefix(m[1], "127.0.0.1:") {
				t.Errorf("%s: dev topology publishes %q on every interface; bind it to 127.0.0.1", where, m[1])
			}
		}
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

// Todo #4229: async embed is opt-in from the file or the environment.
func TestEmbeddingAsync_DefaultOffFileAndEnv(t *testing.T) {
	if Defaults().Embedding.Async {
		t.Fatal("embedding.async defaults to true; want false (inline embed)")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[embedding]\nasync = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEMORA_CONFIG", path)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Embedding.Async {
		t.Fatal("[embedding] async = true not loaded")
	}

	env := Defaults()
	t.Setenv("MEMORA_EMBEDDING_ASYNC", "true")
	env.ApplyEnv()
	if !env.Embedding.Async {
		t.Fatal("MEMORA_EMBEDDING_ASYNC=true not applied")
	}
}

func TestUICookieSecure_ValidateAndEnv(t *testing.T) {
	for _, v := range []string{"", "auto", "always", "never"} {
		cfg := Defaults()
		cfg.Server.UICookieSecure = v
		if err := cfg.Validate(); err != nil {
			t.Errorf("ui_cookie_secure %q rejected: %v", v, err)
		}
	}
	cfg := Defaults()
	cfg.Server.UICookieSecure = "true"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ui_cookie_secure") {
		t.Errorf("ui_cookie_secure \"true\" accepted or unclear error: %v", err)
	}

	t.Setenv("MEMORA_UI_COOKIE_SECURE", "always")
	env := Defaults()
	env.ApplyEnv()
	if env.Server.UICookieSecure != "always" {
		t.Errorf("MEMORA_UI_COOKIE_SECURE not applied: %q", env.Server.UICookieSecure)
	}
}
