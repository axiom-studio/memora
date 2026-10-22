// Package config provides TOML-based configuration loading for memora-core.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/axiom-studio/memora/pkg/types"
)

// EX_CONFIG is the sysexits.h exit code for configuration errors.
const EX_CONFIG = 78

// Config is the top-level configuration file schema.
type Config struct {
	Server     ServerConfig     `toml:"server"`
	Storage    StorageConfig    `toml:"storage"`
	Embedding  EmbeddingConfig  `toml:"embedding"`
	Node       NodeConfig       `toml:"node"`
	Federation FederationConfig `toml:"federation"`
	Telemetry  TelemetryConfig  `toml:"telemetry"`

	// Source records which file the config was loaded from ("" = defaults only).
	Source string `toml:"-"`
}

type ServerConfig struct {
	Addr        string `toml:"addr"`
	Mode        string `toml:"mode"`
	APIKey      string `toml:"api_key"`
	APIKeyFile  string `toml:"api_key_file"`
	AllowNoAuth bool   `toml:"allow_no_auth"`
	MCPEnable   bool   `toml:"mcp_enable"`
	// UICookieSecure sets the console session cookie's Secure flag:
	// "always", "never", or "auto" (Secure only for requests that arrived
	// over TLS, which is never true behind a TLS-terminating proxy).
	// Empty means "always" on a non-loopback addr and "auto" on loopback (#6033).
	UICookieSecure string    `toml:"ui_cookie_secure"`
	TLS            TLSConfig `toml:"tls"`
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
	MetadataDSN    string `toml:"metadata_dsn"`
	VectorDriver   string `toml:"vector_driver"`
	VectorDSN      string `toml:"vector_dsn"`
	LedgerDriver   string `toml:"ledger_driver"`
	LedgerDSN      string `toml:"ledger_dsn"`
	GraphDriver    string `toml:"graph_driver"`
	GraphDSN       string `toml:"graph_dsn"`
	ContentDriver  string `toml:"content_driver"`
	ContentDSN     string `toml:"content_dsn"`
}

type EmbeddingConfig struct {
	Model string `toml:"model"`
	// Async moves cell embedding off the write path onto an in-process
	// worker pool: writes return recall_ready=false and the Memory turns
	// recall_ready once its cells are embedded. Default false (inline).
	Async bool `toml:"async"`
}

// NodeConfig identifies this node to its federation peers. ID is a
// UUIDv4; when empty the node mints one on first start and keeps it in
// the metadata store. Name is a display label (default: the hostname).
type NodeConfig struct {
	ID   string `toml:"id"`
	Name string `toml:"name"`
}

type FederationConfig struct {
	Enabled bool `toml:"enabled"`
	// FederationID is an optional UUIDv4 cluster label. Nodes are
	// identified by [node] id, never by this.
	FederationID string `toml:"federation_id"`
	// InboundQPS caps each peer's calls to this node's peer routes
	// (default federation.DefaultInboundQPS).
	InboundQPS float64 `toml:"inbound_qps"`
	// PeerTimeoutMS and OutboundQPS are the defaults for each peer's
	// timeout_ms and rate_limit_qps (5000 ms and 50 when unset).
	PeerTimeoutMS int `toml:"peer_timeout_ms"`
	// SecretKeyFile holds the 32-byte key that seals peer api_keys in
	// the metadata store (default <data_dir>/federation.key, created on
	// first start). $MEMORA_FEDERATION_SECRET overrides it.
	SecretKeyFile string  `toml:"secret_key_file"`
	OutboundQPS   float64 `toml:"outbound_qps"`
	// InsecurePeerHosts lists peer URL hosts this node may call over
	// http://, which sends the peer key in cleartext. Loopback hosts
	// need no entry; any other http:// peer is refused.
	InsecurePeerHosts []string `toml:"insecure_peer_hosts"`
	// PrivatePeerHosts lets peers reach loopback, private, link-local
	// and other non-public addresses, which are refused otherwise. An
	// entry is a URL host (name or IP), which may then resolve to any
	// address, or a CIDR, which allows the addresses inside it.
	PrivatePeerHosts []string     `toml:"private_peer_hosts"`
	Peers            []PeerConfig `toml:"peers"`
}

