package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func fixtureGemini(t *testing.T, handler http.HandlerFunc) *GeminiProvider {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &GeminiProvider{
		model:    "text-embedding-004",
		dim:      2,
		endpoint: ts.URL,
		apiKey:   "test-key",
		client:   ts.Client(),
	}
}

func TestGeminiProvider_Success(t *testing.T) {
	p := fixtureGemini(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "key=test-key") {
			t.Errorf("missing api key in query: %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(geminiResponse{
			Embeddings: []struct {
				Values []float32 `json:"values"`
			}{
				{Values: []float32{0.7, 0.8}},
				{Values: []float32{0.9, 1.0}},
			},
		})
	})

	vs, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vs))
	}
	if vs[0][0] != 0.7 || vs[1][0] != 0.9 {
		t.Errorf("unexpected vectors: %v", vs)
	}
}

func TestGeminiProvider_NoAPIKey(t *testing.T) {
	p := &GeminiProvider{model: "test", dim: 2, apiKey: ""}
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected ErrNoAPIKey")
	}
}

func TestGeminiProvider_5xxRetries(t *testing.T) {
	var calls atomic.Int32
	p := fixtureGemini(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(geminiResponse{
			Embeddings: []struct {
				Values []float32 `json:"values"`
			}{
				{Values: []float32{0.1, 0.2}},
			},
		})
	})

	vs, err := p.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("should succeed after retries: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1, got %d", len(vs))
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestGeminiProvider_Capabilities(t *testing.T) {
	p := &GeminiProvider{model: "text-embedding-004", dim: 768}
	caps := p.Capabilities()
	if caps.MaxBatchSize != geminiMaxBatch {
		t.Errorf("max batch = %d, want %d", caps.MaxBatchSize, geminiMaxBatch)
	}
	if caps.Quality != "production" {
		t.Errorf("quality = %q", caps.Quality)
	}
}
