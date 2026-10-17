package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

func credentialCleanupRegistryPath(configurationDirectory, authoredCatalogRoot string) (string, error) {
	configurationDirectory = filepath.Clean(strings.TrimSpace(configurationDirectory))
	if configurationDirectory == "." || !filepath.IsAbs(configurationDirectory) {
		return "", errors.New("credential cleanup registry paths must be absolute")
	}
	scope, err := CredentialScopeForRoot(authoredCatalogRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(configurationDirectory, "credential-keyring-registries", scope+".json"), nil
}

func CredentialScopeForRoot(root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || !filepath.IsAbs(root) {
		return "", errors.New("credential storage root must be an absolute path")
	}
	canonicalRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		canonicalRoot = filepath.Clean(resolved)
	}
	if runtime.GOOS == "windows" {
		canonicalRoot = strings.ToLower(canonicalRoot)
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonicalRoot)))
	return hex.EncodeToString(digest[:]), nil
}