type PeerConfig struct {
	NodeID string `toml:"node_id"` // the peer's [node] id (UUIDv4)
	Name   string `toml:"name"`
	URL    string `toml:"url"`
	// APIKey is the outbound credential: the key this node presents to
	// the peer in X-Memora-Federation-Auth.
	APIKey  string `toml:"api_key"`
	TLSCert string `toml:"tls_cert"`
	// ClientCertFile and ClientKeyFile are the TLS client certificate
	// this node presents when it calls the peer (outbound mTLS).
	ClientCertFile string `toml:"client_cert_file"`
	ClientKeyFile  string `toml:"client_key_file"`
	// InboundKeySHA256 is the hex SHA-256 of the key the peer presents
	// to this node. Only the hash is configured, so the key itself never
	// sits in this node's config. Empty means the peer cannot call in.
	InboundKeySHA256 string `toml:"inbound_key_sha256"`
	// ClientCertSHA256, when set, additionally requires the peer to
	// present a TLS client certificate with this SHA-256 fingerprint.
	ClientCertSHA256 string `toml:"client_cert_sha256"`
	// Revoked keeps the peer known but refuses its inbound calls (403)
	// and leaves it out of federated searches.
	Revoked bool `toml:"revoked"`
	// TimeoutMS and RateLimitQPS bound this node's calls to the peer;
	// 0 takes federation.peer_timeout_ms / federation.outbound_qps.
	TimeoutMS    int     `toml:"timeout_ms"`
	RateLimitQPS float64 `toml:"rate_limit_qps"`
	// InboundQPS caps this peer's calls to this node; 0 takes
	// federation.inbound_qps.
	InboundQPS float64 `toml:"inbound_qps"`
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
		//nolint:gosec // G703: config search paths come from the operator (flag, env, home dir), by design
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
	c.Storage.MetadataDSN = expandTilde(c.Storage.MetadataDSN)
	c.Storage.VectorDSN = expandTilde(c.Storage.VectorDSN)
	c.Storage.LedgerDSN = expandTilde(c.Storage.LedgerDSN)
	c.Storage.GraphDSN = expandTilde(c.Storage.GraphDSN)
	c.Storage.ContentDSN = expandTilde(c.Storage.ContentDSN)
	c.Server.TLS.CertFile = expandTilde(c.Server.TLS.CertFile)
	c.Server.TLS.KeyFile = expandTilde(c.Server.TLS.KeyFile)
	c.Server.APIKeyFile = expandTilde(c.Server.APIKeyFile)
	c.Federation.SecretKeyFile = expandTilde(c.Federation.SecretKeyFile)
	for i := range c.Federation.Peers {
		p := &c.Federation.Peers[i]
		p.TLSCert = expandTilde(p.TLSCert)
		p.ClientCertFile = expandTilde(p.ClientCertFile)
		p.ClientKeyFile = expandTilde(p.ClientKeyFile)
	}
}

