// Package file is the JSONL-with-gzip-rotation LedgerStore — the
// drop-in choice for operators who want a flat-file audit trail
// instead of running a database. Suitable for piping into existing
// log infrastructure (Vector, Fluent Bit, etc.).
package file

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func init() {
	adapter.RegisterLedger("file", func() adapter.LedgerStore { return &Store{} })
}

// Store implements adapter.LedgerStore writing JSONL files. Rotation
// is by size (default 100 MiB) — rotated files are gzipped in the
// background and renamed with an ISO timestamp suffix.
type Store struct {
	path       string
	maxBytes   int64
	f          *os.File
	bw         *bufio.Writer
	written    int64
	mu         sync.Mutex
}

const defaultMaxBytes = 100 * 1024 * 1024

// Open implements adapter.LedgerStore.
func (s *Store) Open(_ context.Context, cfg adapter.LedgerConfig) error {
	if cfg.DSN == "" {
		return errors.New("file ledger: DSN required (path to .jsonl)")
	}
	maxBytes := int64(defaultMaxBytes)
	if v, ok := cfg.Extra["max_bytes"]; ok {
		switch t := v.(type) {
		case int:
			maxBytes = int64(t)
		case int64:
			maxBytes = t
		case float64:
			maxBytes = int64(t)
		case string:
			if parsed, err := strconv.ParseInt(t, 10, 64); err == nil {
				maxBytes = parsed
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DSN), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(cfg.DSN, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("file ledger: open: %w", err)
	}
	info, _ := f.Stat()
	s.path = cfg.DSN
	s.maxBytes = maxBytes
	s.f = f
	s.bw = bufio.NewWriter(f)
	s.written = info.Size()
	return nil
}

// Close implements adapter.LedgerStore.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bw != nil {
		if err := s.bw.Flush(); err != nil {
			return err
		}
	}
	if s.f != nil {
		return s.f.Close()
	}
	return nil
}

// Ping implements adapter.LedgerStore.
func (s *Store) Ping(_ context.Context) error {
	if s.f == nil {
		return errors.New("file ledger: not opened")
	}
	return nil
}

// Capabilities implements adapter.LedgerStore.
func (s *Store) Capabilities() adapter.LedgerCapabilities {
	return adapter.LedgerCapabilities{
		SupportsAppend:      true,
		SupportsBatchAppend: true,
		SupportsQuery:       false,
		SupportsRedaction:   true,
		DurableOnAppend:     true, // fsync on every Append
		EstimatedAppendQPS:  2000,
	}
}

// Append writes one entry and fsyncs. Triggers rotation if the file
// would exceed max_bytes.
func (s *Store) Append(_ context.Context, e api.LedgerEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.LedgerID == "" {
		e.LedgerID = types.NewID(types.LedgerIDPrefix)
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if s.written+int64(len(b)) > s.maxBytes {
		if err := s.rotate(); err != nil {
			return err
		}
	}
	n, err := s.bw.Write(b)
	if err != nil {
		return err
	}
	s.written += int64(n)
	if err := s.bw.Flush(); err != nil {
		return err
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	return nil
}

// AppendBatch writes N entries with a single fsync.
func (s *Store) AppendBatch(_ context.Context, entries []api.LedgerEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range entries {
		e := &entries[i]
		if e.LedgerID == "" {
			e.LedgerID = types.NewID(types.LedgerIDPrefix)
		}
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		if s.written+int64(len(b)) > s.maxBytes {
			if err := s.rotate(); err != nil {
				return err
			}
		}
		n, _ := s.bw.Write(b)
		s.written += int64(n)
	}
	if err := s.bw.Flush(); err != nil {
		return err
	}
	return s.f.Sync()
}

// Query is not supported by the file adapter — returns ErrCapability.
func (s *Store) Query(_ context.Context, _ adapter.LedgerQuery) ([]api.LedgerEntry, string, error) {
	return nil, "", fmt.Errorf("%w: file ledger does not support query (use `memora-cli ledger tail` or a SIEM downstream)", types.ErrCapability)
}

// Redact appends a tombstone record. The original line is preserved.
func (s *Store) Redact(_ context.Context, ledgerID string, fields []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tombstone := map[string]any{
		"_tombstone":      true,
		"redacted_for":    ledgerID,
		"redacted_fields": fields,
		"ts":              time.Now().UTC(),
	}
	b, _ := json.Marshal(tombstone)
	b = append(b, '\n')
	if _, err := s.bw.Write(b); err != nil {
		return err
	}
	if err := s.bw.Flush(); err != nil {
		return err
	}
	return s.f.Sync()
}

func (s *Store) rotate() error {
	if err := s.bw.Flush(); err != nil {
		return err
	}
	if err := s.f.Close(); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	rotated := s.path + "." + ts
	if err := os.Rename(s.path, rotated); err != nil {
		return err
	}
	// Async gzip of the rotated file. Best-effort.
	go gzipFile(rotated)
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	s.f = f
	s.bw = bufio.NewWriter(f)
	s.written = 0
	return nil
}

func gzipFile(path string) {
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := os.Create(path + ".gz")
	if err != nil {
		return
	}
	defer dst.Close()
	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err == nil {
		_ = gz.Close()
		_ = os.Remove(path)
	}
}
