package adapter

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/axiom-studio/memora/pkg/types"
)

// EncodeListCursor returns the opaque cursor for a newest-first listing
// (ListWorkspaces, ListMemories) positioned after the row with this
// created_at and id. The next page holds rows strictly older, or equally
// old with a smaller id.
func EncodeListCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeListCursor parses a cursor from EncodeListCursor. A malformed
// cursor is ErrInvalidInput.
func DecodeListCursor(cursor string) (time.Time, string, error) {
	bad := fmt.Errorf("%w: cursor is invalid", types.ErrInvalidInput)
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", bad
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", bad
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", bad
	}
	if _, err := types.ParseID("cursor", id); err != nil {
		return time.Time{}, "", bad
	}
	return t, id, nil
}