// GraphEnabled reports whether a graph store should be opened. "" and
// "none" both disable it; "none" exists because env overlays ignore empty
// values, so MEMORA_GRAPH_DRIVER="" cannot express "off".
func (s StorageConfig) GraphEnabled() bool {
	return s.GraphDriver != "" && s.GraphDriver != "none"
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

	switch c.Server.UICookieSecure {
	case "", "auto", "always", "never":
	default:
		errs = append(errs, fmt.Errorf("server.ui_cookie_secure: unknown %q (want auto, always or never)", c.Server.UICookieSecure))
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

	// sqlite_graph reads the memora_edges/memora_memories tables that only
	// the sqlite metadata migrations create, and opens without migrating, so
	// any other metadata driver boots "healthy" with a graph store that fails
	// on first use (Issue #4378). No other graph driver exists yet.
	if c.Storage.GraphDriver == "sqlite_graph" && c.Storage.MetadataDriver != "sqlite" {
		errs = append(errs, fmt.Errorf(`storage.graph_driver "sqlite_graph" requires storage.metadata_driver "sqlite" (got %q); set storage.graph_driver = "none" (or MEMORA_GRAPH_DRIVER=none) to run without the context graph`, c.Storage.MetadataDriver))
	}

	if c.Node.ID != "" {
		if _, err := types.ParseID("node.id", c.Node.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Federation.FederationID != "" {
		if _, err := types.ParseID("federation.federation_id", c.Federation.FederationID); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Federation.InboundQPS < 0 || c.Federation.OutboundQPS < 0 || c.Federation.PeerTimeoutMS < 0 {
		errs = append(errs, errors.New("federation.inbound_qps, outbound_qps and peer_timeout_ms must not be negative"))
	}
	for _, h := range c.Federation.InsecurePeerHosts {
		if strings.TrimSpace(h) == "" {
			errs = append(errs, errors.New("federation.insecure_peer_hosts: empty entry"))
		}
	}
	for _, h := range c.Federation.PrivatePeerHosts {
		if strings.TrimSpace(h) == "" {
			errs = append(errs, errors.New("federation.private_peer_hosts: empty entry"))
		} else if strings.Contains(h, "/") {
			if _, err := netip.ParsePrefix(strings.TrimSpace(h)); err != nil {
				errs = append(errs, fmt.Errorf("federation.private_peer_hosts: %q is not a CIDR: %v", h, err))
			}
		}
	}
	for i, p := range c.Federation.Peers {
		if _, err := types.ParseID(fmt.Sprintf("federation.peers[%d].node_id", i), p.NodeID); err != nil {
			errs = append(errs, err)
		}
		if p.URL == "" {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: url is required", i))
		}
		if p.TimeoutMS < 0 || p.RateLimitQPS < 0 || p.InboundQPS < 0 {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: timeout_ms, rate_limit_qps and inbound_qps must not be negative", i))
		}
		if (p.ClientCertFile == "") != (p.ClientKeyFile == "") {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: client_cert_file and client_key_file must be set together", i))
		}
		if p.InboundKeySHA256 != "" && !isSHA256Hex(p.InboundKeySHA256) {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: inbound_key_sha256 must be 64 hex characters", i))
		}
		if p.ClientCertSHA256 != "" && !isSHA256Hex(p.ClientCertSHA256) {
			errs = append(errs, fmt.Errorf("federation.peers[%d]: client_cert_sha256 must be 64 hex characters", i))
		}
	}

	return errors.Join(errs...)
}

// isSHA256Hex reports whether s is a hex-encoded SHA-256 digest.
func isSHA256Hex(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
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
	setIfEnv(&c.Node.ID, "MEMORA_NODE_ID")
	setIfEnv(&c.Node.Name, "MEMORA_NODE_NAME")
	setIfEnv(&c.Server.Mode, "MEMORA_MODE")
	setIfEnv(&c.Server.APIKey, "MEMORA_API_KEY")
	setIfEnv(&c.Storage.DataDir, "MEMORA_DATA_DIR")
	setIfEnv(&c.Storage.MetadataDriver, "MEMORA_METADATA_DRIVER")
	setIfEnv(&c.Storage.VectorDriver, "MEMORA_VECTOR_DRIVER")
	setIfEnv(&c.Storage.LedgerDriver, "MEMORA_LEDGER_DRIVER")
	setIfEnv(&c.Storage.GraphDriver, "MEMORA_GRAPH_DRIVER")
	setIfEnv(&c.Storage.ContentDriver, "MEMORA_CONTENT_DRIVER")
	setIfEnv(&c.Storage.MetadataDSN, "MEMORA_METADATA_DSN")
	setIfEnv(&c.Storage.VectorDSN, "MEMORA_VECTOR_DSN")
	setIfEnv(&c.Storage.LedgerDSN, "MEMORA_LEDGER_DSN")
	setIfEnv(&c.Storage.GraphDSN, "MEMORA_GRAPH_DSN")
	setIfEnv(&c.Storage.ContentDSN, "MEMORA_CONTENT_DSN")
	setIfEnv(&c.Embedding.Model, "MEMORA_EMBEDDING_MODEL")
	setIfEnv(&c.Server.UICookieSecure, "MEMORA_UI_COOKIE_SECURE")
	if os.Getenv("MEMORA_EMBEDDING_ASYNC") == "true" {
		c.Embedding.Async = true
	}
	if os.Getenv("MEMORA_MCP_ENABLE") == "true" {
		c.Server.MCPEnable = true
	}
}

func setIfEnv(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// Redacted returns a copy of the config with secrets replaced by "***".
func (c *Config) Redacted() Config {
	r := *c
	if r.Server.APIKey != "" {
		r.Server.APIKey = "***"
	}
	if r.Server.APIKeyFile != "" {
		r.Server.APIKeyFile = "***"
	}
	for i := range r.Federation.Peers {
		if r.Federation.Peers[i].APIKey != "" {
			r.Federation.Peers[i].APIKey = "***"
		}
	}
	return r
}
