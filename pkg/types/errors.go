package types

import (
	"errors"
	"fmt"
)

// Sentinel errors. Adapter implementations return these so the server
// can map them to the right HTTP status without inspecting the message.
var (
	ErrNotFound       = errors.New("memora: not found")
	ErrCAS            = errors.New("memora: cas conflict (head watermark moved)")
	ErrAlreadyExists  = errors.New("memora: already exists")
	ErrQuotaExceeded  = errors.New("memora: quota exceeded")
	ErrInvalidInput   = errors.New("memora: invalid input")
	ErrSyntheticEdge  = errors.New("memora: cannot mutate synthetic edge from the parent_context_id shim")
	ErrAlreadyLinked  = errors.New("memora: parent_of edge already linked via synthetic shim")
	ErrPatchAnchor    = errors.New("memora: patch op anchor not found or ambiguous")
	ErrDepthExceeded  = errors.New("memora: graph traverse depth exceeded the adapter's cap")
	ErrCapability     = errors.New("memora: adapter does not support this capability")
	ErrEmbedPending   = errors.New("memora: memory has not finished embedding")
	ErrNotEmpty       = errors.New("memora: container has live children")
	ErrFederationLoop = errors.New("memora: federation loop detected")
	ErrFederationAuth = errors.New("memora: federation peer not authorized")
	ErrFederationDown = errors.New("memora: all federation peers failed")
)

func errEmpty(field string) error {
	return fmt.Errorf("%w: %s is empty", ErrInvalidInput, field)
}

func errf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}
