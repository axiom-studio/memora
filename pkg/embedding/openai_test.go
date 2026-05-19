package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func fixtureOpenAI(t *testing.T, handler http.HandlerFunc) *OpenAIProvider {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &OpenAIProvider{
		model:    "text-embedding-3-small",
		dim:      2,
		endpoint: ts.URL,
		apiKey:   "test-key",
		client:   ts.Client(),
	}
}

func TestOpenAIProvider_Success(t *testing.T) {
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("wrong auth: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong content-type: %s", r.Header.Get("Content-Type"))
		}
		var req openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		resp := openAIResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2}, Index: 0},
				{Embedding: []float32{0.3, 0.4}, Index: 1},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	vs, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vs))
	}
	if vs[0][0] != 0.1 || vs[0][1] != 0.2 {
		t.Errorf("vec[0] = %v, want [0.1 0.2]", vs[0])
	}
	if vs[1][0] != 0.3 || vs[1][1] != 0.4 {
		t.Errorf("vec[1] = %v, want [0.3 0.4]", vs[1])
	}
}

func TestOpenAIProvider_OutOfOrderIndex(t *testing.T) {
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.3, 0.4}, Index: 1},
				{Embedding: []float32{0.1, 0.2}, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	vs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if vs[0][0] != 0.1 {
		t.Errorf("index reordering failed: vs[0] = %v", vs[0])
	}
	if vs[1][0] != 0.3 {
		t.Errorf("index reordering failed: vs[1] = %v", vs[1])
	}
}

func TestOpenAIProvider_NoAPIKey(t *testing.T) {
	p := &OpenAIProvider{model: "test", dim: 2, endpoint: "http://unused", apiKey: ""}
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected ErrNoAPIKey")
	}
}

func TestOpenAIProvider_4xxTerminal(t *testing.T) {
	var calls atomic.Int32
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "invalid api key"},
		})
	})

	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("4xx should not retry: got %d calls", calls.Load())
	}
}

func TestOpenAIProvider_5xxRetries(t *testing.T) {
	var calls atomic.Int32
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"message": "overloaded"},
			})
			return
		}
		resp := openAIResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2}, Index: 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	vs, err := p.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("should have succeeded after retries: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1 vector, got %d", len(vs))
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", calls.Load())
	}
}

func TestOpenAIProvider_429ExhaustsRetries(t *testing.T) {
	var calls atomic.Int32
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "rate limited"},
		})
	})

	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestOpenAIProvider_MalformedJSON(t *testing.T) {
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid json`))
	})

	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOpenAIProvider_BatchClamping(t *testing.T) {
	var batchSizes []int
	p := fixtureOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		var req openAIRequest
		json.NewDecoder(r.Body).Decode(&req)
		batchSizes = append(batchSizes, len(req.Input))

		data := make([]struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}, len(req.Input))
		for i := range data {
			data[i] = struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{Embedding: []float32{0.1, 0.2}, Index: i}
		}
		json.NewEncoder(w).Encode(openAIResponse{Data: data})
	})

	texts := make([]string, openAIMaxBatch+100)
	for i := range texts {
		texts[i] = "text"
	}
	vs, err := p.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != len(texts) {
		t.Fatalf("want %d vectors, got %d", len(texts), len(vs))
	}
	if len(batchSizes) != 2 {
		t.Fatalf("want 2 batches, got %d", len(batchSizes))
	}
	if batchSizes[0] != openAIMaxBatch {
		t.Errorf("batch[0] size = %d, want %d", batchSizes[0], openAIMaxBatch)
	}
	if batchSizes[1] != 100 {
		t.Errorf("batch[1] size = %d, want 100", batchSizes[1])
	}
}

func TestOpenAIProvider_Capabilities(t *testing.T) {
	p := fixtureOpenAI(t, nil)
	caps := p.Capabilities()
	if !caps.SupportsBatch {
		t.Error("should support batch")
	}
	if caps.MaxBatchSize != openAIMaxBatch {
		t.Errorf("max batch = %d, want %d", caps.MaxBatchSize, openAIMaxBatch)
	}
	if caps.Quality != "production" {
		t.Errorf("quality = %q, want production", caps.Quality)
	}
}

func TestOpenFromEnv_DefaultsToNoop(t *testing.T) {
	t.Setenv("MEMORA_EMBEDDING_MODEL", "")
	p, err := OpenFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "noop" {
		t.Errorf("default should be noop, got %s", p.Name())
	}
}
