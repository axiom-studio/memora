package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The default model is pinned to one Hugging Face commit (resolve/main
// is mutable) and to the SHA-256 of that file, which is checked on
// download and on every load: the GGUF is parsed by native code, so an
// unverified file is never opened (#4380).
const (
	defaultModelName     = "nomic-embed-text-v1.5.Q8_0.gguf"
	defaultModelRevision = "0188c9bf409793f810680a5a431e7b899c46104c"
	defaultModelURL      = "https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/" + defaultModelRevision + "/" + defaultModelName
	defaultModelSHA      = "3e24342164b3d94991ba9692fdc0dd08e3fd7362e0aacc396a9a5c54a544c3b7"
	defaultModelSize     = 146146432
)

// verifyModel checks a model file against its pinned SHA-256.
func verifyModel(path, wantSHA string) error {
	if wantSHA == "" {
		return fmt.Errorf("model %s has no pinned sha256; refusing to load it", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("model %s has sha256 %s, want %s; delete it to download the pinned model again", path, got, wantSHA)
	}
	return nil
}

// downloadModel fetches url to dest, keeping it only if its SHA-256 is
// wantSHA. An empty wantSHA is refused rather than skipped.
func downloadModel(url, dest, wantSHA string) error {
	if wantSHA == "" {
		return fmt.Errorf("no pinned sha256 for %s; refusing to download", url)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	h := sha256.New()
	w := io.MultiWriter(f, h)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, wantSHA)
	}

	return os.Rename(tmp, dest)
}
