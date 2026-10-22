package embedding

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// urlWithQuery matches an http(s) URL's query string, which may carry a
// credential (an API key passed as ?key=…).
var urlWithQuery = regexp.MustCompile(`(https?://[^\s"?#]+)\?[^\s"#]*`)

// Redact removes the query string from every URL in s. Use it on any
// provider error text bound for a log line or the ledger.
func Redact(s string) string {
	return urlWithQuery.ReplaceAllString(s, "$1?[redacted]")
}

// transportError wraps an HTTP client error for provider without leaking
// the request URL's query string: *url.Error prints the full URL. The
// cause chain is kept, so errors.Is(err, context.DeadlineExceeded) still
// holds.
func transportError(provider string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %s %s: %w", provider, ue.Op, Redact(ue.URL), ue.Err)
	}
	return fmt.Errorf("%s: %w", provider, err)
}
