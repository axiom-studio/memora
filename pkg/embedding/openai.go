package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

func (p *OpenAIProvider) Name() string    { return "openai" }
func (p *OpenAIProvider) ModelID() string { return "openai:" + p.model }
func (p *OpenAIProvider) Dim() int        { return p.dim }

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
	payload, _ := json.Marshal(openAIRequest{Model: p.model, Input: texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var er openAIResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		msg := ""
		if er.Error != nil {
			msg = er.Error.Message
		}
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, msg)
	}
	var r openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	out := make([][]float32, len(r.Data))
	for _, d := range r.Data {
		if d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
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
