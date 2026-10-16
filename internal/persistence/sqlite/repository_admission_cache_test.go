package sqlite

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func admissionCacheFixture(t *testing.T) ([]byte, jobcontract.Snapshot) {
	t.Helper()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "https://cache.example.test/v1", ModelID: "fixture", APIKey: "admission-cache-test-secret",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 8, OutputTokens: 4,
	})
	if code != "" {
		t.Fatal(code)
	}
	lease.Close()
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: "85000000-0000-4000-8000-000000000001",
		NodeInstallationID: "85000000-0000-4000-8000-000000000002", EngineBuildID: strings.Repeat("a", 64),
		Kind: jobcontract.KindPerformance, Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal},
		Assets: []jobcontract.Asset{}, Spec: jobcontract.Payload{Performance: &prepared},
	}
	origin := domain.ExecutionOrigin{
		Kind: "local", NodeID: "85000000-0000-4000-8000-000000000003",
		InstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
		RunnerVersion: "cache-runner-1", OS: "linux", Arch: "amd64",
	}
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	snapshot, err := spec.InitialSnapshot(at, origin)
	if err != nil {
		t.Fatal(err)
	}
	event, err := newJobEvent(jobEventWrite{
		JobID: spec.JobID, Kind: jobcontract.EventAccepted, At: at,
		Payload: jobcontract.AcceptedEvent{Spec: spec, Snapshot: snapshot},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	document, err := marshalCanonical(event)
	if err != nil {
		t.Fatal(err)
	}
	return document, snapshot
}

func TestAdmissionValidationCacheUsesCompleteBytesAndNeverCachesErrors(t *testing.T) {
	document, snapshot := admissionCacheFixture(t)
	var cache admissionValidationCache
	first, err := cache.resolve(document)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(document, []byte(`"cache-runner-1"`), []byte(`"cache-runner-2"`), 1)
	if len(changed) != len(document) || bytes.Equal(changed, document) {
		t.Fatal("test must change accepted bytes without changing their length")
	}
	second, err := cache.resolve(changed)
	if err != nil || second.receipt.Origin.RunnerVersion != "cache-runner-2" {
		t.Fatalf("same-length source change reused a cached receipt: %v", err)
	}
	if _, err := decodeAcceptedOrigin(changed, snapshot); err == nil {
		t.Fatal("different accepted source was trusted against the current row")
	}
	invalid := bytes.Replace(document, []byte(`"runner_version"`), []byte(`"runner_versioN"`), 1)
	for range 2 {
		if _, err := cache.resolve(invalid); err == nil {
			t.Fatal("invalid current origin was accepted")
		}
	}
	if len(cache.entries) != 2 {
		t.Fatal("validation errors entered the admission cache")
	}
	first.receipt.ReportIDs[0] = "caller mutation"
	stored, err := cache.resolve(document)
	if err != nil || !reflect.DeepEqual(stored.receipt, snapshot.Receipt) {
		t.Fatal("caller-owned receipt slices changed cached validation")
	}
	var independent admissionValidationCache
	cold, err := independent.resolve(document)
	if err != nil || !reflect.DeepEqual(cold, stored) {
		t.Fatal("independent full validation disagrees with the warm result")
	}
}

func TestAdmissionValidationCacheHasBoundedEntriesAndReceiptBytes(t *testing.T) {
	document, _ := admissionCacheFixture(t)
	var cache admissionValidationCache
	entry, err := cache.resolve(document)
	if err != nil {
		t.Fatal(err)
	}
	for index := range maxValidatedAdmissions {
		key := sha256.Sum256([]byte(fmt.Sprintf("bounded-admission-%d", index)))
		cache.remember(key, entry)
	}
	if len(cache.entries) != maxValidatedAdmissions {
		t.Fatalf("cache has %d entries, maximum is %d", len(cache.entries), maxValidatedAdmissions)
	}
	if _, exists := cache.entries[sha256.Sum256(document)]; exists {
		t.Fatal("FIFO eviction retained the oldest admission")
	}
	oversized := cloneAdmission(entry)
	oversized.receipt.ReportIDs = []string{strings.Repeat("x", jobcontract.MaxStateBytes+1)}
	oversizedKey := sha256.Sum256([]byte("oversized-admission"))
	cache.remember(oversizedKey, oversized)
	if _, exists := cache.entries[oversizedKey]; exists {
		t.Fatal("cache retained more than MaxStateBytes of receipt data")
	}
	again, err := cache.resolve(document)
	if err != nil || !reflect.DeepEqual(again, entry) || len(cache.entries) > maxValidatedAdmissions {
		t.Fatal("evicted document could not be fully revalidated within the cache bound")
	}
}

func TestAdmissionValidationCacheConcurrentReadsDetachReceiptSlices(t *testing.T) {
	document, snapshot := admissionCacheFixture(t)
	var cache admissionValidationCache
	var group sync.WaitGroup
	for index := range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 4 {
				entry, err := cache.resolve(document)
				if err != nil || !reflect.DeepEqual(entry.receipt, snapshot.Receipt) {
					t.Errorf("parallel source validation changed receipt: %v", err)
					return
				}
				entry.receipt.ReportIDs[0] = fmt.Sprintf("independent-caller-%d", index)
			}
		}()
	}
	group.Wait()
	if len(cache.entries) != 1 {
		t.Fatal("identical concurrent bytes created multiple cache entries")
	}
}
