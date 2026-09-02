package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenWritesStructuredRedactedDiagnosticEvent(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{
		Directory:  directory,
		Filename:   "llm-test-studio.log",
		AppVersion: "test-version",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	logger.Record(context.Background(), Event{
		Level:        LevelError,
		Message:      "operation failed",
		Component:    "desktop",
		Operation:    "startup",
		ErrorCode:    "startup_failed",
		RunID:        "run-1",
		RequestID:    "request-1",
		ReportID:     "report-1",
		Duration:     1250 * time.Millisecond,
		DroppedCount: 3,
		FailureCount: 2,
		Err: errors.New(
			"connect Authorization: Bearer bearer-secret api_key=key-secret sk-secret-token",
		),
	})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(directory, "llm-test-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, secret := range []string{"bearer-secret", "key-secret", "sk-secret-token"} {
		if strings.Contains(string(contents), secret) {
			t.Fatalf("diagnostic log leaked %q: %s", secret, contents)
		}
	}

	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v; log = %s", err, contents)
	}
	want := map[string]any{
		"level":         "ERROR",
		"msg":           "operation failed",
		"app_version":   "test-version",
		"component":     "desktop",
		"operation":     "startup",
		"error_code":    "startup_failed",
		"run_id":        "run-1",
		"request_id":    "request-1",
		"report_id":     "report-1",
		"duration_ms":   float64(1250),
		"dropped_count": float64(3),
		"failure_count": float64(2),
	}
	for key, expected := range want {
		if got := entry[key]; got != expected {
			t.Errorf("entry[%q] = %#v, want %#v", key, got, expected)
		}
	}
	if got, _ := entry["error"].(string); got == "" || !strings.Contains(got, "[REDACTED]") {
		t.Errorf("entry[error] = %#v, want redacted diagnostic cause", entry["error"])
	}
}

func TestRedactTextCoversCommonCredentialRepresentations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		secrets []string
	}{
		{name: "json quoted values", input: `request={"password":"value with spaces","api_key":"json-key"}`, secrets: []string{"value with spaces", "json-key"}},
		{name: "formatted header map", input: `headers=map[Authorization:[Bearer header-secret]]`, secrets: []string{"header-secret"}},
		{name: "url user info", input: `dial https://alice:s3cret@example.com/v1`, secrets: []string{"alice", "s3cret"}},
		{name: "single quoted assignment", input: `api-key='quoted value with spaces'`, secrets: []string{"quoted value with spaces"}},
		{name: "token variants", input: `key=sk-proj-secret-token Authorization: Bearer eyJhbGciOi.secret.signature`, secrets: []string{"sk-proj-secret-token", "eyJhbGciOi.secret.signature"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redacted := RedactText(test.input)
			for _, secret := range test.secrets {
				if strings.Contains(redacted, secret) {
					t.Errorf("RedactText() leaked %q in %q", secret, redacted)
				}
			}
			if !strings.Contains(redacted, "[REDACTED]") {
				t.Errorf("RedactText() = %q, want redaction marker", redacted)
			}
		})
	}
}

func TestOpenRotatesDiagnosticLogAndRetainsConfiguredBackups(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{
		Directory: directory,
		Filename:  "llm-test-studio.log",
		MaxBytes:  1,
		Backups:   2,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for _, message := range []string{"event-1", "event-2", "event-3", "event-4"} {
		logger.Record(context.Background(), Event{Message: message, Component: "test"})
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	wantMessages := map[string]string{
		"llm-test-studio.log":   "event-4",
		"llm-test-studio.log.1": "event-3",
		"llm-test-studio.log.2": "event-2",
	}
	for name, expected := range wantMessages {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", name, err)
		}
		var entry map[string]any
		if err := json.Unmarshal(contents, &entry); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v; log = %s", name, err, contents)
		}
		if got := entry["msg"]; got != expected {
			t.Errorf("%s msg = %#v, want %q", name, got, expected)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "llm-test-studio.log.3")); !os.IsNotExist(err) {
		t.Fatalf("third backup exists or could not be inspected: %v", err)
	}
}

