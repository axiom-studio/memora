// Package orphangc provides a background goroutine that reclaims
// orphaned content blobs. An orphan is a ContentStore entry whose
// corresponding MetadataStore memory row either doesn't exist or was
// soft-deleted (forgotten). Orphans arise from the content-first
// write ordering (doc #301 §7.3 option a): if the content write
// succeeds but the metadata write fails, the blob is stranded.
package orphangc

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// LedgerAppender is the minimal interface the sweeper needs to log
// audit entries. Satisfied by *service.Service.
type LedgerAppender interface {
	AppendLedger(ctx context.Context, e api.LedgerEntry) string
}

// Config controls the sweeper's behavior.
type Config struct {
	Interval time.Duration // tick interval (default 5m)
	MinAge   time.Duration // minimum age before an orphan is eligible (default 1h)
}

// Sweeper is the background orphan-content GC.
type Sweeper struct {
	metadata adapter.MetadataStore
	content  adapter.ContentStore
	ledger   LedgerAppender
	logger   *slog.Logger
	cfg      Config
	now      func() time.Time

	totalDeleted atomic.Int64
	lastDeleted  atomic.Int64
	cancel       context.CancelFunc
}

// New creates a Sweeper but does not start it. Call Start to begin.
func New(metadata adapter.MetadataStore, content adapter.ContentStore, ledger LedgerAppender, logger *slog.Logger, cfg Config) *Sweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.MinAge <= 0 {
		cfg.MinAge = 1 * time.Hour
	}
	return &Sweeper{
		metadata: metadata,
		content:  content,
		ledger:   ledger,
		logger:   logger,
		cfg:      cfg,
		now:      time.Now,
	}
}

// Start begins the background sweep loop. Safe to call once.
func (s *Sweeper) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go s.loop(ctx)
}

// Stop signals the sweep loop to exit.
func (s *Sweeper) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// TotalDeleted returns the cumulative count of orphans deleted.
func (s *Sweeper) TotalDeleted() int64 { return s.totalDeleted.Load() }

// LastDeleted returns orphans deleted in the most recent pass.
func (s *Sweeper) LastDeleted() int64 { return s.lastDeleted.Load() }

func (s *Sweeper) loop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep runs one GC pass. For each workspace (paginated) it enumerates
// content keys older than MinAge via ContentStore.ListMemoryIDsOlderThan,
// cross-references each against MetadataStore.GetMemory, and deletes
// content for memories that are missing or forgotten (ErrNotFound).
func (s *Sweeper) sweep(ctx context.Context) {
	s.logger.Info("orphan_gc_pass_start")

	if s.metadata == nil || s.content == nil {
		s.lastDeleted.Store(0)
		s.logger.Info("orphan_gc_pass_skip", "reason", "nil metadata or content store")
		return
	}

	cutoff := s.now().Add(-s.cfg.MinAge)

	var scanned, deleted int
	cursor := ""
	const pageSize = 500
	for {
		workspaces, nextCursor, err := s.metadata.ListWorkspacesPaged(ctx, cursor, pageSize)
		if err != nil {
			s.logger.Warn("orphan_gc_list_workspaces_error", "err", err)
			break
		}

		for _, ws := range workspaces {
			memIDs, err := s.content.ListMemoryIDsOlderThan(ctx, ws.ID, cutoff)
			if err != nil {
				s.logger.Warn("orphan_gc_list_keys_error", "workspace", ws.ID, "err", err)
				continue
			}
			for _, memID := range memIDs {
				scanned++
				_, err := s.metadata.GetMemory(ctx, memID)
				if err == nil {
					deleted += s.sweepCellOrphans(ctx, ws.ID, memID)
					continue
				}
				if err := s.content.DeleteAllForMemory(ctx, ws.ID, memID); err != nil {
					s.logger.Warn("orphan_gc_delete_error", "workspace", ws.ID, "memory", memID, "err", err)
					continue
				}
				deleted++
				s.logger.Info("orphan_gc_reclaimed", "workspace", ws.ID, "memory", memID)
			}
		}

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	s.lastDeleted.Store(int64(deleted))
	s.totalDeleted.Add(int64(deleted))
	s.logger.Info("orphan_gc_pass_done", "scanned", scanned, "deleted", deleted)

	if s.ledger != nil {
		s.ledger.AppendLedger(ctx, api.LedgerEntry{
			Op:       "orphan_gc",
			Metadata: map[string]any{"scanned": scanned, "deleted": deleted},
		})
	}
}

// sweepCellOrphans deletes content-layer cells that have no matching
// metadata cell. Returns the number of orphaned cells deleted.
func (s *Sweeper) sweepCellOrphans(ctx context.Context, workspaceID, memoryID string) int {
	contentCellIDs, err := s.content.ListCellIDs(ctx, workspaceID, memoryID)
	if err != nil {
		s.logger.Warn("orphan_gc_list_cells_error", "workspace", workspaceID, "memory", memoryID, "err", err)
		return 0
	}
	if len(contentCellIDs) == 0 {
		return 0
	}
	metaCells, err := s.metadata.GetCells(ctx, memoryID)
	if err != nil {
		s.logger.Warn("orphan_gc_get_cells_error", "memory", memoryID, "err", err)
		return 0
	}
	live := make(map[string]struct{}, len(metaCells))
	for _, c := range metaCells {
		live[c.CellID] = struct{}{}
	}
	var deleted int
	for _, cellID := range contentCellIDs {
		if _, ok := live[cellID]; ok {
			continue
		}
		if err := s.content.DeleteCellContent(ctx, workspaceID, memoryID, cellID); err != nil {
			s.logger.Warn("orphan_gc_delete_cell_error", "workspace", workspaceID, "memory", memoryID, "cell", cellID, "err", err)
			continue
		}
		deleted++
		s.logger.Info("orphan_gc_cell_reclaimed", "workspace", workspaceID, "memory", memoryID, "cell", cellID)
	}
	return deleted
}
