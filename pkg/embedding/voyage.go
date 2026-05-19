package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"
)

// VoyageProvider calls the Voyage AI embeddings API.
type VoyageProvider struct {
	model    string
	dim      int
	endpoint string
	apiKey   string
	client   *http.Client
}

const voyageMaxBatch = 128

func (p *VoyageProvider) Name() string    { return "voyage" }
func (p *VoyageProvider) ModelID() string { return "voyage:" + p.model }
func (p *VoyageProvider) Dim() int        { return p.dim }

func (p *VoyageProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         voyageMaxBatch,
		MaxInputTokens:       32000,
		ReturnsDeterministic: true,
		Quality:              "production",
	}
}

type voyageRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type voyageResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Detail string `json:"detail,omitempty"`
}

func (p *VoyageProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%w: set VOYAGE_API_KEY", ErrNoAPIKey)
	}
	if len(texts) <= voyageMaxBatch {
		return p.embedBatch(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += voyageMaxBatch {
		end := i + voyageMaxBatch
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := p.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		copy(out[i:], batch)
	}
	return out, nil
}

func (p *VoyageProvider) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	payload, _ := json.Marshal(voyageRequest{Model: p.model, Input: texts})

	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			base := time.Duration(1<<uint(attempt-1)) * time.Second
			jitter := time.Duration(float64(base) * (0.8 + 0.4*rand.Float64()))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(jitter):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("voyage: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			var er voyageResponse
			_ = json.NewDecoder(resp.Body).Decode(&er)
			resp.Body.Close()
			lastErr = fmt.Errorf("voyage: status %d: %s", resp.StatusCode, er.Detail)
			if isRetryable(resp.StatusCode) {
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if secs, err := strconv.Atoi(ra); err == nil {
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(time.Duration(secs) * time.Second):
						}
					}
				}
				continue
			}
			return nil, lastErr
		}

		var r voyageResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		out := make([][]float32, len(texts))
		for _, d := range r.Data {
			if d.Index < len(out) {
				out[d.Index] = d.Embedding
			}
		}
		return out, nil
	}
	return nil, lastErr
}

func init() {
	Register("voyage", func(model string) (Provider, error) {
		if model == "" {
			model = "voyage-3"
		}
		dim := 1024
		return &VoyageProvider{
			model:    model,
			dim:      dim,
			endpoint: getenvDefault("VOYAGE_BASE_URL", "https://api.voyageai.com/v1") + "/embeddings",
			apiKey:   os.Getenv("VOYAGE_API_KEY"),
			client:   &http.Client{Timeout: 30 * time.Second},
		}, nil
	})
}
