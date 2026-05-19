package adapter

import (
	"fmt"
	"log/slog"

	"github.com/axiom-studio/memora/pkg/types"
)

// ValidateCompatibility checks that the configured adapter capabilities
// satisfy Memora's minimum requirements. Called at boot after all
// stores are opened. Returns an error for fatal misconfigurations;
// logs warnings for non-fatal ones.
func ValidateCompatibility(logger *slog.Logger, metadata MetadataCapabilities, content *ContentCapabilities, graph *GraphCapabilities) error {
	if !metadata.SupportsCAS {
		return fmt.Errorf("%w: MetadataStore must support CAS (compare-and-swap) — the watermark moat depends on it", types.ErrCapability)
	}

	if content != nil {
		if !content.SupportsConditionalPut && metadata.SupportsCAS {
			logger.Warn("ContentStore does not support conditional put — CAS semantics are metadata-only",
				"metadata_cas", true, "content_conditional_put", false)
		}
	}

	if graph != nil {
		if graph.MaxDepth == 0 {
			logger.Warn("GraphStore reports MaxDepth=0 — traversal will be a no-op")
		}
	}

	return nil
}
