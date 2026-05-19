package adapter

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterMetadata_DuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()

	const name = "test-dup-metadata"
	RegisterMetadata(name, func() MetadataStore { return nil })
	RegisterMetadata(name, func() MetadataStore { return nil }) // panic
}

func TestOpenMetadata_UnknownDriverErrors(t *testing.T) {
	_, err := OpenMetadata(context.Background(), MetadataConfig{Driver: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if !strings.Contains(err.Error(), "unknown MetadataStore driver") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListDrivers_Sorted(t *testing.T) {
	got := ListMetadataDrivers()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("ListMetadataDrivers is not sorted: %v", got)
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
