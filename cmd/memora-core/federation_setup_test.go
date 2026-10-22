package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestSetupFederation: serve and the stdio MCP server share this setup.
// Config peers are seeded into the store and loaded, the federation
// secret file is created 0600, and the service can search federated.
// A second start with the same config keeps a runtime removal.
func TestSetupFederation(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Embedding.Model = "hash:default"
	peerID := types.NewID()
	cfg.Federation = config.FederationConfig{Enabled: true, Peers: []config.PeerConfig{
		{NodeID: peerID, Name: "peer", URL: "https://peer.example.com", APIKey: "k"},
	}}
	ctx := context.Background()
	svc, closeAll, err := openCoreAdapters(ctx, cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()

	reg, peers, err := setupFederation(ctx, cfg, svc, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if reg.Peer(peerID) == nil || svc.Federation == nil || peers == nil {
		t.Fatalf("registry %v, searcher %v", reg.Peers(), svc.Federation)
	}
	info, err := os.Stat(filepath.Join(cfg.Storage.DataDir, "federation.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("federation.key: %v, %v", info, err)
	}

	if err := peers.Delete(ctx, peerID); err != nil {
		t.Fatal(err)
	}
	reg, _, err = setupFederation(ctx, cfg, svc, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if reg.Peer(peerID) != nil {
		t.Fatal("restart re-imported a peer removed at runtime")
	}

	cfg.Federation.Enabled = false
	svc.Federation = nil
	if reg, peers, err := setupFederation(ctx, cfg, svc, discardLogger()); reg != nil || peers != nil || err != nil || svc.Federation != nil {
		t.Fatalf("federation off: %v %v %v", reg, peers, err)
	}
}
