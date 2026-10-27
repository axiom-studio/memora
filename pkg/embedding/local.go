//go:build local_embed

package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kelindar/search"
)

const (
	localDim       = 768
	localMaxTokens = 8192
)

// LocalProvider runs a GGUF embedding model locally via kelindar/search
// (purego, no CGO at compile time). Requires libllama_go shared library
// at runtime. The GGUF model is auto-downloaded on first use.
//
// Build with: go build -tags local_embed
type LocalProvider struct {
	model    string
	modelDir string

	mu         sync.Mutex
	vectorizer *search.Vectorizer
}

func (p *LocalProvider) Name() string    { return "local" }
func (p *LocalProvider) ModelID() string { return "local:" + p.model }
func (p *LocalProvider) Dim() int        { return localDim }

func (p *LocalProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         64,
		MaxInputTokens:       localMaxTokens,
		ReturnsDeterministic: true,
		Quality:              "production",
	}
}

func (p *LocalProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	v, err := p.ensureModel()
	if err != nil {
		return nil, fmt.Errorf("local: %w", err)
	}

	out := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		vec, err := v.EmbedText(text)
		if err != nil {
			return nil, fmt.Errorf("local: embed text[%d]: %w", i, err)
		}
		out[i] = vec
	}
	return out, nil
}

func (p *LocalProvider) ensureModel() (*search.Vectorizer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vectorizer != nil {
		return p.vectorizer, nil
	}

	modelPath := filepath.Join(p.modelDir, defaultModelName)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		if err := downloadModel(defaultModelURL, modelPath, defaultModelSHA); err != nil {
			return nil, fmt.Errorf("download model: %w", err)
		}
	} else if err := verifyModel(modelPath, defaultModelSHA); err != nil {
		return nil, err
	}

	v, err := search.NewVectorizer(modelPath, 0)
	if err != nil {
		return nil, fmt.Errorf("load model %s: %w", modelPath, err)
	}
	p.vectorizer = v
	return v, nil
}

// Close releases the vectorizer resources.
func (p *LocalProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vectorizer != nil {
		err := p.vectorizer.Close()
		p.vectorizer = nil
		return err
	}
	return nil
}

func defaultModelDir() string {
	if d := os.Getenv("MEMORA_MODEL_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".memora", "models")
	}
	return filepath.Join(home, ".memora", "models")
}

func init() {
	Register("local", func(model string) (Provider, error) {
		if model == "" {
			model = "nomic-embed-text-v1.5"
		}
		return &LocalProvider{
			model:    model,
			modelDir: defaultModelDir(),
		}, nil
	})
}
