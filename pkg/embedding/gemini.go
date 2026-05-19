package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"time"
)

// GeminiProvider calls Google's text-embedding API.
type GeminiProvider struct {
	model    string
	dim      int
	endpoint string
	apiKey   string
	client   *http.Client
}

const geminiMaxBatch = 100

func (p *GeminiProvider) Name() string    { return "gemini" }
func (p *GeminiProvider) ModelID() string { return "gemini:" + p.model }
func (p *GeminiProvider) Dim() int        { return p.dim }

func (p *GeminiProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         geminiMaxBatch,
		MaxInputTokens:       2048,
		ReturnsDeterministic: true,
		Quality:              "production",
	}
}

type geminiRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiEmbedRequest struct {
	Model   string            `json:"model"`
	Content geminiTextContent `json:"content"`
}

type geminiTextContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *GeminiProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%w: set GEMINI_API_KEY", ErrNoAPIKey)
	}
	if len(texts) <= geminiMaxBatch {
		return p.embedBatch(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += geminiMaxBatch {
		end := i + geminiMaxBatch
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

func (p *GeminiProvider) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqs := make([]geminiEmbedRequest, len(texts))
	for i, t := range texts {
		reqs[i] = geminiEmbedRequest{
			Model:   "models/" + p.model,
			Content: geminiTextContent{Parts: []geminiPart{{Text: t}}},
		}
	}
	payload, _ := json.Marshal(geminiRequest{Requests: reqs})

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

		url := p.endpoint + "/models/" + p.model + ":batchEmbedContents?key=" + p.apiKey
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gemini: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			var er geminiResponse
			_ = json.NewDecoder(resp.Body).Decode(&er)
			resp.Body.Close()
			msg := ""
			if er.Error != nil {
				msg = er.Error.Message
			}
			lastErr = fmt.Errorf("gemini: status %d: %s", resp.StatusCode, msg)
			if isRetryable(resp.StatusCode) {
				continue
			}
			return nil, lastErr
		}

		var r geminiResponse
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		out := make([][]float32, len(texts))
		for i, emb := range r.Embeddings {
			if i < len(out) {
				out[i] = emb.Values
			}
		}
		return out, nil
	}
	return nil, lastErr
}

func init() {
	Register("gemini", func(model string) (Provider, error) {
		if model == "" {
			model = "text-embedding-004"
		}
		dim := 768
		return &GeminiProvider{
			model:    model,
			dim:      dim,
			endpoint: getenvDefault("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta"),
			apiKey:   os.Getenv("GEMINI_API_KEY"),
			client:   &http.Client{Timeout: 30 * time.Second},
		}, nil
	})
}
