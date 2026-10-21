package embedding

import "context"

// NoopProvider returns deterministic vectors derived from the bytes of
// the input text. They carry no similarity, so Searchable reports false:
// vector search is unavailable and hybrid falls back to keyword. Use
// hash:default for offline vector search.
type NoopProvider struct {
	model string
	dim   int
}

func (p *NoopProvider) Name() string    { return "noop" }
func (p *NoopProvider) ModelID() string { return "noop:" + p.model }
func (p *NoopProvider) Dim() int        { return p.dim }

func (p *NoopProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         10000,
		ReturnsDeterministic: true,
		Quality:              "fallback",
	}
}

func (p *NoopProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, p.dim)
		// Spread a tiny signal over the vector to avoid pure-zero norm
		// breaking cosine division. Use bytes of the input modulo dim.
		for j, b := range []byte(t) {
			v[j%p.dim] += float32(b) / 255.0
		}
		out[i] = v
	}
	return out, nil
}

func init() {
	Register("noop", func(model string) (Provider, error) {
		if model == "" {
			model = "default"
		}
		return &NoopProvider{model: model, dim: 384}, nil
	})
}
