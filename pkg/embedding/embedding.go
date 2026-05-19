// Package embedding defines the embedding-provider contract and the
// in-tree implementations.
//
// OSS v0.1 ships:
//   - noop:     zero-vector provider (default for local dev; keyword-only recall stays useful)
//   - openai:   text-embedding-3-small / -large via OpenAI's REST API
//   - voyage:   voyage-3 via Voyage AI
//   - gemini:   text-embedding-004 via Google
//
// A real local llama.cpp-backed `nomic-embed-text-v1.5` provider is on
// the v0.5 roadmap (per CLI spec §7.3's CGo-free constraint). For now
// the noop provider lets the rest of the pipeline operate end-to-end
// without external API keys; keyword + FTS recall still works.
package embedding

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// EmbeddingCapabilities describes what a provider supports.
type EmbeddingCapabilities struct {
	SupportsBatch        bool
	MaxBatchSize         int
	MaxInputTokens       int
	SupportsAsync        bool
	ReturnsDeterministic bool
	Quality              string // "production", "fallback", "placeholder"
}

// Provider implements an embedding model.
type Provider interface {
	Name() string
	ModelID() string
	Dim() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Capabilities() EmbeddingCapabilities
}

// EmbeddingConfig carries provider configuration, typically populated
// from MEMORA_EMBEDDING_MODEL.
type EmbeddingConfig struct {
	ModelID string // "openai:text-embedding-3-small", "voyage:voyage-3", etc.
}

// OpenFromEnv reads MEMORA_EMBEDDING_MODEL and opens the provider.
func OpenFromEnv() (Provider, error) {
	cfg := EmbeddingConfig{ModelID: os.Getenv("MEMORA_EMBEDDING_MODEL")}
	if cfg.ModelID == "" {
		cfg.ModelID = "noop:default"
	}
	return Open(cfg.ModelID)
}

// Factory builds a Provider from a model identifier like "openai:text-embedding-3-small".
type Factory func(modelID string) (Provider, error)

var (
	regMu     sync.RWMutex
	providers = map[string]Factory{}
)

// Register wires a provider factory keyed by name prefix (`openai`, `voyage`, ...).
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := providers[name]; dup {
		panic(fmt.Sprintf("embedding provider %q registered twice", name))
	}
	providers[name] = f
}

// Open parses a model id of the form `<provider>:<model>` and instantiates.
func Open(modelID string) (Provider, error) {
	if modelID == "" {
		modelID = "noop:default"
	}
	parts := strings.SplitN(modelID, ":", 2)
	name := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	regMu.RLock()
	f, ok := providers[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown embedding provider %q (registered: %v)", name, listNames())
	}
	return f(rest)
}

func listNames() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(providers))
	for k := range providers {
		out = append(out, k)
	}
	return out
}
