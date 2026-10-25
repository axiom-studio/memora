package federation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
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

// brokenPeerManager returns a Manager whose store holds peer "tls" (CA
// file since removed, so the stored set no longer builds) and peer
// "victim", both loaded in the live registry.
func brokenPeerManager(t *testing.T) (m *Manager, victim string) {
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
	m = NewManager(reg, store, sealer)
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
