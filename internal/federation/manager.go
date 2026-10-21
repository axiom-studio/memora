package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/axiom-studio/memora/internal/config"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// Manager edits the runtime peer registry (F-7): peers live in the
// metadata store, and every edit is persisted and then swapped into the
// Registry, so the next search, fetch or inbound call sees it. Config
// file peers are only a seed.
type Manager struct {
	reg    *Registry
	store  adapter.PeerStore
	sealer *Sealer
	mu     sync.Mutex // serializes edits: validate, persist, reload

	// AllowClientCertPins is set when the server serves TLS; without it
	// a client_cert_sha256 pin could never be satisfied and is refused.
	AllowClientCertPins bool
}

// NewManager returns a Manager for reg backed by store, sealing outbound
// keys with sealer.
func NewManager(reg *Registry, store adapter.PeerStore, sealer *Sealer) *Manager {
	return &Manager{reg: reg, store: store, sealer: sealer}
}

// Seed imports each config-file peer the store has never held, live or
// deleted, so a runtime edit or removal is never overwritten. It returns
// how many were imported. Call Reload afterwards.
func (m *Manager) Seed(ctx context.Context, peers []config.PeerConfig) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reg.Validate(peers); err != nil {
		return 0, err
	}
	n := 0
	for _, pc := range peers {
		sealed, err := m.sealer.Seal(pc.APIKey, pc.NodeID)
		if err != nil {
			return n, err
		}
		ok, err := m.store.SeedPeer(ctx, adapter.PeerRecord{
			NodeID: pc.NodeID, Name: pc.Name, URL: pc.URL,
			InboundKeySHA256: strings.ToLower(pc.InboundKeySHA256), SealedAPIKey: sealed,
			TLSCert: pc.TLSCert, ClientCertFile: pc.ClientCertFile, ClientKeyFile: pc.ClientKeyFile,
			ClientCertSHA256: strings.ToLower(pc.ClientCertSHA256),
			TimeoutMS:        pc.TimeoutMS, RateLimitQPS: pc.RateLimitQPS, Revoked: pc.Revoked,
		})
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// Reload replaces the registry's peers with the store's.
func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked(ctx)
}

func (m *Manager) reloadLocked(ctx context.Context) error {
	recs, err := m.store.ListPeers(ctx)
	if err != nil {
		return err
	}
	return m.reg.Replace(m.toConfig(recs))
}

// toConfig opens each record's sealed key. A key that does not open (the
// federation secret changed) leaves that peer without an outbound key,
// so it is not searched, rather than failing the whole registry.
func (m *Manager) toConfig(recs []adapter.PeerRecord) []config.PeerConfig {
	out := make([]config.PeerConfig, 0, len(recs))
	for _, r := range recs {
		key, err := m.sealer.Open(r.SealedAPIKey, r.NodeID)
		if err != nil {
			slog.Default().Warn("federation peer api_key unreadable; peer will not be searched until it is set again",
				"peer", r.Name, "node_id", r.NodeID, "err", err)
			key = ""
		}
		out = append(out, config.PeerConfig{
			NodeID: r.NodeID, Name: r.Name, URL: r.URL, APIKey: key,
			InboundKeySHA256: r.InboundKeySHA256, TLSCert: r.TLSCert,
			ClientCertFile: r.ClientCertFile, ClientKeyFile: r.ClientKeyFile,
			ClientCertSHA256: r.ClientCertSHA256, TimeoutMS: r.TimeoutMS,
			RateLimitQPS: r.RateLimitQPS, Revoked: r.Revoked,
		})
	}
	return out
}

// List returns the peers as the API shows them: no keys.
func (m *Manager) List(ctx context.Context) ([]api.PeerView, error) {
	recs, err := m.store.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.PeerView, 0, len(recs))
	for _, r := range recs {
		out = append(out, view(r))
	}
	return out, nil
}

