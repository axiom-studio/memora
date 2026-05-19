// Package types defines the public Memora domain types — Workspace,
// Collection, Memory, Cell, Edge, Agent, Watermark, Tag, Seed — plus
// the validators every adapter and handler relies on.
//
// These types are part of the OSS contract surface. Third-party SDKs
// and adapter authors import this package directly.
package types

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// ID prefixes — every Memora identifier carries a typed prefix so a
// stray ID in a log message tells you what kind of thing it points at.
const (
	WorkspaceIDPrefix  = "ws_"
	CollectionIDPrefix = "coll_"
	MemoryIDPrefix     = "mem_"
	CellIDPrefix       = "cell_"
	EdgeIDPrefix       = "edg_"
	AgentIDPrefix      = "agent_"
	WatermarkPrefix    = "wmk_"
	LedgerIDPrefix     = "lg_"
	PinIDPrefix        = "pin_"
	SnapshotIDPrefix   = "snap_"
	FederationIDPrefix = "fed_"
)

// NewID returns a fresh prefixed ULID, e.g. "mem_01HXYZ...".
func NewID(prefix string) string {
	entropy := ulid.Monotonic(rand.Reader, 0)
	return prefix + ulid.MustNew(ulid.Timestamp(time.Now().UTC()), entropy).String()
}

// MustHavePrefix returns an error if id does not start with prefix.
// Used to fail fast on cross-table foreign keys that the wrong kind
// of ID slipped into.
func MustHavePrefix(id, prefix string) error {
	if id == "" {
		return fmt.Errorf("id is empty (expected %s prefix)", prefix)
	}
	if !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("id %q has wrong prefix (expected %s)", id, prefix)
	}
	return nil
}
