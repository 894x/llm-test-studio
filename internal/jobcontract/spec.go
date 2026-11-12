// Package jobcontract defines the single current complete-job submission format.
package jobcontract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"slices"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jsonbudget"
)

const (
	CurrentContract = 1
	// Allow sixteen full plans while keeping decoding and transport resource bounds.
	MaxSubmissionBytes = 128 << 20
	MaxSecrets         = 32
	MaxSecretBytes     = 8 << 10
	MaxAssets          = 128
	MaxAssetBytes      = int64(1 << 30)
	MaxJobAssetBytes   = int64(4 << 30)
	MaxOwnedItems      = 16 * 32_768
)

var (
	ErrInvalid     = errors.New("invalid_spec")
	ErrUnsupported = errors.New("unsupported_contract")
	ErrTooLarge    = errors.New("submission_too_large")
	assetRef       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)
)

type TargetKind string

const (
	TargetLocal  TargetKind = "local"
	TargetRemote TargetKind = "remote"
)

type ExecutionTarget struct {
	Kind   TargetKind `json:"kind"`
	NodeID string     `json:"node_id,omitempty"`
}

func (target ExecutionTarget) Validate() error {
	switch target.Kind {
	case TargetLocal:
		if target.NodeID == "" {
			return nil
		}
	case TargetRemote:
		if domain.IsUUID(target.NodeID) {
			return nil
		}
	}
	return invalid("target")
}

type Kind string

const (
	KindRun         Kind = "run"
	KindPerformance Kind = "performance"
	KindComparison  Kind = "comparison"
)

type Payload struct {
	Run         *runs.PreparedRun               `json:"run,omitempty"`
	Performance *quicktest.PreparedPerformance  `json:"performance,omitempty"`
	Comparison  *comparisons.PreparedComparison `json:"comparison,omitempty"`
}

