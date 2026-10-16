package sqlite

import (
	"crypto/sha256"
	"slices"
	"sync"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const maxValidatedAdmissions = 128

type validatedAdmission struct {
	receipt            jobcontract.Receipt
	specSHA256         string
	specDocumentSHA256 [sha256.Size]byte
	runs               []validatedAdmissionRun
}

type validatedAdmissionRun struct {
	runID, planID  string
	snapshotSHA256 [sha256.Size]byte
}

// This caches a pure validation result by the complete durable document's
// content, never by a Job or database identity. Every read still hashes the
// original bytes and checks the current row against the frozen receipt. Each
// entry retains at most MaxStateBytes of receipt and frozen run identity data;
// large Specs and event documents are never retained. FIFO eviction bounds the
// cache to 128 entries.
var admissionDocuments = admissionValidationCache{}

type admissionValidationCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]validatedAdmission
	order   [maxValidatedAdmissions][sha256.Size]byte
	next    int
}

func (cache *admissionValidationCache) resolve(document []byte) (validatedAdmission, error) {
	if len(document) == 0 || len(document) > jobcontract.MaxEventBytes {
		return validatedAdmission{}, storageCorrupt("accepted execution origin document budget")
	}
	key := sha256.Sum256(document)
	cache.mu.Lock()
	entry, exists := cache.entries[key]
	cache.mu.Unlock()
	if exists {
		return cloneAdmission(entry), nil
	}
	entry, err := validateAdmissionDocument(document)
	if err != nil {
		return validatedAdmission{}, err
	}
	cache.remember(key, entry)
	return entry, nil
}

func (cache *admissionValidationCache) remember(key [sha256.Size]byte, entry validatedAdmission) {
	receiptDocument, err := marshalCanonical(entry.receipt)
	retainedBytes := len(receiptDocument) + len(entry.specSHA256) + sha256.Size
	for _, run := range entry.runs {
		retainedBytes += len(run.runID) + len(run.planID) + sha256.Size
	}
	if err != nil || retainedBytes > jobcontract.MaxStateBytes {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[[sha256.Size]byte]validatedAdmission, maxValidatedAdmissions)
	}
	if _, exists := cache.entries[key]; exists {
		return
	}
	if len(cache.entries) == maxValidatedAdmissions {
		delete(cache.entries, cache.order[cache.next])
	}
	cache.entries[key] = cloneAdmission(entry)
	cache.order[cache.next] = key
	cache.next = (cache.next + 1) % maxValidatedAdmissions
}

func cloneAdmission(entry validatedAdmission) validatedAdmission {
	entry.receipt.RunIDs = slices.Clone(entry.receipt.RunIDs)
	entry.receipt.ReportIDs = slices.Clone(entry.receipt.ReportIDs)
	entry.runs = slices.Clone(entry.runs)
	return entry
}

func validateAdmissionDocument(document []byte) (validatedAdmission, error) {
	event, err := decodeStoredJobEvent(document)
	if err != nil {
		return validatedAdmission{}, storageCorrupt("accepted execution origin")
	}
	var accepted jobcontract.AcceptedEvent
	if err := decodeCanonical(event.Payload, &accepted, nil); err != nil {
		return validatedAdmission{}, storageCorrupt("accepted execution origin")
	}
	initial, err := accepted.Spec.InitialSnapshot(event.CreatedAt, accepted.Snapshot.Receipt.Origin)
	validEvent := event.Kind == jobcontract.EventAccepted && event.Sequence == 1 && event.JobID == initial.Receipt.JobID
	if err != nil || !validEvent || !equalCanonicalDocuments(initial, accepted.Snapshot) {
		return validatedAdmission{}, storageCorrupt("accepted execution origin")
	}
	digest, err := accepted.Spec.Digest()
	if err != nil {
		return validatedAdmission{}, storageCorrupt("accepted job spec digest")
	}
	specDocument, err := marshalCanonical(accepted.Spec)
	if err != nil {
		return validatedAdmission{}, storageCorrupt("accepted job spec document")
	}
	inputs := accepted.Spec.PreparedRuns()
	runs := make([]validatedAdmissionRun, 0, len(inputs))
	for _, input := range inputs {
		snapshotDocument, err := marshalCanonical(input.Snapshot)
		if err != nil {
			return validatedAdmission{}, storageCorrupt("accepted run snapshot document")
		}
		runs = append(runs, validatedAdmissionRun{
			runID: input.Meta.ID, planID: input.PlanID, snapshotSHA256: sha256.Sum256(snapshotDocument),
		})
	}
	return validatedAdmission{
		receipt: initial.Receipt, specSHA256: digest, specDocumentSHA256: sha256.Sum256(specDocument),
		runs: runs,
	}, nil
}
