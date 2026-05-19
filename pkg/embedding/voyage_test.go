package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func fixtureVoyage(t *testing.T, handler http.HandlerFunc) *VoyageProvider {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &VoyageProvider{
		model:    "voyage-3",
		dim:      2,
		endpoint: ts.URL,
		apiKey:   "test-key",
		client:   ts.Client(),
	}
}

func TestVoyageProvider_Success(t *testing.T) {
	p := fixtureVoyage(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("wrong auth: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(voyageResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.5, 0.6}, Index: 0},
			},
		})
	})

	vs, err := p.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0][0] != 0.5 {
		t.Errorf("unexpected: %v", vs)
	}
}

func TestVoyageProvider_NoAPIKey(t *testing.T) {
	p := &VoyageProvider{model: "test", dim: 2, apiKey: ""}
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected ErrNoAPIKey")
	}
}

func TestVoyageProvider_5xxRetries(t *testing.T) {
	var calls atomic.Int32
	p := fixtureVoyage(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(voyageResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2}, Index: 0},
			},
		})
	})

	vs, err := p.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("should have succeeded after retries: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1 vector, got %d", len(vs))
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestVoyageProvider_Capabilities(t *testing.T) {
	p := &VoyageProvider{model: "voyage-3", dim: 1024}
	caps := p.Capabilities()
	if caps.MaxBatchSize != voyageMaxBatch {
		t.Errorf("max batch = %d, want %d", caps.MaxBatchSize, voyageMaxBatch)
	}
	if caps.Quality != "production" {
		t.Errorf("quality = %q", caps.Quality)
	}
}
