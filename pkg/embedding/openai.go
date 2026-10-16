package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"
)

// OpenAIProvider calls the OpenAI embeddings REST API. Reads OPENAI_API_KEY
// from the environment; if absent, Embed returns ErrNoAPIKey.
type OpenAIProvider struct {
	model    string
	dim      int
	endpoint string
	apiKey   string
	client   *http.Client
}

// ErrNoAPIKey is returned when an HTTP-based provider is configured but
// the required key is missing from the environment.
var ErrNoAPIKey = errors.New("embedding: API key missing")

const openAIMaxBatch = 2048

func (p *OpenAIProvider) Name() string    { return "openai" }
func (p *OpenAIProvider) ModelID() string { return "openai:" + p.model }
func (p *OpenAIProvider) Dim() int        { return p.dim }

func (p *OpenAIProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         openAIMaxBatch,
		MaxInputTokens:       8191,
		ReturnsDeterministic: true,
		Quality:              "production",
	}
}

type openAIRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *OpenAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%w: set OPENAI_API_KEY", ErrNoAPIKey)
	}
	if len(texts) <= openAIMaxBatch {
		return p.embedBatch(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += openAIMaxBatch {
		end := i + openAIMaxBatch
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

func (p *OpenAIProvider) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	payload, _ := json.Marshal(openAIRequest{Model: p.model, Input: texts})

	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			base := time.Duration(1<<uint(attempt-1)) * time.Second
			//nolint:gosec // G404: retry jitter, not a security value
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
			lastErr = fmt.Errorf("openai: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			var er openAIResponse
			_ = json.NewDecoder(resp.Body).Decode(&er)
			resp.Body.Close()
			msg := ""
			if er.Error != nil {
				msg = er.Error.Message
			}
			lastErr = fmt.Errorf("openai: status %d: %s", resp.StatusCode, msg)

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

		var r openAIResponse
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

func isRetryable(code int) bool {
	return code == 429 || code == 500 || code == 502 || code == 503 || code == 504
}

func init() {
	Register("openai", func(model string) (Provider, error) {
		if model == "" {
			model = "text-embedding-3-small"
		}
		dim := 1536
		if model == "text-embedding-3-large" {
			dim = 3072
		}
		return &OpenAIProvider{
			model:    model,
			dim:      dim,
			endpoint: getenvDefault("OPENAI_BASE_URL", "https://api.openai.com/v1") + "/embeddings",
			apiKey:   os.Getenv("OPENAI_API_KEY"),
			client:   &http.Client{Timeout: 30 * time.Second},
		}, nil
	})
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
