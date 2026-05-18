// Package postgres is the scale-up Memora PrimaryStore adapter. v0.1
// of OSS ships the SQLite adapter as the default zero-dependency
// triple; this package is a deferred-but-registered stub so deployers
// who set --primary-driver=postgres get a clear error pointing at the
// v0.5 milestone rather than an "unknown driver" panic.
//
// Full implementation tracking: see OSS roadmap milestone v0.5 in
// PRD #297 §12. The schema in migrate/postgres/ mirrors the SQLite
// schema with JSONB/TIMESTAMPTZ/BRIN tuning.
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
	adapter.RegisterPrimary("postgres", func() adapter.PrimaryStore { return &Store{} })
}

// Store is the (deferred) Postgres PrimaryStore.
type Store struct{}

// Open returns a clear capability error so the deployer can pick
// `sqlite` or wait for v0.5.
func (s *Store) Open(_ context.Context, _ adapter.PrimaryConfig) error {
	return fmt.Errorf("%w: postgres PrimaryStore is not yet wired in OSS v0.1 (tracked for v0.5; use --primary-driver=sqlite for now)", types.ErrCapability)
}

func (s *Store) Close() error                                 { return nil }
func (s *Store) Ping(_ context.Context) error                 { return types.ErrCapability }
func (s *Store) Capabilities() adapter.PrimaryCapabilities    { return adapter.PrimaryCapabilities{} }

// The remaining PrimaryStore methods all return ErrCapability. They
// keep the type satisfying the interface so the registry compile-
// checks pass.

func (s *Store) CreateWorkspace(context.Context, *types.Workspace) error            { return types.ErrCapability }
func (s *Store) GetWorkspace(context.Context, string) (*types.Workspace, error)     { return nil, types.ErrCapability }
func (s *Store) ListWorkspaces(context.Context, int) ([]types.Workspace, error)     { return nil, types.ErrCapability }
func (s *Store) UpdateWorkspace(context.Context, *types.Workspace) error            { return types.ErrCapability }
func (s *Store) DeleteWorkspace(context.Context, string) error                      { return types.ErrCapability }
func (s *Store) CreateCollection(context.Context, *types.Collection) error          { return types.ErrCapability }
func (s *Store) GetCollection(context.Context, string) (*types.Collection, error)   { return nil, types.ErrCapability }
func (s *Store) ListCollections(context.Context, string) ([]types.Collection, error) {
	return nil, types.ErrCapability
}
func (s *Store) DeleteCollection(context.Context, string) error { return types.ErrCapability }
func (s *Store) ImprintMemory(context.Context, *types.Memory) (string, error) {
	return "", types.ErrCapability
}
func (s *Store) GetMemory(context.Context, string) (*types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *Store) GetMemoryAtWatermark(context.Context, string, string) (*types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *Store) ListMemories(context.Context, string, string, int) ([]types.Memory, error) {
	return nil, types.ErrCapability
}
func (s *Store) UpdateMemory(context.Context, string, string, *types.Memory) (string, error) {
	return "", types.ErrCapability
}
func (s *Store) AppendMemory(context.Context, string, string, string, string) (string, string, error) {
	return "", "", types.ErrCapability
}
func (s *Store) PatchMemory(context.Context, string, string, []api.PatchOp, string) (string, []types.CellDelta, string, error) {
	return "", nil, "", types.ErrCapability
}
func (s *Store) ForgetMemory(context.Context, string) error                        { return types.ErrCapability }
func (s *Store) UpsertCells(context.Context, string, []types.Cell) error           { return types.ErrCapability }
func (s *Store) GetCells(context.Context, string) ([]types.Cell, error)            { return nil, types.ErrCapability }
func (s *Store) UpdateCellVectorKey(context.Context, string, string, string) error { return types.ErrCapability }
func (s *Store) GetWatermarkHistory(context.Context, string, time.Time) ([]types.WatermarkHistoryEntry, error) {
	return nil, types.ErrCapability
}
func (s *Store) AppendWatermarkHistory(context.Context, types.WatermarkHistoryEntry) error {
	return types.ErrCapability
}
func (s *Store) UpsertTag(context.Context, string, string, string) error { return types.ErrCapability }
func (s *Store) DeleteTag(context.Context, string, string) error         { return types.ErrCapability }
func (s *Store) RegisterAgent(context.Context, *types.Agent) error       { return types.ErrCapability }
func (s *Store) GetAgent(context.Context, string) (*types.Agent, error)  { return nil, types.ErrCapability }
func (s *Store) ListAgents(context.Context, string) ([]types.Agent, error) {
	return nil, types.ErrCapability
}
func (s *Store) DeactivateAgent(context.Context, string) error { return types.ErrCapability }
func (s *Store) GraphLink(context.Context, types.Edge) (types.Edge, error) {
	return types.Edge{}, types.ErrCapability
}
func (s *Store) GraphUnlink(context.Context, string, string) error { return types.ErrCapability }
func (s *Store) GraphLinkBatch(context.Context, []types.Edge) ([]adapter.LinkResult, error) {
	return nil, types.ErrCapability
}
func (s *Store) GraphCascadeForget(context.Context, string, string) (int, error) {
	return 0, types.ErrCapability
}
func (s *Store) GraphNeighbors(context.Context, string, string, adapter.NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error) {
	return nil, nil, types.ErrCapability
}
func (s *Store) GraphTraverse(context.Context, string, string, adapter.TraverseOpts) (adapter.TraverseResult, error) {
	return adapter.TraverseResult{}, types.ErrCapability
}
func (s *Store) GraphStats(context.Context, string) (int, map[string]int, error) {
	return 0, nil, types.ErrCapability
}
