// Package config provides TOML-based configuration loading for memora-core.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// EX_CONFIG is the sysexits.h exit code for configuration errors.
const EX_CONFIG = 78

// Config is the top-level configuration file schema.
type Config struct {
	Server     ServerConfig     `toml:"server"`
	Storage    StorageConfig    `toml:"storage"`
	Embedding  EmbeddingConfig  `toml:"embedding"`
	Federation FederationConfig `toml:"federation"`
	Telemetry  TelemetryConfig  `toml:"telemetry"`

	// Source records which file the config was loaded from ("" = defaults only).
	Source string `toml:"-"`
}

type ServerConfig struct {
	Addr       string    `toml:"addr"`
	Mode       string    `toml:"mode"`
	APIKey     string    `toml:"api_key"`
	APIKeyFile string    `toml:"api_key_file"`
	AllowNoAuth bool    `toml:"allow_no_auth"`
	MCPEnable  bool      `toml:"mcp_enable"`
	TLS        TLSConfig `toml:"tls"`
}

type TLSConfig struct {
	Enabled        bool   `toml:"enabled"`
	CertFile       string `toml:"cert_file"`
	KeyFile        string `toml:"key_file"`
	AutoSelfSigned bool   `toml:"auto_self_signed"`
}

type StorageConfig struct {
	DataDir        string `toml:"data_dir"`
	MetadataDriver string `toml:"metadata_driver"`
	VectorDriver   string `toml:"vector_driver"`
	LedgerDriver   string `toml:"ledger_driver"`
	GraphDriver    string `toml:"graph_driver"`
	ContentDriver  string `toml:"content_driver"`
	ContentDSN     string `toml:"content_dsn"`
}

type EmbeddingConfig struct {
	Model string `toml:"model"`
}

type FederationConfig struct {
	Enabled      bool         `toml:"enabled"`
	FederationID string       `toml:"federation_id"`
	Peers        []PeerConfig `toml:"peers"`
}

type PeerConfig struct {
	Name    string `toml:"name"`
	URL     string `toml:"url"`
	APIKey  string `toml:"api_key"`
	TLSCert string `toml:"tls_cert"`
}

type TelemetryConfig struct {
	LogLevel  string `toml:"log_level"`
	LogFormat string `toml:"log_format"`
}

// Defaults returns a Config with production-safe defaults.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Addr: ":7777",
			Mode: "single-tenant",
		},
		Storage: StorageConfig{
			DataDir:        "./data",
			MetadataDriver: "sqlite",
			VectorDriver:   "sqlite-vec",
			LedgerDriver:   "sqlite",
			GraphDriver:    "sqlite_graph",
		},
		Embedding: EmbeddingConfig{
			Model: "noop:default",
		},
		Telemetry: TelemetryConfig{
			LogLevel:  "info",
			LogFormat: "json",
		},
	}
}

// Load finds and parses a config file using the standard search path:
//
//	$MEMORA_CONFIG → flagPath → ~/.memora/config.toml → ./memora.toml → defaults
//
// flagPath is the value of --config (empty string if not provided).
// Env vars and CLI flags override individual fields after loading; that
// is the caller's responsibility (see ApplyEnv / ApplyFlags).
func Load(flagPath string) (Config, error) {
	cfg := Defaults()
	path := resolveSearchPath(flagPath)
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Source = path
	cfg.expandPaths()
	return cfg, nil
}

// resolveSearchPath returns the first config file that exists, or ""
// if none is found.
func resolveSearchPath(flagPath string) string {
	candidates := []string{}
	if v := os.Getenv("MEMORA_CONFIG"); v != "" {
		candidates = append(candidates, expandTilde(v))
	}
	if flagPath != "" {
		candidates = append(candidates, expandTilde(flagPath))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".memora", "config.toml"))
	}
	candidates = append(candidates, "memora.toml")

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

