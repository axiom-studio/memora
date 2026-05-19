package federation

import (
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
)

const MaxFederationHops = 2

func ParseFederationPath(header string) []string {
	if header == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func CheckLoop(path []string, localID string) error {
	for _, id := range path {
		if id == localID {
			return types.ErrFederationLoop
		}
	}
	if len(path) >= MaxFederationHops {
		return types.ErrFederationLoop
	}
	return nil
}

func AppendToPath(existing string, localID string) string {
	if existing == "" {
		return localID
	}
	return existing + "," + localID
}
