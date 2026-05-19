// Package file implements a filesystem-backed ContentStore. Content is
// stored as individual files under <root>/<workspace_id>/<memory_id>/
// with 0o600 permissions.
package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

var idSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,128}$`)

func validIDSegment(s string) bool { return idSegmentRe.MatchString(s) }

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
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "memory.txt"), []byte(content), 0o600)
}

func (c *ContentStore) GetMemoryContent(_ context.Context, workspaceID, memoryID string) (string, error) {
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "memory.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", types.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *ContentStore) DeleteMemoryContent(_ context.Context, workspaceID, memoryID string) error {
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, "memory.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) PutCellContent(_ context.Context, workspaceID, memoryID, cellID, _, text string) error {
	if !validIDSegment(cellID) {
		return fmt.Errorf("%w: invalid cell_id segment", types.ErrInvalidInput)
	}
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cellID+".txt"), []byte(text), 0o600)
}

func (c *ContentStore) GetCellContent(_ context.Context, workspaceID, memoryID, cellID string) (string, error) {
	if !validIDSegment(cellID) {
		return "", fmt.Errorf("%w: invalid cell_id segment", types.ErrInvalidInput)
	}
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, cellID+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", types.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *ContentStore) GetCellContentBatch(_ context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error) {
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cellIDs))
	for _, id := range cellIDs {
		if !validIDSegment(id) {
			continue
		}
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
	if !validIDSegment(cellID) {
		return fmt.Errorf("%w: invalid cell_id segment", types.ErrInvalidInput)
	}
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, cellID+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) DeleteAllForMemory(_ context.Context, workspaceID, memoryID string) error {
	dir, err := c.memoryDir(workspaceID, memoryID)
	if err != nil {
		return err
	}
	err = os.RemoveAll(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *ContentStore) ListMemoryIDs(_ context.Context, workspaceID string) ([]string, error) {
	if !validIDSegment(workspaceID) {
		return nil, fmt.Errorf("%w: invalid workspace_id segment", types.ErrInvalidInput)
	}
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

func (c *ContentStore) ListMemoryIDsOlderThan(_ context.Context, workspaceID string, cutoff time.Time) ([]string, error) {
	if !validIDSegment(workspaceID) {
		return nil, fmt.Errorf("%w: invalid workspace_id segment", types.ErrInvalidInput)
	}
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
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

func (c *ContentStore) memoryDir(workspaceID, memoryID string) (string, error) {
	if !validIDSegment(workspaceID) || !validIDSegment(memoryID) {
		return "", fmt.Errorf("%w: invalid id segment", types.ErrInvalidInput)
	}
	path := filepath.Join(c.root, workspaceID, memoryID)
	clean := filepath.Clean(path)
	rootClean := filepath.Clean(c.root)
	if !strings.HasPrefix(clean+string(filepath.Separator), rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path escapes root", types.ErrInvalidInput)
	}
	return clean, nil
}
