package embedding

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// HashProvider is an offline, deterministic lexical embedder: signed
// feature hashing over lower-cased word tokens, with log-scaled term
// counts, L2-normalised. Texts sharing words get similar vectors. It
// captures no meaning beyond shared words, but it needs no network,
// model or native library, so vector search works on an air-gapped node
// and in tests.
type HashProvider struct {
	model string
	dim   int
}

const hashDim = 384

func (p *HashProvider) Name() string    { return "hash" }
func (p *HashProvider) ModelID() string { return "hash:" + p.model }
func (p *HashProvider) Dim() int        { return p.dim }

func (p *HashProvider) Capabilities() EmbeddingCapabilities {
	return EmbeddingCapabilities{
		SupportsBatch:        true,
		MaxBatchSize:         10000,
		ReturnsDeterministic: true,
		Quality:              "lexical",
	}
}

// Embed is O(total input length).
func (p *HashProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		counts := map[string]int{}
		for _, tok := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			counts[tok]++
		}
		v := make([]float32, p.dim)
		for tok, n := range counts {
			h := fnv.New64a()
			_, _ = h.Write([]byte(tok))
			sum := h.Sum64()
			w := float32(1 + math.Log(float64(n)))
			if sum>>63 == 1 { // signed hashing cancels collisions in expectation
				w = -w
			}
			v[sum%uint64(p.dim)] += w //nolint:gosec // G115: dim is validated to 8-4096 at construction
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			inv := float32(1 / math.Sqrt(norm))
			for j := range v {
				v[j] *= inv
			}
		}
		out[i] = v
	}
	return out, nil
}

func init() {
	// "hash:default" is 384-dimensional; "hash:<n>" sets the dimension
	// to n (8–4096), to match a vector store sized for another model.
	Register("hash", func(model string) (Provider, error) {
		if model == "" {
			model = "default"
		}
		dim := hashDim
		if n, err := strconv.Atoi(model); err == nil {
			if n < 8 || n > 4096 {
				return nil, fmt.Errorf("hash: dimension %d out of range 8-4096", n)
			}
			dim = n
		}
		return &HashProvider{model: model, dim: dim}, nil
	})
}
