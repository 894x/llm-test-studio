package runnerartifacts

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Source interface {
	Fetch(context.Context, string, string) (io.ReadCloser, error)
}

type GitHubSource struct {
	base   string
	client *http.Client
}

var repositoryPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_.-]{0,99}/[0-9A-Za-z][0-9A-Za-z_.-]{0,99}$`)

func NewGitHubSource(repository string) (*GitHubSource, error) {
	if !repositoryPattern.MatchString(repository) {
		return nil, ErrInvalid
	}
	client := &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			valid := request.URL.Scheme == "https" && request.URL.User == nil && len(via) < 5
			if !valid {
				return ErrUnavailable
			}
			return nil
		},
	}
	return &GitHubSource{base: "https://github.com/" + repository + "/releases/download/", client: client}, nil
}

func (source *GitHubSource) Fetch(ctx context.Context, release, name string) (io.ReadCloser, error) {
	if !validSourceRequest(ctx, release, name) {
		return nil, ErrInvalid
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		source.base+url.PathEscape(release)+"/"+name,
		nil,
	)
	if err != nil {
		return nil, ErrInvalid
	}
	response, err := source.client.Do(request)
	if err != nil {
		return nil, sourceFailure(ctx)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, ErrUnavailable
	}
	return response.Body, nil
}

// DirectorySource consumes a locally built release explicitly selected by the
// developer. It never builds on the desktop or executes a downloaded program.
type DirectorySource struct{ root string }

func NewDirectorySource(root string) (*DirectorySource, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, ErrInvalid
	}
	return &DirectorySource{root: root}, nil
}

func (source *DirectorySource) Fetch(ctx context.Context, release, name string) (io.ReadCloser, error) {
	if !validSourceRequest(ctx, release, name) {
		return nil, ErrInvalid
	}
	root, err := os.Lstat(source.root)
	if err != nil || !root.IsDir() {
		return nil, ErrUnavailable
	}
	return openRegular(filepath.Join(source.root, name))
}

func validSourceRequest(ctx context.Context, release, name string) bool {
	knownName := name == ManifestName || name == "llm-test-runner-linux-amd64" || name == "llm-test-runner-linux-arm64"
	return ctx != nil && ValidRelease(release) && knownName
}

func sourceFailure(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}

func openRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrIntegrity
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, ErrIntegrity
	}
	return file, nil
}
