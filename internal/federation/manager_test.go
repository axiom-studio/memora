package federation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axiom-studio/memora/internal/config"
	storesqlite "github.com/axiom-studio/memora/internal/store/sqlite"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// writeCA writes a self-signed CA certificate as PEM and returns its path.
func writeCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestManager returns a Manager on a real SQLite store with no peers.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	ctx := context.Background()
	store := &storesqlite.Store{}
	if err := store.Open(ctx, adapter.MetadataConfig{DSN: filepath.Join(t.TempDir(), "peers.db")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reg, err := NewRegistry(config.FederationConfig{Enabled: true}, nid("self"))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sealer, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(reg, store, sealer)
}

// brokenPeerManager returns a Manager whose store holds peer "tls" (CA
// file since removed, so the stored set no longer builds) and peer
// "victim", both loaded in the live registry.
func brokenPeerManager(t *testing.T) (m *Manager, victim string) {
	t.Helper()
	ctx := context.Background()
	m = newTestManager(t)
	ca, victim := writeCA(t), nid("victim")
	if _, err := m.Seed(ctx, []config.PeerConfig{
		{NodeID: nid("tls"), Name: "tls", URL: "https://tls.example.com", APIKey: "key-tls", TLSCert: ca},
		{NodeID: victim, Name: "victim", URL: "https://victim.example.com", APIKey: "key-victim"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ca); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); err == nil {
		t.Fatal("the stored set still builds without the CA file; this test proves nothing")
	}
	return m, victim
}

// TestManagerDelete_OtherPeerBroken (#6351): deleting a peer takes it out
// of the live registry even when another stored peer no longer builds.
func TestManagerDelete_OtherPeerBroken(t *testing.T) {
	m, victim := brokenPeerManager(t)
	if err := m.Delete(context.Background(), victim); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if m.reg.Peer(victim) != nil || m.reg.Client(victim) != nil {
		t.Fatal("the deleted peer is still in the live registry")
	}
	if m.reg.Peer(nid("tls")) == nil || m.reg.Client(nid("tls")) == nil {
		t.Fatal("the other peer was dropped from the live registry")
	}
}

// TestManagerRevoke_OtherPeerBroken (#6351): a pure revoke is persisted
// and applied live even when another stored peer no longer builds; an
// edit that changes anything else is still refused.
func TestManagerRevoke_OtherPeerBroken(t *testing.T) {
	m, victim := brokenPeerManager(t)
	ctx := context.Background()
	req := api.PeerRequest{NodeID: victim, Name: "victim", URL: "https://victim.example.com"}

	rename := req
	rename.Name, rename.Revoked = "renamed", true
	if _, err := m.Upsert(ctx, rename); err == nil {
		t.Fatal("an edit beyond revoking was accepted while the stored set does not build")
	}
	if m.reg.Peer(victim).Revoked {
		t.Fatal("a refused edit revoked the peer")
	}

	req.Revoked = true
	v, err := m.Upsert(ctx, req)
	if err != nil || !v.Revoked {
		t.Fatalf("revoke: %+v, %v", v, err)
	}
	if p := m.reg.Peer(victim); p == nil || !p.Revoked || p.Searchable() {
		t.Fatalf("live peer after revoke = %+v, want revoked", p)
	}
}

// TestManagerSeed_ConflictWithStoredPeer (#6353): a config peer that is
// valid on its own but shares a stored peer's inbound key is refused
// before anything is written, so the next start still loads.
func TestManagerSeed_ConflictWithStoredPeer(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if _, err := m.Upsert(ctx, api.PeerRequest{NodeID: nid("runtime"), Name: "runtime", URL: "https://runtime.example.com", InboundKey: "shared-key"}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("shared-key"))
	clash := config.PeerConfig{NodeID: nid("config"), Name: "config", URL: "https://config.example.com", InboundKeySHA256: hex.EncodeToString(sum[:])}
	if n, err := m.Seed(ctx, []config.PeerConfig{clash}); err == nil || n != 0 || !strings.Contains(err.Error(), "none were seeded") {
		t.Fatalf("seed = %d, %v; want a conflict refused before writing", n, err)
	}
	recs, err := m.store.ListPeers(ctx)
	if err != nil || len(recs) != 1 || recs[0].NodeID != nid("runtime") {
		t.Fatalf("stored peers = %+v, %v; want only the runtime peer", recs, err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatalf("reload after the refused seed: %v", err)
	}

	// A config peer already stored is not re-checked: it is never seeded.
	again := config.PeerConfig{NodeID: nid("runtime"), Name: "runtime", URL: "https://runtime.example.com", InboundKeySHA256: hex.EncodeToString(sum[:])}
	if n, err := m.Seed(ctx, []config.PeerConfig{again}); err != nil || n != 0 {
		t.Fatalf("seed of an already stored peer = %d, %v", n, err)
	}
}

// TestManagerSeed_TombstonedConfigPeerSkipped (#6353 QA rework): a config
// peer deleted at runtime is never seeded again, so it is not checked
// against the stored peers either; a runtime peer reusing its inbound
// key must not stop the next start with an unchanged config.
func TestManagerSeed_TombstonedConfigPeerSkipped(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sum := sha256.Sum256([]byte("reused-key"))
	cfg := []config.PeerConfig{{NodeID: nid("x-old"), Name: "remote", URL: "https://remote.example.com", InboundKeySHA256: hex.EncodeToString(sum[:])}}
	if n, err := m.Seed(ctx, cfg); err != nil || n != 1 {
		t.Fatalf("first seed = %d, %v", n, err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, nid("x-old")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Upsert(ctx, api.PeerRequest{NodeID: nid("y-new"), Name: "remote", URL: "https://remote.example.com", InboundKey: "reused-key"}); err != nil {
		t.Fatal(err)
	}

	if n, err := m.Seed(ctx, cfg); err != nil || n != 0 {
		t.Fatalf("restart seed = %d, %v; want the tombstoned config peer skipped", n, err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatalf("restart reload: %v", err)
	}
}
