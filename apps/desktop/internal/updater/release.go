package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const repository = "894x/llm-test-studio"
const maxPackageSize int64 = 512 << 20

var stableVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func versionParts(version string) ([3]uint64, bool) {
	var parts [3]uint64
	match := stableVersion.FindStringSubmatch(version)
	if match == nil {
		return parts, false
	}
	for i := range parts {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

func newer(candidate, current string) bool {
	a, aOK := versionParts(candidate)
	b, bOK := versionParts(current)
	if !aOK || !bOK {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Notes      string  `json:"body"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type packageInfo struct {
	Version string
	Notes   string
	Asset   asset
	SHA256  string
}

func trustedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !trustedURL(req.URL.String()) {
				return errors.New("untrusted update redirect")
			}
			return nil
		},
	}
}

func get(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	if !trustedURL(rawURL) {
		return nil, errors.New("untrusted update URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "llm-test-studio-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("update HTTP status %d", resp.StatusCode)
	}
	return resp, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("update document too large")
	}
	return data, nil
}

func findPackage(ctx context.Context, client *http.Client, current, platform string) (*packageInfo, error) {
	resp, err := get(ctx, client, "https://api.github.com/repos/"+repository+"/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	var latest release
	if err := json.Unmarshal(data, &latest); err != nil {
		return nil, err
	}
	if latest.Draft || latest.Prerelease {
		return nil, errors.New("release is not stable")
	}
	if _, ok := versionParts(latest.Tag); !ok {
		return nil, errors.New("release version is invalid")
	}
	if !newer(latest.Tag, current) {
		return nil, nil
	}
	name := "llm-test-studio-" + latest.Tag + "-" + platform
	var selected, checksums *asset
	for _, item := range latest.Assets {
		if item.Name != name && item.Name != "SHA256SUMS.txt" {
			continue
		}
		expectedURL := "https://github.com/" + repository + "/releases/download/" + latest.Tag + "/" + item.Name
		if item.URL != expectedURL || item.Size < 1 || item.Size > maxPackageSize {
			return nil, errors.New("invalid update asset")
		}
		if item.Name == name {
			if selected != nil {
				return nil, errors.New("duplicate update asset")
			}
			selected = &item
		} else {
			if checksums != nil {
				return nil, errors.New("duplicate checksum asset")
			}
			checksums = &item
		}
	}
	if selected == nil || checksums == nil || checksums.Size > 64<<10 {
		return nil, errors.New("release assets incomplete")
	}
	checksumResponse, err := get(ctx, client, checksums.URL)
	if err != nil {
		return nil, err
	}
	defer checksumResponse.Body.Close()
	checksumData, err := readBounded(checksumResponse.Body, 64<<10)
	if err != nil {
		return nil, err
	}
	if int64(len(checksumData)) != checksums.Size {
		return nil, errors.New("checksum document truncated")
	}
	want := ""
	for _, line := range strings.Split(string(checksumData), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") != selected.Name {
			continue
		}
		if want != "" {
			return nil, errors.New("duplicate package checksum")
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("invalid package checksum")
		}
		want = strings.ToLower(fields[0])
	}
	if want == "" || (selected.Digest != "" && selected.Digest != "sha256:"+want) {
		return nil, errors.New("package checksum mismatch")
	}
	if len(latest.Notes) > 32<<10 {
		latest.Notes = string([]rune(latest.Notes)[:8192])
	}
	return &packageInfo{Version: latest.Tag, Notes: latest.Notes, Asset: *selected, SHA256: want}, nil
}
