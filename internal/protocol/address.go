package protocol

import (
	"errors"
	"net/url"
	"strings"
)

var ErrInvalidAddress = errors.New("invalid service address")

type ResolvedAddress struct {
	Endpoint string
	Suffix   string
}

// ResolveAddress accepts a service root, a partially entered route, or a complete
// endpoint. Known sibling endpoints share the same service prefix (for example,
// a video submit URL must still allow polling its task).
func ResolveAddress(address, requestPath string, knownPaths []RequestPath) (ResolvedAddress, error) {
	parsed, err := url.Parse(address)
	invalidInput := address == "" || address != strings.TrimSpace(address) || strings.Contains(address, "\\")
	if err != nil || invalidInput {
		return ResolvedAddress{}, ErrInvalidAddress
	}
	invalidURL := parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil
	unsafeURL := parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || strings.Contains(address, "#")
	if invalidURL || unsafeURL || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ResolvedAddress{}, ErrInvalidAddress
	}
	path, err := url.Parse(requestPath)
	if err != nil || path.IsAbs() || path.Host != "" || !strings.HasPrefix(requestPath, "/") {
		return ResolvedAddress{}, ErrInvalidAddress
	}
	unsafePath := strings.ContainsAny(requestPath, "?#\\") || strings.HasPrefix(requestPath, "//")
	if unsafePath || requestPath == "/" {
		return ResolvedAddress{}, ErrInvalidAddress
	}
	trimmed := strings.TrimRight(address, "/")
	inputPath := strings.TrimRight(parsed.EscapedPath(), "/")
	endpoint := ""
	for _, candidate := range addressPaths(requestPath) {
		if strings.HasSuffix(inputPath, candidate) {
			endpoint = trimmed
			break
		}
	}
	if endpoint == "" {
		for _, known := range knownPaths {
			for _, candidate := range addressPaths(known.Path) {
				if strings.HasSuffix(inputPath, candidate) {
					endpoint = strings.TrimSuffix(trimmed, candidate) + requestPath
					break
				}
			}
			if endpoint != "" {
				break
			}
		}
	}
	if endpoint == "" {
		completion := requestPath
		overlap := 0
		for _, candidate := range addressPaths(requestPath) {
			for size := min(len(inputPath), len(candidate)-1); size > overlap; size-- {
				if strings.HasSuffix(inputPath, candidate[:size]) {
					completion, overlap = candidate[size:], size
					break
				}
			}
		}
		endpoint = trimmed + completion
	}
	suffix := ""
	if strings.HasPrefix(endpoint, address) {
		suffix = strings.TrimPrefix(endpoint, address)
	}
	return ResolvedAddress{Endpoint: endpoint, Suffix: suffix}, nil
}

func addressPaths(path string) []string {
	paths := []string{path}
	// OpenAI-compatible providers currently expose both of these wire routes.
	if strings.HasPrefix(path, "/v1/") {
		paths = append(paths, strings.TrimPrefix(path, "/v1"))
	}
	return paths
}
