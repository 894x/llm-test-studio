package apiaudit

import (
	"net/url"
	"strings"
)

// validVideoURL validates an output reference without fetching it or disclosing
// signed query parameters. Provider-specific output contracts remain in drivers.
func validVideoURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return nil, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, false
	}
	return parsed, true
}
