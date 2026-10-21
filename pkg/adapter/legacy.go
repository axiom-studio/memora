package adapter

import (
	"errors"
	"fmt"
)

// ErrLegacyIDs is a metadata store refusing to open a database written
// before identifiers were UUIDv4s. Stores check before any migration or
// seeding, so the refusal leaves the old database exactly as it was.
var ErrLegacyIDs = errors.New("metadata store holds IDs from before they were UUIDv4s")

// LegacyIDsError is the refusal for a store whose workspace id is not a
// UUID.
func LegacyIDsError(id string) error {
	return fmt.Errorf("%w: workspace %q; this release does not migrate it. Start with an empty database "+
		"(see docs/user-docs/operations.md, \"Upgrading from Prefixed IDs\")", ErrLegacyIDs, id)
}
