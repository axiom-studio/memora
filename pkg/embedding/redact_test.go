package embedding

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

const sentinelKey = "AIzaSENTINEL0123456789"

func TestRedact_StripsURLQueryStrings(t *testing.T) {
	in := `gemini: Post "https://example.com/v1/models/m:batchEmbedContents?key=` + sentinelKey + `": context deadline exceeded`
	got := Redact(in)
	if strings.Contains(got, sentinelKey) || !strings.Contains(got, "https://example.com/v1/models/m:batchEmbedContents?[redacted]") {
		t.Fatalf("Redact = %q", got)
	}
	if plain := "no urls here"; Redact(plain) != plain {
		t.Fatalf("Redact changed text without URLs")
	}
}

func TestTransportError_DropsURLQueryKeepsCause(t *testing.T) {
	ue := &url.Error{Op: "Post", URL: "https://example.com/embed?key=" + sentinelKey, Err: context.DeadlineExceeded}
	err := transportError("gemini", ue)
	if strings.Contains(err.Error(), sentinelKey) {
		t.Fatalf("transportError leaks the key: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "gemini: Post https://example.com/embed?[redacted]") {
		t.Fatalf("transportError = %v; want the provider, op, redacted URL and the deadline cause", err)
	}
}
