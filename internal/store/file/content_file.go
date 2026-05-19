// Package file implements a filesystem-backed ContentStore. Content is
// stored as individual files under <root>/<workspace_id>/<memory_id>/
// with 0o600 permissions.
package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

func init() {
	adapter.RegisterContent("file", func() adapter.ContentStore { return &ContentStore{} })
}

// ContentStore stores memory and cell content as files on disk.
// Layout:
//
//	<root>/<workspace_id>/<memory_id>/memory.txt    — memory content
//	<root>/<workspace_id>/<memory_id>/<cell_id>.txt — cell content
type ContentStore struct {
	root string
}

func (c *ContentStore) Open(_ context.Context, cfg adapter.ContentConfig) error {
	if cfg.DSN == "" {
		return errors.New("file content: DSN (root directory) required")
	}
	c.root = cfg.DSN
	return os.MkdirAll(c.root, 0o700)
}

func (c *ContentStore) Close() error { return nil }

func (c *ContentStore) Ping(_ context.Context) error {
	_, err := os.Stat(c.root)
	return err
}

func (c *ContentStore) Capabilities() adapter.ContentCapabilities {
	return adapter.ContentCapabilities{
		SupportsConditionalPut: false,
		SupportsBatchGet:       true,
		RecommendedMaxObjectMB: 100,
		DurabilityClass:        "single-disk",
	}
}

func (c *ContentStore) PutMemoryContent(_ context.Context, workspaceID, memoryID, _, content string) error {
	dir := c.memoryDir(workspaceID, memoryID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "memory.txt"), []byte(content), 0o600)
}

func (c *ContentStore) GetMemoryContent(_ context.Context, workspaceID, memoryID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(c.memoryDir(workspaceID, memoryID), "memory.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", types.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *ContentStore) DeleteMemoryContent(_ context.Context, workspaceID, memoryID string) error {
	p := filepath.Join(c.memoryDir(workspaceID, memoryID), "memory.txt")
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) PutCellContent(_ context.Context, workspaceID, memoryID, cellID, _, text string) error {
	dir := c.memoryDir(workspaceID, memoryID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cellID+".txt"), []byte(text), 0o600)
}

func (c *ContentStore) GetCellContent(_ context.Context, workspaceID, memoryID, cellID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(c.memoryDir(workspaceID, memoryID), cellID+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", types.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *ContentStore) GetCellContentBatch(_ context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(cellIDs))
	dir := c.memoryDir(workspaceID, memoryID)
	for _, id := range cellIDs {
		b, err := os.ReadFile(filepath.Join(dir, id+".txt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = string(b)
	}
	return out, nil
}

func (c *ContentStore) DeleteCellContent(_ context.Context, workspaceID, memoryID, cellID string) error {
	p := filepath.Join(c.memoryDir(workspaceID, memoryID), cellID+".txt")
	err := os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) DeleteAllForMemory(_ context.Context, workspaceID, memoryID string) error {
	dir := c.memoryDir(workspaceID, memoryID)
	err := os.RemoveAll(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) ListMemoryIDs(_ context.Context, workspaceID string) ([]string, error) {
	wsDir := filepath.Join(c.root, workspaceID)
	entries, err := os.ReadDir(wsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

func (c *ContentStore) memoryDir(workspaceID, memoryID string) string {
	return filepath.Join(c.root, workspaceID, memoryID)
}
