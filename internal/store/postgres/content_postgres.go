package postgres

import (
	"context"
	"fmt"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterContent("postgres", func() adapter.ContentStore { return &ContentStore{} })
}

// ContentStore is the Postgres-backed ContentStore. Currently a
// deferred stub (returns ErrCapability) — full implementation is
// tracked for v0.5, alongside the Postgres MetadataStore. The type is
// registered so deployers who set --content-driver=postgres get a
// clear error instead of a panic.
//
// Schema (for the v0.5 migration):
//
//	CREATE TABLE memora_content (
//	    workspace_id TEXT NOT NULL,
//	    memory_id    TEXT NOT NULL,
//	    cell_id      TEXT NOT NULL DEFAULT '',
//	    content_md5  TEXT NOT NULL DEFAULT '',
//	    content      TEXT NOT NULL,
//	    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    PRIMARY KEY (workspace_id, memory_id, cell_id)
//	);
//	CREATE INDEX idx_content_memory ON memora_content(workspace_id, memory_id);
type ContentStore struct{}

func (c *ContentStore) Open(_ context.Context, _ adapter.ContentConfig) error {
	return fmt.Errorf("%w: postgres ContentStore is not yet wired in OSS v0.1 (tracked for v0.5; use --content-driver=sqlite or file for now)", types.ErrCapability)
}

func (c *ContentStore) Close() error                                   { return nil }
func (c *ContentStore) Ping(_ context.Context) error                   { return types.ErrCapability }
func (c *ContentStore) Capabilities() adapter.ContentCapabilities      { return adapter.ContentCapabilities{} }
func (c *ContentStore) PutMemoryContent(context.Context, string, string, string, string) error {
	return types.ErrCapability
}
func (c *ContentStore) GetMemoryContent(context.Context, string, string) (string, error) {
	return "", types.ErrCapability
}
func (c *ContentStore) DeleteMemoryContent(context.Context, string, string) error {
	return types.ErrCapability
}
func (c *ContentStore) PutCellContent(context.Context, string, string, string, string, string) error {
	return types.ErrCapability
}
func (c *ContentStore) GetCellContent(context.Context, string, string, string) (string, error) {
	return "", types.ErrCapability
}
func (c *ContentStore) GetCellContentBatch(context.Context, string, string, []string) (map[string]string, error) {
	return nil, types.ErrCapability
}
func (c *ContentStore) DeleteCellContent(context.Context, string, string, string) error {
	return types.ErrCapability
}
func (c *ContentStore) DeleteAllForMemory(context.Context, string, string) error {
	return types.ErrCapability
}
func (c *ContentStore) ListMemoryIDs(context.Context, string) ([]string, error) {
	return nil, types.ErrCapability
}