func view(r adapter.PeerRecord) api.PeerView {
	trust := string(TrustAPIKey)
	if r.TLSCert != "" || r.ClientCertFile != "" {
		trust = string(TrustMTLS)
	}
	return api.PeerView{
		NodeID: r.NodeID, Name: r.Name, URL: r.URL, TrustMode: trust,
		HasInboundKey: r.InboundKeySHA256 != "", HasAPIKey: len(r.SealedAPIKey) > 0,
		ClientCertSHA256: r.ClientCertSHA256, TimeoutMS: r.TimeoutMS, RateLimitQPS: r.RateLimitQPS,
		Revoked: r.Revoked, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// Upsert creates or replaces a peer and swaps it into the registry. An
// omitted key keeps the stored one; TLS file paths are kept from the
// stored peer (they are set only in config). The whole peer set is
// validated, client TLS files included, before anything is written.
func (m *Manager) Upsert(ctx context.Context, req api.PeerRequest) (api.PeerView, error) {
	if _, err := types.ParseID("node_id", req.NodeID); err != nil {
		return api.PeerView{}, err
	}
	invalid := func(msg string) (api.PeerView, error) {
		return api.PeerView{}, fmt.Errorf("%w: %s", types.ErrInvalidInput, msg)
	}
	switch {
	case strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "":
		return invalid("name and url are required")
	case req.InboundKey != "" && req.InboundKeySHA256 != "":
		return invalid("set inbound_key or inbound_key_sha256, not both")
	case req.TimeoutMS < 0 || req.RateLimitQPS < 0:
		return invalid("timeout_ms and rate_limit_qps must not be negative")
	case req.ClientCertSHA256 != "" && !m.AllowClientCertPins:
		return invalid("client_cert_sha256 needs server.tls")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	recs, err := m.store.ListPeers(ctx)
	if err != nil {
		return api.PeerView{}, err
	}
	rec := adapter.PeerRecord{NodeID: req.NodeID}
	idx := -1
	for i := range recs {
		if recs[i].NodeID == req.NodeID {
			rec, idx = recs[i], i
		}
	}
	rec.Name, rec.URL = strings.TrimSpace(req.Name), strings.TrimSpace(req.URL)
	rec.ClientCertSHA256 = strings.ToLower(req.ClientCertSHA256)
	rec.TimeoutMS, rec.RateLimitQPS, rec.Revoked = req.TimeoutMS, req.RateLimitQPS, req.Revoked
	switch {
	case req.InboundKey != "":
		sum := sha256.Sum256([]byte(req.InboundKey))
		rec.InboundKeySHA256 = hex.EncodeToString(sum[:])
	case req.InboundKeySHA256 != "":
		rec.InboundKeySHA256 = strings.ToLower(req.InboundKeySHA256)
	}
	if req.APIKey != "" {
		if rec.SealedAPIKey, err = m.sealer.Seal(req.APIKey, req.NodeID); err != nil {
			return api.PeerView{}, err
		}
	}
	if idx >= 0 {
		recs[idx] = rec
	} else {
		recs = append(recs, rec)
	}
	if err := m.reg.Validate(m.toConfig(recs)); err != nil {
		return api.PeerView{}, fmt.Errorf("%w: %v", types.ErrInvalidInput, err)
	}
	if err := m.store.UpsertPeer(ctx, rec); err != nil {
		return api.PeerView{}, err
	}
	if err := m.reloadLocked(ctx); err != nil {
		return api.PeerView{}, err
	}
	stored, err := m.store.ListPeers(ctx)
	if err != nil {
		return api.PeerView{}, err
	}
	for _, r := range stored {
		if r.NodeID == req.NodeID {
			return view(r), nil
		}
	}
	return api.PeerView{}, errors.New("federation: upserted peer not found")
}

// Delete removes a peer: it stops being searched and its inbound calls
// are refused from the next request on. types.ErrNotFound when absent.
func (m *Manager) Delete(ctx context.Context, nodeID string) error {
	if _, err := types.ParseID("node_id", nodeID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.DeletePeer(ctx, nodeID); err != nil {
		return err
	}
	return m.reloadLocked(ctx)
}
