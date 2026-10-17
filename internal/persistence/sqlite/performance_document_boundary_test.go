package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type performanceBoundaryTransport struct{}

func (performanceBoundaryTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic boundary response"}}`)),
	}, nil
}

func maximalPerformanceCheckpoint(t *testing.T) (quicktest.PreparedPerformance, quicktest.PerformanceCheckpoint) {
	t.Helper()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "boundary-model", APIKey: "synthetic-transport-only",
			RequestCount: quicktest.MaxPerformanceRequests, Concurrency: quicktest.MaxPerformanceConcurrency,
			TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	defer lease.Close()
	host := quicktest.NewHost(quicktest.HostDependencies{Transport: performanceBoundaryTransport{}})
	report, err := host.ExecutePrepared(
		t.Context(),
		prepared,
		lease,
		quicktest.ExecutionOptions{},
	)
	if err != nil || len(report.Samples) != int(quicktest.MaxPerformanceRequests) {
		t.Fatalf("synthetic measured report: samples=%d error=%v", len(report.Samples), err)
	}
	// Keep every evidence field within its current limits. Opaque request IDs
	// and redacted response bodies are owned data, not unknown fields or padding.
	for index := range report.Samples {
		evidence := &quicktest.PerformanceResponseEvidence{
			CaptureStatus: quicktest.PerformanceEvidenceOmitted, RequestID: "r",
			ContentType: "text/plain; boundary=r", BodyBytes: 1, Truncated: true, Redacted: true,
		}
		if index < quicktest.MaxPerformanceEvidenceTotalBytes/quicktest.MaxPerformanceEvidenceBodyBytes {
			evidence.CaptureStatus = quicktest.PerformanceEvidenceCaptured
			evidence.Body = strings.Repeat("<", quicktest.MaxPerformanceEvidenceBodyBytes)
			evidence.BodyBytes, evidence.Truncated = uint64(len(evidence.Body)), false
		}
		report.Samples[index].ResponseEvidence = evidence
	}
	checkpoint := quicktest.PerformanceCheckpoint{
		Stage: quicktest.PerformanceStageMeasured, Boundary: quicktest.PerformanceBoundaryReturned, Report: report,
	}
	document, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	remaining := quicktest.MaxPerformanceCheckpointBytes - len(document)
	if remaining <= 0 {
		t.Fatal("bounded evidence fixture already exceeds the checkpoint budget")
	}
	for _, sample := range checkpoint.Report.Samples {
		evidence := sample.ResponseEvidence
		count := min(remaining/6, 256-len(evidence.RequestID))
		evidence.RequestID += strings.Repeat("<", count)
		remaining -= count * 6 // encoding/json escapes each '<' as six bytes.
		if remaining < 6 && remaining <= 256-len(evidence.RequestID) {
			evidence.RequestID += strings.Repeat("r", remaining)
			remaining = 0
		}
		if remaining == 0 {
			break
		}
	}
	for _, sample := range checkpoint.Report.Samples {
		evidence := sample.ResponseEvidence
		count := min(remaining, 128-len(evidence.ContentType))
		evidence.ContentType += strings.Repeat("r", count)
		remaining -= count
		if remaining == 0 {
			break
		}
	}
	document, err = json.Marshal(checkpoint)
	exactCheckpointSize := remaining == 0 && len(document) == quicktest.MaxPerformanceCheckpointBytes
	if err != nil || !exactCheckpointSize {
		t.Fatalf(
			"legal checkpoint construction: bytes=%d remaining=%d error=%v",
			len(document),
			remaining,
			err,
		)
	}
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("maximum checkpoint is not a valid current measured report: %v", err)
	}
	return prepared, checkpoint
}

