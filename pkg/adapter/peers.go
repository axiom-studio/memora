package adapter

import (
	"context"
	"time"
)

// PeerRecord is a federation peer as persisted (F-7). Keys are never
// stored in the clear: InboundKeySHA256 is the hash of the key the peer
// presents, and SealedAPIKey is the key this node presents to the peer,
// sealed by the node's federation secret (federation.Sealer).
type PeerRecord struct {
	NodeID           string
	Name             string
	URL              string
	InboundKeySHA256 string
	SealedAPIKey     []byte
	TLSCert          string
	ClientCertFile   string
	ClientKeyFile    string
	ClientCertSHA256 string
	TimeoutMS        int
	RateLimitQPS     float64
	InboundQPS       float64
	Revoked          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// PeerStore persists the federation peer registry. Deletion is a
// tombstone, so SeedPeer (the config-file import) never brings back a
// peer an operator removed.
type PeerStore interface {
	// ListPeers returns live peers ordered by node ID.
	ListPeers(ctx context.Context) ([]PeerRecord, error)
	// UpsertPeer inserts or replaces a peer, reviving a deleted one.
	// CreatedAt is kept from an existing row.
	UpsertPeer(ctx context.Context, p PeerRecord) error
	// DeletePeer tombstones a live peer; types.ErrNotFound otherwise.
	DeletePeer(ctx context.Context, nodeID string) error
	// SeedPeer inserts p only when no row, live or deleted, has its
	// node ID, and reports whether it did.
	SeedPeer(ctx context.Context, p PeerRecord) (bool, error)
}
