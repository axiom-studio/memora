// Package types defines the public Memora domain types — Workspace,
// Collection, Memory, Cell, Edge, Agent, Watermark, Tag, Seed — plus
// the validators every adapter and handler relies on.
//
// These types are part of the OSS contract surface. Third-party SDKs
// and adapter authors import this package directly.
package types

import (
	"fmt"

	"github.com/google/uuid"
)

// AgentIDPrefix is the prefix client-supplied agent IDs still carry.
// Agent IDs become UUIDv4s with the identity_key change (ID-3); every
// other Memora ID is already a bare UUIDv4.
const AgentIDPrefix = "agent_"

// NewID returns a fresh ID: a canonical lowercase UUIDv4 such as
// "3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90". IDs carry no type prefix and
// no timestamp; nothing may rely on their order.
func NewID() string {
	return uuid.NewString()
}

// ParseID returns id unchanged if it is a canonical Memora ID — a
// lowercase, hyphenated UUIDv4 — and otherwise an ErrInvalidInput error
// naming field ("memory_id must be a UUID"). The field name matters
// because the ID itself no longer says what kind of thing it points at.
func ParseID(field, id string) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122 || u.String() != id {
		return "", fmt.Errorf("%w: %s must be a UUID, got %q", ErrInvalidInput, field, id)
	}
	return id, nil
}
