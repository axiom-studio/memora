package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterMetadata("postgres", func() adapter.MetadataStore { return &MetadataStore{} })
}

// MetadataStore is the Postgres MetadataStore adapter. Currently a
// deferred stub (returns ErrCapability) — full implementation is
// tracked for v0.5. The type is registered so deployers who set
// --metadata-driver=postgres get a clear error instead of a panic.
type MetadataStore struct{}

func (s *MetadataStore) Open(_ context.Context, _ adapter.MetadataConfig) error {
	return fmt.Errorf("%w: postgres MetadataStore is not yet wired in OSS v0.1 (tracked for v0.5; use --metadata-driver=sqlite for now)", types.ErrCapability)
}

func (s *MetadataStore) Close() error                                          { return nil }
func (s *MetadataStore) Ping(_ context.Context) error                          { return types.ErrCapability }
func (s *MetadataStore) Capabilities() adapter.MetadataCapabilities            { return adapter.MetadataCapabilities{} }
func (s *MetadataStore) CreateWorkspace(context.Context, *types.Workspace) error { return types.ErrCapability }
func (s *MetadataStore) GetWorkspace(context.Context, string) (*types.Workspace, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) ListWorkspaces(context.Context, int) ([]types.Workspace, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) UpdateWorkspace(context.Context, *types.Workspace) error { return types.ErrCapability }
func (s *MetadataStore) DeleteWorkspace(context.Context, string) error           { return types.ErrCapability }
func (s *MetadataStore) CreateCollection(context.Context, *types.Collection) error { return types.ErrCapability }
func (s *MetadataStore) GetCollection(context.Context, string) (*types.Collection, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) ListCollections(context.Context, string) ([]types.Collection, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) DeleteCollection(context.Context, string) error { return types.ErrCapability }
func (s *MetadataStore) ImprintMemory(context.Context, *types.Memory) (string, error) {
	return "", types.ErrCapability
}
func (s *MetadataStore) GetMemory(context.Context, string) (*types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) GetMemoryAtWatermark(context.Context, string, string) (*types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) ListMemories(context.Context, string, string, int) ([]types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) UpdateMemory(context.Context, string, string, *types.Memory) (string, error) {
	return "", types.ErrCapability
}
func (s *MetadataStore) AppendMemory(context.Context, string, string, string, string) (string, string, error) {
	return "", "", types.ErrCapability
}
func (s *MetadataStore) PatchMemory(context.Context, string, string, []api.PatchOp, string) (string, []types.CellDelta, string, error) {
	return "", nil, "", types.ErrCapability
}
func (s *MetadataStore) ForgetMemory(context.Context, string) error                      { return types.ErrCapability }
func (s *MetadataStore) UpsertCells(context.Context, string, []types.Cell) error         { return types.ErrCapability }
func (s *MetadataStore) GetCells(context.Context, string) ([]types.Cell, error)          { return nil, types.ErrCapability }
func (s *MetadataStore) UpdateCellVectorKey(context.Context, string, string, string) error { return types.ErrCapability }
func (s *MetadataStore) FlipRecallReadyIfAllEmbedded(context.Context, string) (bool, error) {
	return false, types.ErrCapability
}
func (s *MetadataStore) GetWatermarkHistory(context.Context, string, string, time.Time) ([]types.WatermarkHistoryEntry, error) {
	return nil, types.ErrCapability
}
func (s *MetadataStore) AppendWatermarkHistory(context.Context, types.WatermarkHistoryEntry) error {
	return types.ErrCapability
}
func (s *MetadataStore) UpsertTag(context.Context, string, string, string, string) error { return types.ErrCapability }
func (s *MetadataStore) DeleteTag(context.Context, string, string, string) error         { return types.ErrCapability }
func (s *MetadataStore) RegisterAgent(context.Context, *types.Agent) error               { return types.ErrCapability }
func (s *MetadataStore) GetAgent(context.Context, string, string) (*types.Agent, error)  { return nil, types.ErrCapability }
func (s *MetadataStore) ListAgents(context.Context, string, int) ([]types.Agent, error)  { return nil, types.ErrCapability }
func (s *MetadataStore) DeactivateAgent(context.Context, string, string) error           { return types.ErrCapability }
