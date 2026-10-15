// Package jobcontract defines the single current complete-job submission format.
package jobcontract

import (
	"bytes"
	"crypto/sha256"
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
)

const (
	CurrentContract    = 1
	MaxSubmissionBytes = 8 << 20
	MaxSecrets         = 32
	MaxSecretBytes     = 8 << 10
	MaxAssets          = 128
	MaxAssetBytes      = int64(1 << 30)
	MaxJobAssetBytes   = int64(4 << 30)
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
	Contract           int             `json:"contract"`
	JobID              string          `json:"job_id"`
	NodeInstallationID string          `json:"node_installation_id"`
	EngineBuildID      string          `json:"engine_build_id"`
	Target             ExecutionTarget `json:"target"`
	Kind               Kind            `json:"kind"`
	Spec               Payload         `json:"spec"`
	Assets             []Asset         `json:"assets"`
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
	switch spec.Kind {
	case KindRun:
		if spec.Spec.Run == nil || validateRun(*spec.Spec.Run) != nil {
			return invalid("spec.run")
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
		if _, err := input.Comparison(); err != nil {
			return invalid("spec.comparison")
		}
		for _, run := range input.Runs {
			if err := validateRun(run); err != nil {
				return invalid("spec.comparison.runs")
			}
		}
	default:
		return invalid("kind")
	}
	return validateAssets(spec.Assets)
}

func validateRun(input runs.PreparedRun) error {
	if input.Meta.Revision != 1 {
		return invalid("meta.revision")
	}
	_, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
	return err
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
	if err := spec.Validate(); err != nil {
		return JobSpec{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return JobSpec{}, invalid("spec")
	}
	if len(raw) > MaxSubmissionBytes {
		return JobSpec{}, ErrTooLarge
	}
	var frozen JobSpec
	if err := decode(raw, &frozen); err != nil {
		return JobSpec{}, err
	}
	slices.SortFunc(frozen.Assets, func(left, right Asset) int { return strings.Compare(left.Ref, right.Ref) })
	return frozen, nil
}

func (spec JobSpec) Digest() (string, error) {
	frozen, err := spec.Freeze()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		return "", invalid("spec")
	}
	// RawMessage retains its object key order. Decode the complete document with
	// UseNumber so native request objects are canonical without rounding tokens.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return "", invalid("spec")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", invalid("spec")
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
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
	if err := checkCollectionBounds(raw); err != nil {
		return err
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
