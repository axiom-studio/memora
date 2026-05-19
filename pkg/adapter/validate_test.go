package adapter

import (
	"log/slog"
	"os"
	"testing"
)

func TestValidateCompatibility_CASRequired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := ValidateCompatibility(logger, MetadataCapabilities{SupportsCAS: false}, nil, nil)
	if err == nil {
		t.Fatal("expected error when MetadataStore lacks CAS")
	}

	err = ValidateCompatibility(logger, MetadataCapabilities{SupportsCAS: true}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCompatibility_ContentWarning(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	err := ValidateCompatibility(logger,
		MetadataCapabilities{SupportsCAS: true},
		&ContentCapabilities{SupportsConditionalPut: false},
		nil)
	if err != nil {
		t.Fatalf("unexpected error (should warn, not fail): %v", err)
	}
}

func TestValidateCompatibility_GraphMaxDepthZero(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	err := ValidateCompatibility(logger,
		MetadataCapabilities{SupportsCAS: true},
		nil,
		&GraphCapabilities{MaxDepth: 0})
	if err != nil {
		t.Fatalf("unexpected error (should warn, not fail): %v", err)
	}
}
