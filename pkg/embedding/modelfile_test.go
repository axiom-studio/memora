package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalModel_PinnedAndVerified (#4380): the default model URL names
// a commit, not a branch; a download is kept only with the pinned
// SHA-256 (an empty pin is refused); an on-disk model that does not
// match is refused before native code opens it.
func TestLocalModel_PinnedAndVerified(t *testing.T) {
	if strings.Contains(defaultModelURL, "/resolve/main/") || len(defaultModelSHA) != 64 || !strings.Contains(defaultModelURL, defaultModelRevision) {
		t.Fatalf("default model is not pinned: %s sha %q", defaultModelURL, defaultModelSHA)
	}
	body := []byte("pretend gguf bytes")
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	dir := t.TempDir()

	ok := filepath.Join(dir, "ok.gguf")
	if err := downloadModel(srv.URL, ok, good); err != nil {
		t.Fatalf("matching download: %v", err)
	}
	if err := verifyModel(ok, good); err != nil {
		t.Fatalf("verify matching file: %v", err)
	}
	bad := filepath.Join(dir, "bad.gguf")
	if err := downloadModel(srv.URL, bad, strings.Repeat("0", 64)); err == nil {
		t.Fatal("a download with the wrong sha256 was kept")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("a rejected download was left on disk")
	}
	if err := downloadModel(srv.URL, bad, ""); err == nil {
		t.Fatal("a download with no pinned sha256 was allowed")
	}
	if err := os.WriteFile(ok, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyModel(ok, good); err == nil {
		t.Fatal("a tampered model file verified")
	}
}
