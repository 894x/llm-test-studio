package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testService(t *testing.T, data []byte) (*Service, *release) {
	t.Helper()
	s, err := New("v0.2.0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.platform, s.view.Status, s.view.InstallKind = "windows-amd64.zip", "idle", "restart"
	name := "llm-test-studio-v0.3.0-windows-amd64.zip"
	hash := sha256.Sum256(data)
	checksum := fmt.Sprintf("%x  ./%s\n", hash, name)
	baseURL := "https://github.com/" + repository + "/releases/download/v0.3.0/"
	latest := &release{Tag: "v0.3.0", Notes: "Release notes", Assets: []asset{
		{Name: name, URL: baseURL + name, Size: int64(len(data)), Digest: fmt.Sprintf("sha256:%x", hash)},
		{Name: "SHA256SUMS.txt", URL: baseURL + "SHA256SUMS.txt", Size: int64(len(checksum))},
	}}
	s.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		var body []byte
		switch req.URL.String() {
		case "https://api.github.com/repos/" + repository + "/releases/latest":
			body, _ = json.Marshal(latest)
		case baseURL + name:
			body = data
		case baseURL + "SHA256SUMS.txt":
			body = []byte(checksum)
		default:
			t.Errorf("unexpected update URL %s", req.URL)
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	t.Cleanup(s.Close)
	return s, latest
}

func waitDownload(t *testing.T, s *Service) View {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view := s.Snapshot()
		if view.Status != "downloading" {
			return view
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("download did not finish")
	return View{}
}

func TestStableVersionComparison(t *testing.T) {
	for _, test := range []struct {
		candidate, current string
		want               bool
	}{
		{"v0.10.0", "v0.9.9", true}, {"v1.0.0", "v0.99.99", true},
		{"v0.2.0", "v0.2.0", false}, {"v0.1.9", "v0.2.0", false},
		{"v0.3.0-beta.1", "v0.2.0", false}, {"v0.0.01", "v0.0.0", false},
		{"v1.0.0", "dev", false}, {"v18446744073709551616.0.0", "v1.0.0", false},
	} {
		if got := newer(test.candidate, test.current); got != test.want {
			t.Errorf("newer(%q, %q) = %v", test.candidate, test.current, got)
		}
	}
}

func TestRejectsUntrustedURLsAndRedirects(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/a", "https://github.com.evil.example/a", "https://user:secret@github.com/a",
		"https://github.com:444/a", "https://127.0.0.1/a", "https://github.com/a#fragment",
	} {
		if trustedURL(raw) {
			t.Errorf("trusted unsafe URL %q", raw)
		}
	}
	client := newHTTPClient()
	req, _ := http.NewRequest(http.MethodGet, "https://evil.example/package", nil)
	if client.CheckRedirect(req, nil) == nil {
		t.Fatal("accepted untrusted redirect")
	}
}

func TestReleaseRejectsMissingAmbiguousAndUntrustedAssets(t *testing.T) {
	for _, mutate := range []func(*release){
		func(r *release) { r.Assets = r.Assets[:1] },
		func(r *release) { r.Assets = append(r.Assets, r.Assets[0]) },
		func(r *release) {
			r.Assets[0].URL = "https://github.com/attacker/project/releases/download/v0.3.0/a.zip"
		},
		func(r *release) { r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64) },
		func(r *release) { r.Prerelease = true },
		func(r *release) { r.Draft = true },
		func(r *release) { r.Tag = "v0.03.0" },
	} {
		s, latest := testService(t, []byte("package"))
		mutate(latest)
		if view := s.Check(context.Background()); view.Status != "error" || s.selected != nil {
			t.Fatalf("invalid release accepted: %+v", view)
		}
	}
}

func TestDownloadVerifiesPackageAndPreferencesSurviveReopen(t *testing.T) {
	data := []byte("verified package bytes")
	s, _ := testService(t, data)
	if view := s.Check(context.Background()); view.Status != "available" || view.LatestVersion != "v0.3.0" {
		t.Fatalf("check = %+v", view)
	}
	s.Download(context.Background())
	if view := waitDownload(t, s); view.Status != "ready" || view.Downloaded != int64(len(data)) {
		t.Fatalf("download = %+v", view)
	}
	stored, err := os.ReadFile(s.staged)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("staged package differs")
	}
	if view := s.SetPreferences(false, true); view.ErrorCode != "" || view.Status != "skipped" {
		t.Fatalf("preferences = %+v", view)
	}
	reopened, err := New("v0.2.0", s.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if view := reopened.Snapshot(); view.AutoCheck || view.SkippedVersion != "v0.3.0" {
		t.Fatalf("reopened preferences = %+v", view)
	}
}

func TestDownloadTamperAndTruncationNeverBecomeInstallable(t *testing.T) {
	for _, mutate := range []func(*http.Response){
		func(r *http.Response) { r.Body = io.NopCloser(strings.NewReader("tampered bytes!")) },
		func(r *http.Response) { r.ContentLength-- },
	} {
		s, _ := testService(t, []byte("original bytes!"))
		s.Check(context.Background())
		original := s.client.Transport
		s.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
			response, err := original.RoundTrip(req)
			mutate(response)
			return response, err
		})
		s.Download(context.Background())
		if view := waitDownload(t, s); view.Status != "error" || view.ErrorCode != "integrity" || s.staged != "" {
			t.Fatalf("unsafe download = %+v", view)
		}
		entries, _ := os.ReadDir(s.directory)
		if len(entries) != 0 {
			t.Fatal("failed download left files")
		}
	}
}

