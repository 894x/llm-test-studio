package sqlite

import (
	"crypto/sha256"
	"slices"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/jobs"
)

const submissionReadCacheBytes = 128 << 20
const submissionReadCacheEntries = 32

type submissionCacheEntry struct {
	record jobs.SubmissionRecord
	charge int
}

var submissionReads = struct {
	sync.Mutex
	entries map[[sha256.Size]byte]submissionCacheEntry
	order   [][sha256.Size]byte
	used    int
}{entries: map[[sha256.Size]byte]submissionCacheEntry{}, order: [][sha256.Size]byte{}}

// Cache only validation of exact canonical document bytes, not database facts.
// Row identity columns are still checked on every read, and every returned
// record owns its mutable data independently of the cached preparation.
func cachedSubmission(document []byte) (jobs.SubmissionRecord, bool, error) {
	key := sha256.Sum256(document)
	submissionReads.Lock()
	entry, found := submissionReads.entries[key]
	submissionReads.Unlock()
	if !found {
		return jobs.SubmissionRecord{}, false, nil
	}
	record, err := cloneSubmission(entry.record)
	return record, true, err
}
func rememberSubmission(document []byte, record jobs.SubmissionRecord) bool {
	charge := len(document)*6 + 4096
	if charge > submissionReadCacheBytes {
		return false
	}
	key := sha256.Sum256(document)
	submissionReads.Lock()
	defer submissionReads.Unlock()
	if _, found := submissionReads.entries[key]; found {
		return false
	}
	for len(submissionReads.order) >= submissionReadCacheEntries || submissionReads.used+charge > submissionReadCacheBytes {
		oldest := submissionReads.order[0]
		submissionReads.used -= submissionReads.entries[oldest].charge
		delete(submissionReads.entries, oldest)
		submissionReads.order = submissionReads.order[1:]
	}
	submissionReads.entries[key] = submissionCacheEntry{record: record, charge: charge}
	submissionReads.order = append(submissionReads.order, key)
	submissionReads.used += charge
	return true
}
func cloneSubmission(record jobs.SubmissionRecord) (jobs.SubmissionRecord, error) {
	spec, err := record.Prepared.Spec.Freeze()
	if err != nil {
		return jobs.SubmissionRecord{}, err
	}
	record.Prepared.Spec = spec
	record.Prepared.Runner = record.Prepared.Runner.Clone()
	if record.Receipt != nil {
		receipt := *record.Receipt
		receipt.RunIDs = slices.Clone(receipt.RunIDs)
		receipt.ReportIDs = slices.Clone(receipt.ReportIDs)
		record.Receipt = &receipt
	}
	return record, nil
}
