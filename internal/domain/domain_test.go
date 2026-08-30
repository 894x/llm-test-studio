package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewEntityMetaCreatesStableVersionedIdentity(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)

	meta, err := NewEntityMeta(now)
	if err != nil {
		t.Fatalf("NewEntityMeta() error = %v", err)
	}
	if !IsUUID(meta.ID) {
		t.Fatalf("NewEntityMeta() ID = %q, want UUID", meta.ID)
	}
	if meta.SchemaVersion != CurrentEntitySchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", meta.SchemaVersion, CurrentEntitySchemaVersion)
	}
	if meta.Revision != 1 {
		t.Fatalf("Revision = %d, want 1", meta.Revision)
	}
	if !meta.CreatedAt.Equal(now) || !meta.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = (%v, %v), want %v", meta.CreatedAt, meta.UpdatedAt, now)
	}
}

func TestEntityMetaNextRevisionPreservesIdentityAndCreationTime(t *testing.T) {
	created := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)
	updated := created.Add(5 * time.Minute)
	meta := EntityMeta{
		ID: "123e4567-e89b-42d3-a456-426614174000", SchemaVersion: 1,
		Revision: 7, CreatedAt: created, UpdatedAt: created,
	}

	next, err := meta.NextRevision(updated)
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	if next.ID != meta.ID || !next.CreatedAt.Equal(created) {
		t.Fatalf("identity changed: %#v", next)
	}
	if next.Revision != 8 || !next.UpdatedAt.Equal(updated) {
		t.Fatalf("revision = %d at %v, want 8 at %v", next.Revision, next.UpdatedAt, updated)
	}
}

func TestRunTransitionAllowsLifecycleAndRejectsTerminalMutation(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)
	meta := EntityMeta{
		ID: "123e4567-e89b-42d3-a456-426614174000", SchemaVersion: 1,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	snapshot := validRunSnapshot()
	run, err := NewRun(meta, snapshot.Plan.ID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}

	for _, next := range []RunStatus{RunStarting, RunRunning, RunDraining, RunCompleted} {
		now = now.Add(time.Second)
		run, err = run.Transition(next, now)
		if err != nil {
			t.Fatalf("Transition(%q) error = %v", next, err)
		}
	}
	if run.Status() != RunCompleted || run.Meta().Revision != 5 {
		t.Fatalf("completed run = %#v, want status completed revision 5", run)
	}
	if _, err := run.Transition(RunFailed, now.Add(time.Second)); err == nil {
		t.Fatal("terminal run accepted another transition")
	}
}

func TestRunTransitionRejectsInvalidJump(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 30, 0, 0, time.UTC)
	meta := EntityMeta{
		ID: "123e4567-e89b-42d3-a456-426614174000", SchemaVersion: 1,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	snapshot := validRunSnapshot()
	run, err := NewRun(meta, snapshot.Plan.ID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}

	if _, err := run.Transition(RunDraining, now.Add(time.Second)); err == nil {
		t.Fatal("queued run transitioned directly to draining")
	}
}

func TestSuccessDimensionsRequireEveryLayer(t *testing.T) {
	qualified := SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
	if !qualified.Overall() {
		t.Fatal("fully successful result was not qualified")
	}

	semanticFailure := qualified
	semanticFailure.Semantic = false
	if semanticFailure.Overall() {
		t.Fatal("semantic failure was reported as overall success")
	}
}

func TestFailureKindSeparatesExpectedOperationalClasses(t *testing.T) {
	want := []FailureKind{
		FailureNetwork, FailureHTTP, FailureProtocol, FailureSemantic,
		FailureRateLimit, FailureTimeout, FailureCancelled, FailureSLA,
	}
	seen := map[FailureKind]bool{}
	for _, kind := range want {
		if kind == "" || seen[kind] {
			t.Fatalf("invalid or duplicate failure kind %q", kind)
		}
		seen[kind] = true
	}
}

func TestCredentialReferenceSerializesMetadataWithoutSecret(t *testing.T) {
	ref := CredentialRef{
		EntityMeta:   validEntityMeta("123e4567-e89b-42d3-a456-426614174000"),
		StoreRef:     "llm-studio/channel/example",
		Purpose:      CredentialChannelAPIKey,
		MaskedSuffix: "cdef",
		Fingerprint:  "sha256:" + strings.Repeat("a", 64),
	}

	encoded, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, forbidden := range []string{"api_key", "secret", "plaintext", "value"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("credential reference JSON %s contains forbidden field %q", encoded, forbidden)
		}
	}
}

func TestReportValidationRequiresVersionedRunSnapshotAndContext(t *testing.T) {
	report := validReport()
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	report.PlanSnapshot = RunSnapshot{}
	if err := report.Validate(); err == nil {
		t.Fatal("report without immutable plan snapshot validated")
	}
}
