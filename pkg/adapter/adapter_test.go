package adapter

import (
	"context"
	"strings"
	"testing"
)

// Test registry behavior in isolation.

func TestRegisterPrimary_DuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()

	const name = "test-dup-primary"
	RegisterPrimary(name, func() PrimaryStore { return nil })
	RegisterPrimary(name, func() PrimaryStore { return nil }) // panic
}

func TestOpenPrimary_UnknownDriverErrors(t *testing.T) {
	_, err := OpenPrimary(context.Background(), PrimaryConfig{Driver: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if !strings.Contains(err.Error(), "unknown PrimaryStore driver") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListDrivers_Sorted(t *testing.T) {
	got := ListPrimaryDrivers()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("ListPrimaryDrivers is not sorted: %v", got)
		}
	}
}

func TestOpenLedger_UnknownDriverErrors(t *testing.T) {
	_, err := OpenLedger(context.Background(), LedgerConfig{Driver: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenVector_UnknownDriverErrors(t *testing.T) {
	_, err := OpenVector(context.Background(), VectorConfig{Driver: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIdentity_UnknownProviderErrors(t *testing.T) {
	_, err := OpenIdentity("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}
