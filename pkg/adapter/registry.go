package adapter

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry holds every adapter factory registered at boot. The
// `cloud` build tag toggles cloud-only factories on or off.
var (
	regMu             sync.RWMutex
	primaryDrivers    = map[string]PrimaryFactory{}
	vectorDrivers     = map[string]VectorFactory{}
	ledgerDrivers     = map[string]LedgerFactory{}
	contentDrivers    = map[string]ContentFactory{}
	graphDrivers      = map[string]GraphFactory{}
	identityProviders = map[string]IdentityFactory{}
)

// RegisterPrimary registers a PrimaryStore factory under a driver name.
// Idiomatic call site: a package's init() function.
func RegisterPrimary(name string, factory PrimaryFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := primaryDrivers[name]; dup {
		panic(fmt.Sprintf("memora: PrimaryStore driver %q registered twice", name))
	}
	primaryDrivers[name] = factory
}

// RegisterVector registers a VectorStore factory.
func RegisterVector(name string, factory VectorFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := vectorDrivers[name]; dup {
		panic(fmt.Sprintf("memora: VectorStore driver %q registered twice", name))
	}
	vectorDrivers[name] = factory
}

// RegisterLedger registers a LedgerStore factory.
func RegisterLedger(name string, factory LedgerFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := ledgerDrivers[name]; dup {
		panic(fmt.Sprintf("memora: LedgerStore driver %q registered twice", name))
	}
	ledgerDrivers[name] = factory
}

// RegisterContent registers a ContentStore factory.
func RegisterContent(name string, factory ContentFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := contentDrivers[name]; dup {
		panic(fmt.Sprintf("memora: ContentStore driver %q registered twice", name))
	}
	contentDrivers[name] = factory
}

// RegisterGraph registers a GraphStore factory.
func RegisterGraph(name string, factory GraphFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := graphDrivers[name]; dup {
		panic(fmt.Sprintf("memora: GraphStore driver %q registered twice", name))
	}
	graphDrivers[name] = factory
}

// RegisterIdentity registers an IdentityProvider factory.
func RegisterIdentity(name string, factory IdentityFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := identityProviders[name]; dup {
		panic(fmt.Sprintf("memora: IdentityProvider %q registered twice", name))
	}
	identityProviders[name] = factory
}

// OpenPrimary instantiates and opens a PrimaryStore by driver name.
func OpenPrimary(ctx context.Context, cfg PrimaryConfig) (PrimaryStore, error) {
	regMu.RLock()
	factory, ok := primaryDrivers[cfg.Driver]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown PrimaryStore driver %q (registered: %v)", cfg.Driver, ListPrimaryDrivers())
	}
	store := factory()
	if err := store.Open(ctx, cfg); err != nil {
		return nil, fmt.Errorf("memora: open PrimaryStore %q: %w", cfg.Driver, err)
	}
	return store, nil
}

// OpenVector instantiates and opens a VectorStore by driver name.
func OpenVector(ctx context.Context, cfg VectorConfig) (VectorStore, error) {
	regMu.RLock()
	factory, ok := vectorDrivers[cfg.Driver]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown VectorStore driver %q (registered: %v)", cfg.Driver, ListVectorDrivers())
	}
	store := factory()
	if err := store.Open(ctx, cfg); err != nil {
		return nil, fmt.Errorf("memora: open VectorStore %q: %w", cfg.Driver, err)
	}
	return store, nil
}

// OpenLedger instantiates and opens a LedgerStore by driver name.
func OpenLedger(ctx context.Context, cfg LedgerConfig) (LedgerStore, error) {
	regMu.RLock()
	factory, ok := ledgerDrivers[cfg.Driver]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown LedgerStore driver %q (registered: %v)", cfg.Driver, ListLedgerDrivers())
	}
	store := factory()
	if err := store.Open(ctx, cfg); err != nil {
		return nil, fmt.Errorf("memora: open LedgerStore %q: %w", cfg.Driver, err)
	}
	return store, nil
}

// OpenContent instantiates and opens a ContentStore by driver name.
func OpenContent(ctx context.Context, cfg ContentConfig) (ContentStore, error) {
	regMu.RLock()
	factory, ok := contentDrivers[cfg.Driver]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown ContentStore driver %q (registered: %v)", cfg.Driver, ListContentDrivers())
	}
	store := factory()
	if err := store.Open(ctx, cfg); err != nil {
		return nil, fmt.Errorf("memora: open ContentStore %q: %w", cfg.Driver, err)
	}
	return store, nil
}

// OpenGraph instantiates and opens a GraphStore by driver name.
func OpenGraph(ctx context.Context, cfg GraphConfig) (GraphStore, error) {
	regMu.RLock()
	factory, ok := graphDrivers[cfg.Driver]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown GraphStore driver %q (registered: %v)", cfg.Driver, ListGraphDrivers())
	}
	store := factory()
	if err := store.Open(ctx, cfg); err != nil {
		return nil, fmt.Errorf("memora: open GraphStore %q: %w", cfg.Driver, err)
	}
	return store, nil
}

// OpenIdentity returns an IdentityProvider instance by name.
func OpenIdentity(name string) (IdentityProvider, error) {
	regMu.RLock()
	factory, ok := identityProviders[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memora: unknown IdentityProvider %q (registered: %v)", name, ListIdentityProviders())
	}
	return factory(), nil
}

// ListPrimaryDrivers returns the registered PrimaryStore driver names.
func ListPrimaryDrivers() []string { return sortedKeys(primaryDrivers) }

// ListVectorDrivers returns the registered VectorStore driver names.
func ListVectorDrivers() []string { return sortedKeys(vectorDrivers) }

// ListLedgerDrivers returns the registered LedgerStore driver names.
func ListLedgerDrivers() []string { return sortedKeys(ledgerDrivers) }

// ListContentDrivers returns the registered ContentStore driver names.
func ListContentDrivers() []string { return sortedKeys(contentDrivers) }

// ListGraphDrivers returns the registered GraphStore driver names.
func ListGraphDrivers() []string { return sortedKeys(graphDrivers) }

// ListIdentityProviders returns the registered IdentityProvider names.
func ListIdentityProviders() []string { return sortedKeys(identityProviders) }

func sortedKeys[V any](m map[string]V) []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