func TestCompletePerformanceDocumentsReadMaximalLegalCheckpointAndRejectWireBudgets(t *testing.T) {
	prepared, checkpoint := maximalPerformanceCheckpoint(t)
	_, fixture := storedPerformancePageFixture(t)
	spec := fixture.Spec
	spec.Spec.Performance = &prepared
	if _, err := spec.Freeze(); err != nil {
		t.Fatalf("large report's owner cannot pass real submission freezing: %v", err)
	}
	path := filepath.Join(t.TempDir(), "performance-document-boundaries.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "performance-document-boundary"}); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	current, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
		Spec: spec, Origin: fixture.Execution.Receipt.Origin, SecretCommitment: strings.Repeat("b", 64),
		AcceptedAt: at, ContentClaims: []jobcontent.Claim{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCancelled} {
		current, err = repository.ChangeJob(t.Context(), jobhost.Change{
			JobID: spec.JobID, ExpectedRevision: current.Receipt.Revision,
			Status: status, At: at.Add(time.Duration(index+1) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	observation := jobcontract.PerformanceObservation{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, Revision: math.MaxInt64,
		// Use the longest current JSON timestamp and revision representations.
		RecordedAt: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), Checkpoint: checkpoint,
	}
	if err := observation.ValidateFor(spec); err != nil {
		t.Fatal(err)
	}
	observationDocument, err := marshalCanonical(observation)
	if err != nil {
		t.Fatal(err)
	}
	// Seed only this read fixture's complete canonical row. The actual query
	// decoder and result writer below remain production implementations.
	if _, err := repository.db.ExecContext(
		t.Context(),
		`INSERT INTO execution_performance_observations(job_id, revision, recorded_at, document_json) VALUES(?, ?, ?, ?)`,
		spec.JobID,
		observation.Revision,
		formatTime(observation.RecordedAt),
		observationDocument,
	); err != nil {
		t.Fatal(err)
	}
	decoded, err := repository.GetPerformanceObservation(t.Context(), spec.JobID)
	completeSamples := len(decoded.Checkpoint.Report.Samples) == len(checkpoint.Report.Samples)
	validObservation := decoded.ValidateFor(spec) == nil && completeSamples
	if err != nil || !validObservation {
		t.Fatalf("complete maximal observation did not survive actual read: %v", err)
	}
	sealed, err := repository.SealPerformanceResult(t.Context(), spec.JobID, observation.RecordedAt)
	if err != nil {
		t.Fatalf("complete maximal result could not be encoded and sealed: %v", err)
	}
	read, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
	validResult := bytes.Equal(read.Document, sealed.Document) && read.Result.Validate() == nil
	if err != nil || !validResult {
		t.Fatalf("complete maximal result did not survive actual read: %v", err)
	}
	// The outer constants reserve space for metadata. A maximum legal inner
	// checkpoint is smaller than both outer ceilings; do not claim otherwise.
	withinReservedCeilings := len(observationDocument) < jobcontract.MaxPerformanceObservationBytes &&
		len(sealed.Document) < jobcontract.MaxPerformanceResultBytes
	if !withinReservedCeilings {
		t.Fatal("outer metadata reserve no longer exceeds this maximal checkpoint fixture")
	}
	t.Logf(
		"checkpoint=%d observation=%d observation_limit=%d result=%d result_limit=%d",
		quicktest.MaxPerformanceCheckpointBytes,
		len(observationDocument),
		jobcontract.MaxPerformanceObservationBytes,
		len(sealed.Document),
		jobcontract.MaxPerformanceResultBytes,
	)
	for _, test := range []struct {
		name      string
		document  []byte
		limit     int
		update    string
		selectRow string
		read      func() error
	}{
		{
			name: "observation", document: observationDocument, limit: jobcontract.MaxPerformanceObservationBytes,
			update:    `UPDATE execution_performance_observations SET document_json = ? WHERE job_id = ?`,
			selectRow: `SELECT document_json FROM execution_performance_observations WHERE job_id = ?`,
			read:      func() error { _, err := repository.GetPerformanceObservation(t.Context(), spec.JobID); return err },
		},
		{
			name: "result", document: sealed.Document, limit: jobcontract.MaxPerformanceResultBytes,
			update:    `UPDATE execution_performance_results SET document_json = ? WHERE job_id = ?`,
			selectRow: `SELECT document_json FROM execution_performance_results WHERE job_id = ?`,
			read:      func() error { _, err := repository.ReadPerformanceResult(t.Context(), spec.JobID); return err },
		},
	} {
		for _, extra := range []int{0, 1} {
			t.Run(test.name+"/noncanonical-budget+"+strconv.Itoa(extra), func(t *testing.T) {
				// These are explicitly invalid noncanonical stored documents, not
				// purported valid entities at the reserved outer byte ceilings.
				invalid := append(
					bytes.Clone(test.document),
					bytes.Repeat([]byte(" "), test.limit+extra-len(test.document))...,
				)
				if _, err := repository.db.ExecContext(
					t.Context(),
					test.update,
					invalid,
					spec.JobID,
				); err != nil {
					t.Fatal(err)
				}
				before := sha256.Sum256(invalid)
				var changesBefore, changesAfter int64
				if err := repository.db.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changesBefore); err != nil {
					t.Fatal(err)
				}
				if err := test.read(); !errors.Is(err, ErrCorrupt) {
					t.Fatalf("invalid complete document error = %v; want ErrCorrupt", err)
				}
				var after []byte
				if err := repository.db.QueryRowContext(t.Context(), test.selectRow, spec.JobID).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err := repository.db.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changesAfter); err != nil {
					t.Fatal(err)
				}
				unchangedBytes := len(after) == len(invalid) && sha256.Sum256(after) == before
				if !unchangedBytes || changesAfter != changesBefore {
					t.Fatal("rejecting a complete document changed the stored input or wrote other rows")
				}
			})
		}
		if _, err := repository.db.ExecContext(
			t.Context(),
			test.update,
			test.document,
			spec.JobID,
		); err != nil {
			t.Fatal(err)
		}
	}
}
