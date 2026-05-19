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

func AuthorizeFederatedRequest(r *http.Request, reg *Registry, localFedID string) (*InboundAuthResult, error) {
	peerID := r.Header.Get(HeaderLocalInstanceID)
	if peerID == "" {
		return nil, nil
	}

	remoteFedID := r.Header.Get(HeaderFederationID)
	if remoteFedID != "" && remoteFedID != localFedID {
		return nil, fmt.Errorf("%w: remote %q != local %q", types.ErrFederationAuth, remoteFedID, localFedID)
	}

	if reg.Peer(peerID) == nil {
		return nil, fmt.Errorf("%w: peer %q not in registry", types.ErrFederationAuth, peerID)
	}

	path := ParseFederationPath(r.Header.Get(HeaderFederationPath))
	if err := CheckLoop(path, localFedID); err != nil {
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
