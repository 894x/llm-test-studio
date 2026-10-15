// Package runnerartifacts validates and caches explicit Linux runner releases.
package runnerartifacts

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const (
	ManifestName     = "runner-manifest.json"
	CurrentFormat    = 1
	MaxManifestBytes = 16 << 10
	MaxArtifactBytes = 256 << 20
)

var (
	ErrInvalid     = errors.New("invalid_runner_release")
	ErrUnsupported = errors.New("runner_release_upgrade_required")
	ErrUnavailable = errors.New("runner_release_unavailable")
	ErrIntegrity   = errors.New("runner_release_integrity_failed")
	releasePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,127}$`)
)

type Entry struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type Manifest struct {
	Format       int     `json:"format"`
	Contract     int     `json:"contract"`
	Release      string  `json:"release"`
	SourceCommit string  `json:"source_commit"`
	SourceDirty  bool    `json:"source_dirty"`
	Entries      []Entry `json:"entries"`
}

func ValidRelease(release string) bool { return releasePattern.MatchString(release) }

func FileName(arch string) (string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", ErrUnsupported
	}
	return "llm-test-runner-linux-" + arch, nil
}

func (manifest Manifest) Validate() error {
	if manifest.Format != CurrentFormat || manifest.Contract != jobcontract.CurrentContract {
		return ErrUnsupported
	}
	validCommit := len(manifest.SourceCommit) == 40 && strings.ToLower(manifest.SourceCommit) == manifest.SourceCommit
	if _, err := hex.DecodeString(manifest.SourceCommit); err != nil {
		validCommit = false
	}
	if !ValidRelease(manifest.Release) || !validCommit {
		return ErrInvalid
	}
	if len(manifest.Entries) != 2 {
		return ErrInvalid
	}
	for index, arch := range []string{"amd64", "arm64"} {
		entry := manifest.Entries[index]
		name, _ := FileName(arch)
		validIdentity := entry.OS == "linux" && entry.Arch == arch && entry.FileName == name
		validContent := entry.Size > 0 && entry.Size <= MaxArtifactBytes && jobcontract.IsSHA256(entry.SHA256)
		if !validIdentity || !validContent {
			return ErrInvalid
		}
	}
	return nil
}

// Release manifests are generated, owned documents with one current shape.
// Canonical comparison rejects missing, duplicate, case-changed and extra fields.
func DecodeManifest(document []byte) (Manifest, error) {
	var manifest Manifest
	if len(document) > MaxManifestBytes || json.Unmarshal(document, &manifest) != nil {
		return Manifest{}, ErrInvalid
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, document) != nil {
		return Manifest{}, ErrInvalid
	}
	if !bytes.Equal(canonical, compact.Bytes()) {
		return Manifest{}, ErrInvalid
	}
	return manifest, nil
}
