package jobhost_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type diagnosticFailureRepository struct {
	*sqlite.Repository
	failure error
}

func (repository diagnosticFailureRepository) AppendResults(context.Context, ...domain.Result) error {
	return fmt.Errorf("injected result write failure: %w", repository.failure)
}

func TestCoreRetainsResultPersistenceDiagnosticAndDrainsOnClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	failure := errors.New("result storage sentinel")
	diagnostics := []runs.Diagnostic{}
	core, err := jobhost.NewCore(jobhost.CoreDependencies{
		Repository: diagnosticFailureRepository{Repository: repository, failure: failure},
		Runs:       runs.NewProtocolExecutor(http.DefaultTransport), Transport: http.DefaultTransport, Clock: wallClock{},
		ReportDiagnostic: func(diagnostic runs.Diagnostic) {
			diagnostics = append(diagnostics, diagnostic)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	submission := coreSubmission(t, server.URL, false)
	defer submission.Clear()
	spec := submission.Job
	if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
		ContentClaims: []jobcontent.Claim{}, Spec: spec, Origin: acceptedOrigin(spec),
		SecretCommitment: strings.Repeat("d", 64), AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease(submission.Secrets[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	_, err = core.Execute(t.Context(), jobhost.Execution{
		Spec: spec, Secrets: map[string]*credentials.Lease{spec.Spec.Run.Meta.ID: lease},
	})
	if !errors.Is(err, jobhost.ErrUnavailable) {
		t.Fatalf("result persistence failure = %v, want unavailable", err)
	}
	// Close joins execution and drains the asynchronous diagnostic callback.
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.ErrorCode != "result_persistence_failed" {
			continue
		}
		matched++
		if diagnostic.RunID != spec.Spec.Run.Meta.ID || diagnostic.Operation != "persist_suite_result" {
			t.Fatalf("diagnostic lost execution identity: %+v", diagnostic)
		}
		if !errors.Is(diagnostic.Err, failure) {
			t.Fatalf("diagnostic lost original storage error: %v", diagnostic.Err)
		}
	}
	if matched != 1 {
		t.Fatalf("result persistence diagnostics = %d, want one", matched)
	}
}
