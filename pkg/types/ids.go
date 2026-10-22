// Package types defines the public Memora domain types — Workspace,
// Collection, Memory, Cell, Edge, Agent, Watermark, Tag, Seed — plus
// the validators every adapter and handler relies on.
//
// These types are part of the OSS contract surface. Third-party SDKs
// and adapter authors import this package directly.
package types

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

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

// notIDFields end in "_id" but are not UUIDs: chunker_id names a
// chunker, request_id is a client correlation token, and api_key_id and
// user_id come from outside Memora.
var notIDFields = map[string]bool{"chunker_id": true, "request_id": true, "api_key_id": true, "user_id": true}

// IDField reports whether a JSON field or argument name carries Memora
// UUIDs: one when the name ends in "_id", a list when it ends in "_ids",
// "_id_in" or "_id_not_in" (filter sets) or is "peers" (node IDs). Boundaries validate every field named this
// way, so a new ID field is covered by naming it so.
func IDField(name string) (isID, isList bool) {
	switch {
	case notIDFields[name]:
		return false, false
	case strings.HasSuffix(name, "_id"):
		return true, false
	case strings.HasSuffix(name, "_ids"), strings.HasSuffix(name, "_id_in"), strings.HasSuffix(name, "_id_not_in"), name == "peers":
		return true, true
	}
	return false, false
}