func (c *Config) expandPaths() {
	c.Storage.DataDir = expandTilde(c.Storage.DataDir)
	c.Storage.ContentDSN = expandTilde(c.Storage.ContentDSN)
	c.Server.TLS.CertFile = expandTilde(c.Server.TLS.CertFile)
	c.Server.TLS.KeyFile = expandTilde(c.Server.TLS.KeyFile)
	c.Server.APIKeyFile = expandTilde(c.Server.APIKeyFile)
}

// Validate checks for configuration errors. Returns a non-nil error
// suitable for display; the caller should os.Exit(EX_CONFIG).
func (c *Config) Validate() error {
	var errs []error

	if c.Server.TLS.Enabled {
		hasCert := c.Server.TLS.CertFile != "" && c.Server.TLS.KeyFile != ""
		if !hasCert && !c.Server.TLS.AutoSelfSigned {
			errs = append(errs, errors.New("server.tls.enabled requires cert_file+key_file or auto_self_signed"))
		}
	}

	if c.Server.APIKey != "" && c.Server.APIKeyFile != "" {
		errs = append(errs, errors.New("server.api_key and server.api_key_file are mutually exclusive"))
	}

	switch c.Server.Mode {
	case "single-tenant", "multi-tenant":
	default:
		errs = append(errs, fmt.Errorf("server.mode: unknown %q (want single-tenant or multi-tenant)", c.Server.Mode))
	}

	switch c.Telemetry.LogLevel {
	case "debug", "info", "warn", "error", "":
	default:
		errs = append(errs, fmt.Errorf("telemetry.log_level: unknown %q", c.Telemetry.LogLevel))
	}

	switch c.Telemetry.LogFormat {
	case "json", "text", "":
	default:
		errs = append(errs, fmt.Errorf("telemetry.log_format: unknown %q", c.Telemetry.LogFormat))
	}

	if c.Federation.Enabled && c.Federation.FederationID == "" {
		errs = append(errs, errors.New("federation.enabled requires federation.federation_id"))
	}
	for i, p := range c.Federation.Peers {
		if p.URL == "" {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: url is required", i))
		}
	}

	return errors.Join(errs...)
}

// ResolveAPIKey resolves the API key from either the inline value or
// the api_key_file path. It refuses to read a world-readable secret file.
func (c *Config) ResolveAPIKey() (string, error) {
	if c.Server.APIKey != "" {
		return c.Server.APIKey, nil
	}
	if c.Server.APIKeyFile == "" {
		return "", nil
	}
	path := c.Server.APIKeyFile
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("api_key_file: %w", err)
	}
	if info.Mode().Perm()&0o004 != 0 {
		return "", fmt.Errorf("api_key_file %s is world-readable (mode %04o); chmod 600 or 640", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("api_key_file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ApplyEnv overlays environment variables onto the config. Each env var
// wins over the file value when set and non-empty.
func (c *Config) ApplyEnv() {
	setIfEnv(&c.Server.Addr, "MEMORA_ADDR")
	setIfEnv(&c.Server.Mode, "MEMORA_MODE")
	setIfEnv(&c.Server.APIKey, "MEMORA_API_KEY")
	setIfEnv(&c.Storage.DataDir, "MEMORA_DATA_DIR")
	setIfEnv(&c.Storage.MetadataDriver, "MEMORA_METADATA_DRIVER")
	setIfEnv(&c.Storage.VectorDriver, "MEMORA_VECTOR_DRIVER")
	setIfEnv(&c.Storage.LedgerDriver, "MEMORA_LEDGER_DRIVER")
	setIfEnv(&c.Storage.GraphDriver, "MEMORA_GRAPH_DRIVER")
	setIfEnv(&c.Storage.ContentDriver, "MEMORA_CONTENT_DRIVER")
	setIfEnv(&c.Storage.ContentDSN, "MEMORA_CONTENT_DSN")
	setIfEnv(&c.Embedding.Model, "MEMORA_EMBEDDING_MODEL")
	if os.Getenv("MEMORA_MCP_ENABLE") == "true" {
		c.Server.MCPEnable = true
	}
}

func setIfEnv(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}
