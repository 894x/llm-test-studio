package runnerartifacts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGitHubSourceFetchesExactReleaseAndRejectsUnsafeRedirects(t *testing.T) {
	requests := []string{}
	var mutex sync.Mutex
	requestCount := func() int { mutex.Lock(); defer mutex.Unlock(); return len(requests) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		requests = append(requests, request.URL.Path)
		mutex.Unlock()
		switch request.URL.Path {
		case "/v1.2.3+fixture/runner-manifest.json":
			_, _ = io.WriteString(writer, "manifest")
		case "/downgrade/runner-manifest.json":
			http.Redirect(
				writer,
				request,
				"http://invalid.test/binary",
				http.StatusFound,
			)
		case "/loop/runner-manifest.json":
			http.Redirect(
				writer,
				request,
				request.URL.String(),
				http.StatusFound,
			)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	source, err := NewGitHubSource("894x/llm-test-studio")
	if err != nil {
		t.Fatal(err)
	}
	source.base = server.URL + "/"
	source.client.Transport = server.Client().Transport
	reader, err := source.Fetch(context.Background(), "v1.2.3+fixture", ManifestName)
	if err != nil {
		t.Fatal(err)
	}
	document, err := io.ReadAll(reader)
	_ = reader.Close()
	validResponse := string(document) == "manifest" && requestCount() == 1
	if err != nil || !validResponse {
		t.Fatal("release request changed")
	}
	for _, release := range []string{"downgrade", "loop", "missing"} {
		reader, err := source.Fetch(context.Background(), release, ManifestName)
		if reader != nil || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unsafe or missing release accepted: %v", err)
		}
	}
	before := requestCount()
	if _, err := source.Fetch(context.Background(), "../escape", ManifestName); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := source.Fetch(context.Background(), "v1", "../escape"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if requestCount() != before {
		t.Fatal("invalid source request reached HTTP")
	}
	for _, repository := range []string{
		"../repo", "owner/../repo", "owner/repo?token=secret", "https://invalid.test/repo",
	} {
		if _, err := NewGitHubSource(repository); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid repository accepted")
		}
	}
}

func TestGitHubSourceCancellationClosesPendingRequest(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		close(exited)
	}))
	defer server.Close()
	source, _ := NewGitHubSource("894x/llm-test-studio")
	source.base = server.URL + "/"
	source.client.Transport = server.Client().Transport
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		reader, err := source.Fetch(ctx, "v1", ManifestName)
		if reader != nil {
			_ = reader.Close()
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("request never reached the test server")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled fetch did not return")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not leave the server")
	}
}

func TestDirectorySourceRejectsTraversalAndLinks(t *testing.T) {
	root := t.TempDir()
	source, err := NewDirectorySource(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Fetch(context.Background(), "v1", "../secret"); !errors.Is(err, ErrInvalid) {
		t.Fatal("directory traversal accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(strings.Repeat("x", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ManifestName)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := source.Fetch(context.Background(), "v1", ManifestName); !errors.Is(err, ErrIntegrity) {
		t.Fatal("source followed a symlink")
	}
}
