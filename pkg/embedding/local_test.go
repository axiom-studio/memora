//go:build local_embed

package embedding

import (
	"os"
	"testing"
)

func TestLocalProvider_Registration(t *testing.T) {
	p, err := Open("local:nomic-embed-text-v1.5")
	if err != nil {
		t.Fatalf("Open(local:nomic-embed-text-v1.5): %v", err)
	}
	if p.Name() != "local" {
		t.Fatalf("Name() = %q, want %q", p.Name(), "local")
	}
	if p.ModelID() != "local:nomic-embed-text-v1.5" {
		t.Fatalf("ModelID() = %q, want %q", p.ModelID(), "local:nomic-embed-text-v1.5")
	}
	if p.Dim() != 768 {
		t.Fatalf("Dim() = %d, want 768", p.Dim())
	}
}

func TestLocalProvider_DefaultModel(t *testing.T) {
	p, err := Open("local:")
	if err != nil {
		t.Fatalf("Open(local:): %v", err)
	}
	if p.ModelID() != "local:nomic-embed-text-v1.5" {
		t.Fatalf("ModelID() = %q, want default model", p.ModelID())
	}
}

func TestLocalProvider_Capabilities(t *testing.T) {
	p, err := Open("local:")
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if !caps.SupportsBatch {
		t.Fatal("expected SupportsBatch=true")
	}
	if caps.MaxBatchSize != 64 {
		t.Fatalf("MaxBatchSize = %d, want 64", caps.MaxBatchSize)
	}
	if caps.MaxInputTokens != 8192 {
		t.Fatalf("MaxInputTokens = %d, want 8192", caps.MaxInputTokens)
	}
	if caps.Quality != "production" {
		t.Fatalf("Quality = %q, want %q", caps.Quality, "production")
	}
}

func TestLocalProvider_ModelDirFromEnv(t *testing.T) {
	t.Setenv("MEMORA_MODEL_DIR", "/tmp/test-memora-models")
	dir := defaultModelDir()
	if dir != "/tmp/test-memora-models" {
		t.Fatalf("defaultModelDir() = %q, want /tmp/test-memora-models", dir)
	}
}

func TestLocalProvider_DefaultModelDir(t *testing.T) {
	t.Setenv("MEMORA_MODEL_DIR", "")
	dir := defaultModelDir()
	home, _ := os.UserHomeDir()
	want := home + "/.memora/models"
	if dir != want {
		t.Fatalf("defaultModelDir() = %q, want %q", dir, want)
	}
}

// TestLocalProvider_EmbedIntegration requires both libllama_go and the model
// file. Set MEMORA_LOCAL_MODEL_PATH to run:
//
//	MEMORA_LOCAL_MODEL_PATH=/path/to/nomic-embed-text-v1.5.Q8_0.gguf \
//	  go test -tags local_embed -run TestLocalProvider_EmbedIntegration ./pkg/embedding/
func TestLocalProvider_EmbedIntegration(t *testing.T) {
	modelPath := os.Getenv("MEMORA_LOCAL_MODEL_PATH")
	if modelPath == "" {
		t.Skip("MEMORA_LOCAL_MODEL_PATH not set; skipping local embedding integration test")
	}

	tmpDir := t.TempDir()
	dest := tmpDir + "/" + defaultModelName
	if err := os.Symlink(modelPath, dest); err != nil {
		t.Fatalf("symlink model: %v", err)
	}

	lp := &LocalProvider{
		model:    "nomic-embed-text-v1.5",
		modelDir: tmpDir,
	}
	defer lp.Close()

	vecs, err := lp.Embed(t.Context(), []string{
		"The quick brown fox jumps over the lazy dog.",
		"Machine learning is a subset of artificial intelligence.",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
	for i, vec := range vecs {
		if len(vec) != 768 {
			t.Fatalf("vec[%d] dim = %d, want 768", i, len(vec))
		}
		var sumSq float32
		for _, v := range vec {
			sumSq += v * v
		}
		if sumSq < 0.1 {
			t.Fatalf("vec[%d] appears to be zero vector", i)
		}
	}
}
