package embedding

import (
	"context"
	"errors"
)

// AxiomStudioProvider is a placeholder for the Cloud-hosted embedding
// gateway. Returns 501 in OSS; wiring to the gateway lands in v0.3.
type AxiomStudioProvider struct {
	model string
	dim   int
}

var errNotImplemented = errors.New("axiomstudio: 501 not implemented; Cloud gateway wiring lands in v0.3")

func (p *AxiomStudioProvider) Name() string    { return "axiomstudio" }
func (p *AxiomStudioProvider) ModelID() string { return "axiomstudio:" + p.model }
func (p *AxiomStudioProvider) Dim() int        { return p.dim }

func (p *AxiomStudioProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		Quality: "placeholder",
	}
}

func (p *AxiomStudioProvider) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, errNotImplemented
}

func init() {
	// Fail at Open, i.e. at server start, rather than on every embed:
	// a node configured for a gateway it cannot reach should not start
	// and then quietly serve keyword-only results.
	Register("axiomstudio", func(string) (Provider, error) {
		return nil, errNotImplemented
	})
}