func TestCancelledDownloadCanRetryAndCloseCancelsCheck(t *testing.T) {
	s, _ := testService(t, []byte("package"))
	s.Check(context.Background())
	original := s.client.Transport
	started := make(chan struct{})
	s.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	s.Download(context.Background())
	<-started
	s.Cancel()
	if view := waitDownload(t, s); view.Status != "available" || view.ErrorCode != "" {
		t.Fatalf("cancel = %+v", view)
	}
	s.client.Transport = original
	s.Download(context.Background())
	if view := waitDownload(t, s); view.Status != "ready" {
		t.Fatalf("retry = %+v", view)
	}
	started = make(chan struct{})
	s.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	done := make(chan struct{})
	go func() { s.Check(context.Background()); close(done) }()
	<-started
	s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel update check")
	}
}

func TestUnknownPreferencesRejectedWithoutMutation(t *testing.T) {
	for _, document := range []string{
		`{"schema_version":2,"auto_check":true,"skipped_version":""}`,
		`{"schema_version":1,"auto_check":true,"skipped_version":"","secret":"x"}`,
		`{"schema_version":1,"auto_check":true,"skipped_version":"v0.0.01"}`,
		`not JSON`,
		`{"schema_version":1}`,
	} {
		directory := t.TempDir()
		path := filepath.Join(directory, "preferences.json")
		if err := os.WriteFile(path, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New("v0.2.0", directory); err == nil {
			t.Fatal("accepted unknown preferences")
		}
		data, _ := os.ReadFile(path)
		if string(data) != document {
			t.Fatal("modified rejected preferences")
		}
	}
}

func TestArchiveTraversalAndExtraFilesRejected(t *testing.T) {
	for _, names := range [][]string{{"../payload.exe"}, {"llm-test-studio.exe", "extra.exe"}, {"sub/llm-test-studio.exe"}} {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for _, name := range names {
			entry, _ := writer.Create(name)
			_, _ = entry.Write([]byte("MZpayload"))
		}
		_ = writer.Close()
		directory := t.TempDir()
		path := filepath.Join(directory, "release.zip")
		_ = os.WriteFile(path, buffer.Bytes(), 0600)
		if _, err := extractExecutable(path, directory); err == nil {
			t.Fatal("accepted unexpected archive layout")
		}
	}
}

func TestPortableReplacementPreservesOldBinaryAndRestoresOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		directory := t.TempDir()
		target := filepath.Join(directory, "llm-test-studio.exe")
		payloadDirectory := filepath.Join(directory, "install-1")
		_ = os.Mkdir(payloadDirectory, 0700)
		payload := filepath.Join(payloadDirectory, "payload.exe")
		_ = os.WriteFile(target, []byte("old app"), 0700)
		_ = os.WriteFile(payload, []byte("new app"), 0700)
		if fail {
			_ = os.WriteFile(target+".update", []byte("existing user file"), 0600)
		}
		err := replaceExecutable(installPlan{Target: target, Payload: payload})
		data, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if fail {
			if err == nil || string(data) != "old app" {
				t.Fatal("failed replacement did not restore old application")
			}
		} else {
			backup, _ := os.ReadFile(target + ".previous-install-1")
			if err != nil || string(data) != "new app" || string(backup) != "old app" {
				t.Fatal("replacement lost application or backup")
			}
		}
	}
}

func TestInstallRechecksStagedDigestAndFinalizeIsOneShot(t *testing.T) {
	s, _ := testService(t, []byte("verified"))
	s.Check(context.Background())
	s.Download(context.Background())
	waitDownload(t, s)
	_ = os.WriteFile(s.staged, []byte("changed"), 0600)
	if view := s.Install(); view.Status != "error" || view.ErrorCode != "integrity" {
		t.Fatalf("tampered staged package accepted: %+v", view)
	}
	var commits atomic.Int32
	s.commit = func() error { commits.Add(1); return nil }
	_ = s.Finalize()
	_ = s.Finalize()
	if commits.Load() != 1 {
		t.Fatal("shutdown authorized installation more than once")
	}
}
