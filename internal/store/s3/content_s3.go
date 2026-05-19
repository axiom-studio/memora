//go:build cloud

// Package s3 is the S3-backed ContentStore for cloud deployments.
// It stores content using a workspace-scoped key prefix:
//
//	s3://<bucket>/<prefix>/<workspace_id>/<memory_id>.txt       (memory content)
//	s3://<bucket>/<prefix>/<workspace_id>/<memory_id>/<cell_id>.txt  (cell content)
//
// Requires the `cloud` build tag. Uses aws-sdk-go-v2 (Apache-2.0).
// Currently a registered stub — full implementation is tracked for
// the cloud milestone.
package s3

import (
	"context"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterContent("s3", func() adapter.ContentStore { return &ContentStore{} })
}

// ContentStore is the S3-backed ContentStore. This is a registered
// stub — the full implementation (with aws-sdk-go-v2) is tracked for
// the cloud milestone. The type satisfies the interface so
// --content-driver=s3 gives a clear error rather than a panic.
type ContentStore struct{}

func (c *ContentStore) Open(_ context.Context, _ adapter.ContentConfig) error {
	return fmt.Errorf("%w: S3 ContentStore is not yet wired (cloud milestone; use --content-driver=sqlite or file for now)", types.ErrCapability)
}

func (c *ContentStore) Close() error                                   { return nil }
func (c *ContentStore) Ping(_ context.Context) error                   { return types.ErrCapability }
func (c *ContentStore) Capabilities() adapter.ContentCapabilities {
	return adapter.ContentCapabilities{
		SupportsConditionalPut:      true,
		SupportsBatchGet:            false,
		RecommendedMaxObjectMB:      100,
		SupportsServerSideRedaction: false,
		DurabilityClass:             "11-nines",
	}
}
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
func (c *ContentStore) ListMemoryIDsOlderThan(context.Context, string, time.Time) ([]string, error) {
	return nil, types.ErrCapability
}
