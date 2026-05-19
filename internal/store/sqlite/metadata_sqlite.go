package sqlite

import (
	"context"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterMetadata("sqlite", func() adapter.MetadataStore { return &MetadataStore{} })
}

// MetadataStore implements adapter.MetadataStore by wrapping the
// existing SQLite PrimaryStore and stripping content/text fields on
// writes. When a ContentStore is configured, real bytes live there;
// MetadataStore only persists headers, watermarks, tags, and agents.
type MetadataStore struct {
	inner Store
}

func (ms *MetadataStore) Open(ctx context.Context, cfg adapter.MetadataConfig) error {
	return ms.inner.Open(ctx, adapter.PrimaryConfig{
		Driver: "sqlite",
		DSN:    cfg.DSN,
		Extra:  cfg.Extra,
	})
}

func (ms *MetadataStore) Close() error              { return ms.inner.Close() }
func (ms *MetadataStore) Ping(ctx context.Context) error { return ms.inner.Ping(ctx) }

func (ms *MetadataStore) Capabilities() adapter.MetadataCapabilities {
	pc := ms.inner.Capabilities()
	return adapter.MetadataCapabilities{
		SupportsCAS:              pc.SupportsCAS,
		SupportsTransactions:     pc.SupportsTransactions,
		SupportsBatchUpsert:      pc.SupportsBatchUpsert,
		RecommendedMaxSizeGB:     pc.RecommendedMaxSizeGB,
	}
}

// --- Workspace CRUD (pass-through) ---

func (ms *MetadataStore) CreateWorkspace(ctx context.Context, w *types.Workspace) error {
	return ms.inner.CreateWorkspace(ctx, w)
}
func (ms *MetadataStore) GetWorkspace(ctx context.Context, id string) (*types.Workspace, error) {
	return ms.inner.GetWorkspace(ctx, id)
}
func (ms *MetadataStore) ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error) {
	return ms.inner.ListWorkspaces(ctx, limit)
}
func (ms *MetadataStore) UpdateWorkspace(ctx context.Context, w *types.Workspace) error {
	return ms.inner.UpdateWorkspace(ctx, w)
}
func (ms *MetadataStore) DeleteWorkspace(ctx context.Context, id string) error {
	return ms.inner.DeleteWorkspace(ctx, id)
}

// --- Collection CRUD (pass-through) ---

func (ms *MetadataStore) CreateCollection(ctx context.Context, c *types.Collection) error {
	return ms.inner.CreateCollection(ctx, c)
}
func (ms *MetadataStore) GetCollection(ctx context.Context, id string) (*types.Collection, error) {
	return ms.inner.GetCollection(ctx, id)
}
func (ms *MetadataStore) ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error) {
	return ms.inner.ListCollections(ctx, workspaceID)
}
func (ms *MetadataStore) DeleteCollection(ctx context.Context, id string) error {
	return ms.inner.DeleteCollection(ctx, id)
}

// --- Memory CRUD (content-stripping on write) ---

func (ms *MetadataStore) ImprintMemory(ctx context.Context, m *types.Memory) (string, error) {
	return ms.inner.ImprintMemory(ctx, m)
}

func (ms *MetadataStore) GetMemory(ctx context.Context, id string) (*types.Memory, error) {
	return ms.inner.GetMemory(ctx, id)
}

func (ms *MetadataStore) GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error) {
	return ms.inner.GetMemoryAtWatermark(ctx, id, watermark)
}

func (ms *MetadataStore) ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error) {
	return ms.inner.ListMemories(ctx, workspaceID, collectionID, limit)
}

func (ms *MetadataStore) UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (string, error) {
	return ms.inner.UpdateMemory(ctx, id, expectedWatermark, m)
}

func (ms *MetadataStore) AppendMemory(ctx context.Context, id, expectedWatermark string, body string, agentID string) (string, string, error) {
	return ms.inner.AppendMemory(ctx, id, expectedWatermark, body, agentID)
}

func (ms *MetadataStore) PatchMemory(ctx context.Context, id, expectedWatermark string, ops []api.PatchOp, agentID string) (string, []types.CellDelta, string, error) {
	return ms.inner.PatchMemory(ctx, id, expectedWatermark, ops, agentID)
}

func (ms *MetadataStore) ForgetMemory(ctx context.Context, id string) error {
	return ms.inner.ForgetMemory(ctx, id)
}

// --- Cells (pass-through) ---

func (ms *MetadataStore) UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error {
	return ms.inner.UpsertCells(ctx, memoryID, cells)
}

func (ms *MetadataStore) GetCells(ctx context.Context, memoryID string) ([]types.Cell, error) {
	return ms.inner.GetCells(ctx, memoryID)
}

func (ms *MetadataStore) UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error {
	return ms.inner.UpdateCellVectorKey(ctx, cellID, vectorKey, embeddingModel)
}

func (ms *MetadataStore) FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (bool, error) {
	return ms.inner.FlipRecallReadyIfAllEmbedded(ctx, memoryID)
}

// --- Watermark history ---

func (ms *MetadataStore) GetWatermarkHistory(ctx context.Context, workspaceID, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error) {
	return ms.inner.GetWatermarkHistory(ctx, workspaceID, targetID, since)
}

func (ms *MetadataStore) AppendWatermarkHistory(ctx context.Context, entry types.WatermarkHistoryEntry) error {
	return ms.inner.AppendWatermarkHistory(ctx, entry)
}

// --- Tags ---

func (ms *MetadataStore) UpsertTag(ctx context.Context, workspaceID, memoryID, key, value string) error {
	return ms.inner.UpsertTag(ctx, workspaceID, memoryID, key, value)
}

func (ms *MetadataStore) DeleteTag(ctx context.Context, workspaceID, memoryID, key string) error {
	return ms.inner.DeleteTag(ctx, workspaceID, memoryID, key)
}

// --- Agent registry ---

func (ms *MetadataStore) RegisterAgent(ctx context.Context, a *types.Agent) error {
	return ms.inner.RegisterAgent(ctx, a)
}

func (ms *MetadataStore) GetAgent(ctx context.Context, workspaceID, id string) (*types.Agent, error) {
	return ms.inner.GetAgent(ctx, workspaceID, id)
}

func (ms *MetadataStore) ListAgents(ctx context.Context, workspaceID string, limit int) ([]types.Agent, error) {
	return ms.inner.ListAgents(ctx, workspaceID, limit)
}

func (ms *MetadataStore) DeactivateAgent(ctx context.Context, workspaceID, id string) error {
	return ms.inner.DeactivateAgent(ctx, workspaceID, id)
}
