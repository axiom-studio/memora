package federation

import (
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Federation audit ops (F-8). Entries are node-level (no workspace),
// except a served fetch, which is filed under the memory's workspace.
// The peer's node ID goes in api_key_id, the ledger's actor column, so
// `actor=<node_id>` finds every entry about one peer. The query text is
// kept in metadata, so ledger redaction (Redact with "metadata") hides
// it like any other sensitive field.
const (
	OpSearch   = "federation_search"   // caller: one federated search
	OpInbound  = "federation_inbound"  // receiver: a peer's search
	OpRejected = "federation_rejected" // receiver: a refused peer call
	OpFetch    = "federation_fetch"    // both sides: a memory fetched from its node
)

// Ops lists every federation audit op.
var Ops = []string{OpSearch, OpInbound, OpRejected, OpFetch}

func entry(op, actor string, meta map[string]any) api.LedgerEntry {
	return api.LedgerEntry{
		LedgerID:  types.NewID(),
		Op:        op,
		APIKeyID:  actor,
		AgentID:   actor,
		Timestamp: time.Now().UTC(),
		Metadata:  meta,
	}
}

// SearchEntry is the caller's audit of a federated search: the query,
// the peers asked, how many hits each node contributed, and the peers
// that did not answer.
func SearchEntry(query string, fr *FanoutResult) api.LedgerEntry {
	hits := map[string]int{}
	total := 0
	for _, l := range fr.Lists {
		hits[l.NodeID] = len(l.Hits)
		total += len(l.Hits)
	}
	meta := map[string]any{
		"query":         query,
		"peers_asked":   fr.Asked,
		"hits_per_node": hits,
		"total_hits":    total,
	}
	if fr.PartialSuccess() {
		meta["failed_peers"] = fr.FailedPeers()
	}
	return entry(OpSearch, "", meta)
}

// InboundSearchEntry is the receiver's audit of a peer's search.
func InboundSearchEntry(peerID, query string, path []string, hits int) api.LedgerEntry {
	return entry(OpInbound, peerID, map[string]any{
		"requesting_peer": peerID,
		"query":           query,
		"hit_count":       hits,
		"federation_path": path,
	})
}

// RejectedEntry is the receiver's audit of a refused peer call. peerID
// is the caller's claimed node ID when it is a well-formed one, else "".
// reason is one of the Reason* constants.
func RejectedEntry(peerID, reason, route string) api.LedgerEntry {
	e := entry(OpRejected, peerID, map[string]any{
		"requesting_peer": peerID,
		"reason":          reason,
	})
	e.Target = route
	return e
}

// FetchEntry audits a memory fetched from its owning node. direction is
// "outbound" on the node that asked and "inbound" on the node that
// served it; nodeID is the other node; outcome is an Outcome* value.
func FetchEntry(direction, nodeID, memoryID, workspaceID, outcome string) api.LedgerEntry {
	e := entry(OpFetch, nodeID, map[string]any{
		"direction": direction,
		"node_id":   nodeID,
		"memory_id": memoryID,
		"outcome":   outcome,
	})
	e.Target, e.WorkspaceID = memoryID, workspaceID
	return e
}
