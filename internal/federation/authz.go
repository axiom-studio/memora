package federation

import (
	"fmt"
	"net/http"

	"github.com/axiom-studio/memora/pkg/types"
)

type InboundAuthResult struct {
	PeerID         string
	FederationID   string
	FederationPath []string
}

// AuthorizeFederatedRequest identifies the calling peer by its node ID
// (X-Memora-Local-Instance-ID) and applies the loop guard against this
// node's ID. A cluster label, when both sides set one, must match.
func AuthorizeFederatedRequest(r *http.Request, reg *Registry) (*InboundAuthResult, error) {
	peerID := r.Header.Get(HeaderLocalInstanceID)
	if peerID == "" {
		return nil, nil
	}

	remoteFedID := r.Header.Get(HeaderFederationID)
	if remoteFedID != "" && reg.FederationID != "" && remoteFedID != reg.FederationID {
		return nil, fmt.Errorf("%w: remote federation %q != local %q", types.ErrFederationAuth, remoteFedID, reg.FederationID)
	}

	if reg.Peer(peerID) == nil {
		return nil, fmt.Errorf("%w: peer %q not in registry", types.ErrFederationAuth, peerID)
	}

	path := ParseFederationPath(r.Header.Get(HeaderFederationPath))
	if err := CheckLoop(path, reg.NodeID); err != nil {
		return nil, err
	}

	return &InboundAuthResult{
		PeerID:         peerID,
		FederationID:   remoteFedID,
		FederationPath: path,
	}, nil
}

func AuthorizeFederatedWorkspace(reg *Registry, peerID, wsID string) error {
	if !reg.IsAuthorizedPeer(peerID, wsID) {
		return fmt.Errorf("%w: peer %q not authorized for workspace %q", types.ErrFederationAuth, peerID, wsID)
	}
	return nil
}