func TestOpenRejectsConcurrentOwnerAndReleasesOwnershipOnClose(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	first, err := Open(Options{Directory: directory})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	defer first.Close()
	if second, err := Open(Options{Directory: directory}); err == nil {
		_ = second.Close()
		_ = first.Close()
		t.Fatal("second Open() unexpectedly acquired the same diagnostic log")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	third, err := Open(Options{Directory: directory})
	if err != nil {
		t.Fatalf("Open() after release error = %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("third Close() error = %v", err)
	}
}

func TestRotatingLogKeepsWritingWhenBackupCannotBeRemoved(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{Directory: directory, MaxBytes: 1, Backups: 1})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	logger.Record(context.Background(), Event{Message: "event-1"})
	blockedBackup := filepath.Join(directory, "llm-test-studio.log.1")
	if err := os.Mkdir(blockedBackup, 0o700); err != nil {
		t.Fatalf("Mkdir(blocked backup) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedBackup, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocked backup) error = %v", err)
	}
	logger.Record(context.Background(), Event{Message: "event-2"})
	logger.Record(context.Background(), Event{Message: "event-3"})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(directory, "llm-test-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "event-3") {
		t.Fatalf("active log stopped accepting events after rotation failure: %s", contents)
	}
}

func TestRotatingLogRecoversFromInjectedFileOperationFailures(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*rotatingFile)
	}{
		{
			name: "close",
			inject: func(file *rotatingFile) {
				failed := false
				file.ops.close = func(active *os.File) error {
					if !failed {
						failed = true
						_ = active.Close()
						return errors.New("injected close failure")
					}
					return defaultFileOperations.close(active)
				}
			},
		},
		{
			name: "remove",
			inject: func(file *rotatingFile) {
				failed := false
				file.ops.remove = func(path string) error {
					if !failed {
						failed = true
						return errors.New("injected remove failure")
					}
					return defaultFileOperations.remove(path)
				}
			},
		},
		{
			name: "rename",
			inject: func(file *rotatingFile) {
				failed := false
				file.ops.rename = func(from, to string) error {
					if !failed && from == file.path {
						failed = true
						return errors.New("injected rename failure")
					}
					return defaultFileOperations.rename(from, to)
				}
			},
		},
		{
			name: "open",
			inject: func(file *rotatingFile) {
				failed := false
				file.ops.open = func(path string, flag int, mode os.FileMode) (*os.File, error) {
					if !failed && flag&os.O_TRUNC != 0 {
						failed = true
						return nil, errors.New("injected open failure")
					}
					return defaultFileOperations.open(path, flag, mode)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "logs")
			logger, err := Open(Options{Directory: directory, MaxBytes: 1, Backups: 1})
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if err := logger.Record(context.Background(), Event{Message: "event-1"}); err != nil {
				t.Fatalf("first Record() error = %v", err)
			}
			test.inject(logger.sink)
			if err := logger.Record(context.Background(), Event{Message: "event-2"}); err == nil {
				t.Fatal("Record() error = nil, want surfaced rotation failure")
			}
			if err := logger.Record(context.Background(), Event{Message: "event-3"}); err != nil {
				t.Fatalf("Record() after recovery error = %v", err)
			}
			if err := logger.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			contents, err := os.ReadFile(filepath.Join(directory, "llm-test-studio.log"))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if !strings.Contains(string(contents), "event-3") {
				t.Fatalf("active log after recovery = %s, want event-3", contents)
			}
		})
	}
}

func TestLoggerConcurrentRecordAndCloseCompletes(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{Directory: directory, MaxBytes: 1024, Backups: 2})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	start := make(chan struct{})
	var writers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for index := 0; index < 50; index++ {
				_ = logger.Record(context.Background(), Event{Message: "concurrent event"})
			}
		}()
	}
	close(start)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		writers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Record() calls did not finish after Close()")
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	reopened, err := Open(Options{Directory: directory})
	if err != nil {
		t.Fatalf("Open() after concurrent close error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Close() error = %v", err)
	}
}

func TestLoggerConcurrentCloseIsIdempotent(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{Directory: directory})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	results := make(chan error, 16)
	var closers sync.WaitGroup
	for worker := 0; worker < cap(results); worker++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			results <- logger.Close()
		}()
	}
	closers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	reopened, err := Open(Options{Directory: directory})
	if err != nil {
		t.Fatalf("Open() after concurrent Close() error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Close() error = %v", err)
	}
}

func TestRotatingLogRetriesRecoveryAfterReopenFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := Open(Options{Directory: directory, MaxBytes: 1, Backups: 1})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer logger.Close()
	if err := logger.Record(context.Background(), Event{Message: "event-1"}); err != nil {
		t.Fatalf("first Record() error = %v", err)
	}
	removeFailed := false
	logger.sink.ops.remove = func(path string) error {
		if !removeFailed {
			removeFailed = true
			return errors.New("injected remove failure")
		}
		return defaultFileOperations.remove(path)
	}
	reopenFailed := false
	logger.sink.ops.open = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		if !reopenFailed && flag&os.O_APPEND != 0 {
			reopenFailed = true
			return nil, errors.New("injected recovery open failure")
		}
		return defaultFileOperations.open(path, flag, mode)
	}
	if err := logger.Record(context.Background(), Event{Message: "event-2"}); err == nil {
		t.Fatal("second Record() error = nil, want rotation and recovery failure")
	}
	if err := logger.Record(context.Background(), Event{Message: "event-3"}); err != nil {
		t.Fatalf("Record() after obstruction cleared error = %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, "llm-test-studio.log"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "event-3") {
		t.Fatalf("active log did not self-heal: %s", contents)
	}
}
