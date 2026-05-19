package federation

import (
	"time"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

func NewOutboundRecallEntry(wsID, federationID string, fr *FanoutResult) api.LedgerEntry {
	meta := map[string]any{
		"federation_id": federationID,
		"total_hits":    len(fr.Results),
		"total_scanned": fr.TotalScanned,
	}
	if fr.PartialSuccess {
		meta["partial_success"] = true
		meta["failed_peers"] = fr.FailedPeers
	}
	if len(fr.PeerLatencyMS) > 0 {
		meta["peer_latency_ms"] = fr.PeerLatencyMS
	}
	return api.LedgerEntry{
		LedgerID:    types.NewID(types.LedgerIDPrefix),
		WorkspaceID: wsID,
		Op:          "federation_recall",
		Timestamp:   time.Now().UTC(),
		Metadata:    meta,
	}
}

func NewOutboundLookupEntry(wsID, federationID, memoryID string, found bool, peerName string) api.LedgerEntry {
	meta := map[string]any{
		"federation_id": federationID,
		"memory_id":     memoryID,
		"found":         found,
	}
	if found {
		meta["found_on_peer"] = peerName
	}
	return api.LedgerEntry{
		LedgerID:    types.NewID(types.LedgerIDPrefix),
		WorkspaceID: wsID,
		Op:          "federation_lookup",
		Target:      memoryID,
		Timestamp:   time.Now().UTC(),
		Metadata:    meta,
	}
}

func NewInboundEntry(wsID, peerID, federationPath, op string) api.LedgerEntry {
	return api.LedgerEntry{
		LedgerID:    types.NewID(types.LedgerIDPrefix),
		WorkspaceID: wsID,
		Op:          "federation_inbound",
		AgentID:     peerID,
		Timestamp:   time.Now().UTC(),
		Metadata: map[string]any{
			"requesting_peer": peerID,
			"federation_path": federationPath,
			"inbound_op":      op,
		},
	}
}

func NewRejectedEntry(wsID, peerID, reason string) api.LedgerEntry {
	return api.LedgerEntry{
		LedgerID:    types.NewID(types.LedgerIDPrefix),
		WorkspaceID: wsID,
		Op:          "federation_rejected",
		AgentID:     peerID,
		Timestamp:   time.Now().UTC(),
		Metadata: map[string]any{
			"requesting_peer": peerID,
			"reason":          reason,
		},
	}
}