type Asset struct {
	Ref       string `json:"ref"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
}

type JobSpec struct {
	verification       *specVerification
	Contract           int             `json:"contract"`
	JobID              string          `json:"job_id"`
	NodeInstallationID string          `json:"node_installation_id"`
	EngineBuildID      string          `json:"engine_build_id"`
	Target             ExecutionTarget `json:"target"`
	Kind               Kind            `json:"kind"`
	Spec               Payload         `json:"spec"`
	Assets             []Asset         `json:"assets"`
	FunctionalProgress bool            `json:"functional_progress,omitempty"`
}

type Secret struct {
	Slot  string `json:"slot"`
	Value []byte `json:"value"`
}

func (secret Secret) String() string   { return "Secret{redacted}" }
func (secret Secret) GoString() string { return secret.String() }

type Submission struct {
	Job     JobSpec  `json:"job"`
	Secrets []Secret `json:"secrets"`
}

func (submission Submission) String() string   { return "Submission{redacted}" }
func (submission Submission) GoString() string { return submission.String() }

// Clear releases the caller-owned transport copies after converting to leases.
func (submission *Submission) Clear() {
	if submission == nil {
		return
	}
	for _, secret := range submission.Secrets {
		clear(secret.Value)
	}
	submission.Secrets = []Secret{}
}

func (spec JobSpec) Validate() error {
	if spec.verification != nil {
		_, err := spec.validatedDocument()
		return err
	}
	return spec.validate()
}

func (spec JobSpec) validate() error { return spec.validateOwned(nil) }

func (spec JobSpec) validateOwned(owned *[]domain.Run) error {
	if spec.Contract != CurrentContract {
		return ErrUnsupported
	}
	validIDs := domain.IsUUID(spec.JobID) && domain.IsUUID(spec.NodeInstallationID)
	if !validIDs || !IsSHA256(spec.EngineBuildID) {
		return invalid("identity")
	}
	if err := spec.Target.Validate(); err != nil {
		return err
	}
	payloadCount := 0
	for _, present := range []bool{spec.Spec.Run != nil, spec.Spec.Performance != nil, spec.Spec.Comparison != nil} {
		if present {
			payloadCount++
		}
	}
	if payloadCount != 1 {
		return invalid("spec")
	}
	if spec.FunctionalProgress {
		quickRun := spec.Kind == KindRun && spec.Spec.Run != nil && spec.Spec.Run.Snapshot.QuickTask != nil
		if !quickRun {
			return invalid("functional_progress")
		}
	}
	switch spec.Kind {
	case KindRun:
		if spec.Spec.Run == nil {
			return invalid("spec.run")
		}
		run, err := freezeRun(*spec.Spec.Run)
		if err != nil {
			if errors.Is(err, ErrUnsupported) {
				return err
			}
			return invalid("spec.run")
		}
		if owned != nil {
			*owned = append(*owned, run)
		}
	case KindPerformance:
		if spec.Spec.Performance == nil || spec.Spec.Performance.Validate() != nil {
			return invalid("spec.performance")
		}
	case KindComparison:
		input := spec.Spec.Comparison
		if input == nil || input.Meta.Revision != 1 {
			return invalid("spec.comparison")
		}
		if len(input.Runs) > MaxSecrets {
			return invalid("spec.comparison.runs")
		}
		_, runs, err := input.FrozenRuns()
		if err != nil {
			return invalid("spec.comparison")
		}
		if owned != nil {
			*owned = append(*owned, runs...)
		}
		// Comparison() has already validated each complete run and common pins.
		for _, run := range input.Runs {
			if err := validateRunFeatures(run); err != nil {
				if errors.Is(err, ErrUnsupported) {
					return err
				}
				return invalid("spec.comparison.runs")
			}
		}
	default:
		return invalid("kind")
	}
	return validateAssets(spec.Assets)
}

func freezeRun(input runs.PreparedRun) (domain.Run, error) {
	if input.Meta.Revision != 1 {
		return domain.Run{}, invalid("meta.revision")
	}
	run, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validateRunFeatures(input); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func validateRunFeatures(input runs.PreparedRun) error {
	if input.Meta.Revision != 1 {
		return invalid("meta.revision")
	}
	for _, entry := range input.Snapshot.Entries {
		if entry.Performance != nil {
			if _, err := quicktest.PreparePlanEntry(entry.EntryID, input.Snapshot.Channel, *entry.Performance); err != nil {
				return invalid("run.performance")
			}
		}

		if entry.Suite != nil && entry.Suite.HasFileInputs() {
			return fmt.Errorf("%w: file inputs require admitted asset resolution", ErrUnsupported)
		}
		for _, testCase := range entry.CaseDefinitions {
			spec, err := testCase.SpecFor(input.Snapshot.Channel.Protocol)
			if err != nil {
				return err
			}
			if spec.HasFileInputs() {
				return fmt.Errorf("%w: file inputs require admitted asset resolution", ErrUnsupported)
			}
		}
	}
	return nil
}

func validateAssets(assets []Asset) error {
	if assets == nil || len(assets) > MaxAssets {
		return invalid("assets")
	}
	refs := make(map[string]struct{}, len(assets))
	hashes := make(map[string]struct{}, len(assets))
	var total int64
	for _, asset := range assets {
		validIdentity := assetRef.MatchString(asset.Ref) && IsSHA256(asset.SHA256)
		validSize := asset.Size >= 1 && asset.Size <= MaxAssetBytes
		if !validIdentity || !validSize {
			return invalid("assets")
		}
		mediaType, parameters, err := mime.ParseMediaType(asset.MediaType)
		canonicalMediaType := mediaType == asset.MediaType && len(parameters) == 0
		validMediaShape := strings.Count(mediaType, "/") == 1
		validMediaLength := len(asset.MediaType) <= 127
		if err != nil || !canonicalMediaType || !validMediaShape || !validMediaLength {
			return invalid("assets.media_type")
		}
		if _, duplicate := refs[asset.Ref]; duplicate {
			return invalid("assets.ref")
		}
		if _, duplicate := hashes[asset.SHA256]; duplicate {
			return invalid("assets.sha256")
		}
		refs[asset.Ref] = struct{}{}
		hashes[asset.SHA256] = struct{}{}
		total += asset.Size
		if total > MaxJobAssetBytes {
			return invalid("assets.size")
		}
	}
	return nil
}

func (spec JobSpec) SecretSlots() []string {
	slots := []string{}
	switch spec.Kind {
	case KindRun:
		if spec.Spec.Run != nil {
			slots = append(slots, spec.Spec.Run.Meta.ID)
		}
	case KindPerformance:
		if spec.Spec.Performance != nil {
			slots = append(slots, spec.Spec.Performance.ReportID)
		}
	case KindComparison:
		if spec.Spec.Comparison != nil {
			for _, run := range spec.Spec.Comparison.Runs {
				slots = append(slots, run.Meta.ID)
			}
		}
	}
	slices.Sort(slots)
	return slots
}

func (submission Submission) Validate() error {
	if err := submission.Job.Validate(); err != nil {
		return err
	}
	slots := submission.Job.SecretSlots()
	if len(submission.Secrets) != len(slots) || len(submission.Secrets) > MaxSecrets {
		return invalid("secrets")
	}
	remaining := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		remaining[slot] = struct{}{}
	}
	for _, secret := range submission.Secrets {
		if _, expected := remaining[secret.Slot]; !expected {
			return invalid("secrets.slot")
		}
		validLength := len(secret.Value) > 0 && len(secret.Value) <= MaxSecretBytes
		if !validLength || len(bytes.TrimSpace(secret.Value)) == 0 {
			return invalid("secrets.value")
		}
		delete(remaining, secret.Slot)
	}
	return nil
}

// Freeze validates and copies all mutable input before the host takes ownership.
func (spec JobSpec) Freeze() (JobSpec, error) {
	if spec.verification != nil && spec.Spec.Performance == nil {
		fingerprint, err := specFingerprint(spec)
		if err == nil && fingerprint == spec.verification.fingerprint {
			cloned := cloneOwnedSpec(spec)
			after, err := specFingerprint(cloned)
			if err == nil && after == fingerprint {
				return cloned, nil
			}
		}
	}
	raw, err := spec.validatedDocument()
	if err != nil {
		return JobSpec{}, err
	}
	if len(raw) > MaxSubmissionBytes {
		return JobSpec{}, ErrTooLarge
	}
	frozen, err := decodeOwnedSpec(raw)
	if err != nil {
		return JobSpec{}, err
	}
	slices.SortFunc(frozen.Assets, func(left, right Asset) int { return strings.Compare(left.Ref, right.Ref) })
	return frozen, nil
}

func (spec JobSpec) Digest() (string, error) {
	spec.Assets = slices.Clone(spec.Assets)
	slices.SortFunc(spec.Assets, func(left, right Asset) int { return strings.Compare(left.Ref, right.Ref) })
	proof, err := spec.canonicalState()
	if err != nil {
		return "", err
	}
	return proof.digest, nil
}

// DecodeSpec reads the single current non-secret job format with the same
// byte and collection budgets used by complete submissions.
func DecodeSpec(reader io.Reader) (JobSpec, error) {
	if reader == nil {
		return JobSpec{}, invalid("body")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxSubmissionBytes+1))
	if err != nil {
		return JobSpec{}, err
	}
	if len(raw) > MaxSubmissionBytes {
		return JobSpec{}, ErrTooLarge
	}
	return decodeOwnedSpec(raw)
}

func DecodeSubmission(reader io.Reader) (Submission, error) {
	if reader == nil {
		return Submission{}, invalid("body")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxSubmissionBytes+1))
	defer clear(raw)
	if err != nil {
		return Submission{}, invalid("body")
	}
	if len(raw) > MaxSubmissionBytes {
		return Submission{}, ErrTooLarge
	}
	var submission Submission
	if err := decode(raw, &submission); err != nil {
		submission.Clear()
		return Submission{}, err
	}
	if err := submission.Validate(); err != nil {
		submission.Clear()
		return Submission{}, err
	}
	return submission, nil
}

func decode(raw []byte, destination any) error {
	budgetErr := jsonbudget.Check(raw, MaxOwnedItems)
	if errors.Is(budgetErr, jsonbudget.ErrExceeded) {
		return ErrTooLarge
	}
	// Both current checks must pass. Preserve the precise fixed envelope field
	// error when a malformed tail also follows an overfull bounded collection.
	if err := checkCollectionBounds(raw, scopeRoot); err != nil {
		return err
	}
	if budgetErr != nil {
		return invalid("body")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("body")
	}
	return nil
}

func IsSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// InvalidError exposes only a fixed schema field, never untrusted input values.
type InvalidError struct{ Field string }

func (err *InvalidError) Error() string { return fmt.Sprintf("%s: %s", ErrInvalid, err.Field) }
func (err *InvalidError) Unwrap() error { return ErrInvalid }

func invalid(field string) error { return &InvalidError{Field: field} }

// Comparison reuses complete spec validation before constructing immutable
// comparison metadata, instead of validating and encoding every run again.
func (spec JobSpec) Comparison() (domain.Comparison, error) {
	if spec.Kind != KindComparison || spec.Spec.Comparison == nil {
		return domain.Comparison{}, invalid("kind")
	}
	if err := spec.Validate(); err != nil {
		return domain.Comparison{}, err
	}
	input := spec.Spec.Comparison
	refs := make([]domain.ComparisonRunRef, 0, len(input.Runs))
	for _, run := range input.Runs {
		refs = append(refs, domain.ComparisonRunRef{Channel: run.Snapshot.Channel.EntityRevisionRef, RunID: run.Meta.ID})
	}
	return domain.NewComparison(input.Meta, input.Plan, input.Model, refs)
}

// FrozenRuns exposes only domain entities whose snapshots are private and
// immutable. Their getters return copies; no mutable job payload is exposed.
func (spec JobSpec) FrozenRuns() ([]domain.Run, error) {
	if spec.verification != nil {
		fingerprint, err := specFingerprint(spec)
		if err == nil && fingerprint == spec.verification.fingerprint {
			return slices.Clone(spec.verification.runs), nil
		}
	}
	owned := []domain.Run{}
	if err := spec.validateOwned(&owned); err != nil {
		return nil, err
	}
	return owned, nil
}
