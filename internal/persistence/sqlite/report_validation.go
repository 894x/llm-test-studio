package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/canonicaljson"
	"github.com/894x/llm-test-studio/internal/domain"
)

// Deep report checks run only when a report document is requested.
type reportRunExpectation struct {
	id             string
	createdAt      string
	revision       uint64
	documentDigest [sha256.Size]byte
	snapshotDigest [sha256.Size]byte
}

func validateReportRunHistories(ctx context.Context, tx relationQueryer, currentRuns map[string]reportRunExpectation) error {
	if len(currentRuns) == 0 {
		return nil
	}
	arguments := make([]any, 0, len(currentRuns))
	for runID := range currentRuns {
		arguments = append(arguments, runID)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT run_id, schema_version, revision, created_at, updated_at,
		       plan_id, plan_revision, status, snapshot_json, document_json
		FROM execution_run_revisions
		WHERE run_id IN (`+reportProjectionPlaceholders(len(arguments))+`)
		ORDER BY run_id, revision
	`, arguments...)
	if err != nil {
		return reportProjectionReadError(ctx, "query report run history", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, len(currentRuns))
	var activeRunID string
	var previous domain.Run
	var canonicalSnapshot json.RawMessage
	var revisionCount uint64
	var lastDocumentDigest [sha256.Size]byte
	finishRun := func() error {
		if activeRunID == "" {
			return nil
		}
		expected, exists := currentRuns[activeRunID]
		if !exists || revisionCount != expected.revision || lastDocumentDigest != expected.documentDigest {
			return fmt.Errorf("%w: report run current revision", ErrCorrupt)
		}
		seen[activeRunID] = struct{}{}
		return nil
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var schemaVersion, revision, planRevision int64
		var runID, createdAt, updatedAt, planID, status string
		var snapshotDocument, document []byte
		if err := rows.Scan(
			&runID, &schemaVersion, &revision, &createdAt, &updatedAt,
			&planID, &planRevision, &status, &snapshotDocument, &document,
		); err != nil {
			return reportProjectionReadError(ctx, "scan report run history", err)
		}
		if runID != activeRunID {
			if err := finishRun(); err != nil {
				return err
			}
			if _, duplicate := seen[runID]; duplicate {
				return fmt.Errorf("%w: report run revision order", ErrCorrupt)
			}
			activeRunID = runID
			previous = domain.Run{}
			canonicalSnapshot = nil
			revisionCount = 0
			lastDocumentDigest = [sha256.Size]byte{}
		}
		expected, exists := currentRuns[runID]
		if !exists || revision < 1 || uint64(revision) != revisionCount+1 {
			return fmt.Errorf("%w: report run revision order", ErrCorrupt)
		}
		if sha256.Sum256(snapshotDocument) != expected.snapshotDigest {
			return fmt.Errorf("%w: report run snapshot document", ErrCorrupt)
		}
		var run domain.Run
		if revisionCount == 0 {
			run, err = decodeRunDocument(document)
			if err != nil {
				return err
			}
			canonicalSnapshot, err = marshalCanonical(run.Snapshot())
			if err != nil || !bytes.Equal(canonicalSnapshot, snapshotDocument) {
				return fmt.Errorf("%w: report run snapshot document", ErrCorrupt)
			}
			canonicaljson.Remember(canonicalSnapshot)
		} else {
			// Revisions share one frozen snapshot. Reuse its validation and let
			// the domain transition validate the changing metadata and failure.
			run, err = nextReportRunRevision(
				previous,
				status,
				updatedAt,
				document,
			)
			if err != nil {
				return err
			}
			want, err := marshalReportRunRevision(run, canonicalSnapshot)
			if err != nil || !bytes.Equal(want, document) {
				return fmt.Errorf("%w: report run revision transition", ErrCorrupt)
			}
		}
		meta := run.Meta()
		if int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
			formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt ||
			run.PlanID() != planID || int64(run.PlanRevision()) != planRevision ||
			string(run.Status()) != status {
			return fmt.Errorf("%w: report run history columns", ErrCorrupt)
		}
		if revisionCount == 0 {
			if run.Status() != domain.RunQueued || formatTime(meta.CreatedAt) != expected.createdAt {
				return fmt.Errorf("%w: report run initial revision", ErrCorrupt)
			}
		}
		previous = run
		revisionCount++
		lastDocumentDigest = sha256.Sum256(document)
	}
	if err := rows.Err(); err != nil {
		return reportProjectionReadError(ctx, "iterate report run history", err)
	}
	if err := rows.Close(); err != nil {
		return reportProjectionReadError(ctx, "close report run history", err)
	}
	if err := finishRun(); err != nil {
		return err
	}
	if len(seen) != len(currentRuns) {
		return fmt.Errorf("%w: report run history membership", ErrCorrupt)
	}
	return nil
}

func nextReportRunRevision(previous domain.Run, status, updatedAt string, document []byte) (domain.Run, error) {
	at, err := parseProjectionTimestamp(updatedAt)
	if err != nil {
		return domain.Run{}, storageCorrupt("report run revision timestamp")
	}
	var failure struct {
		Failure *domain.RunFailure `json:"failure"`
	}
	if status == string(domain.RunFailed) {
		if err := json.Unmarshal(document, &failure); err != nil {
			return domain.Run{}, storageCorrupt("report run revision failure")
		}
	}
	var run domain.Run
	if failure.Failure != nil {
		run, err = previous.Fail(*failure.Failure, at)
	} else {
		run, err = previous.Transition(domain.RunStatus(status), at)
	}
	if err != nil {
		return domain.Run{}, storageCorrupt("report run revision transition")
	}
	return run, nil
}

// This is the existing persisted Run envelope, assembled from the validated
// domain transition and the same canonical snapshot for every revision.
func marshalReportRunRevision(run domain.Run, snapshot json.RawMessage) ([]byte, error) {
	meta := run.Meta()
	fields := map[string]any{
		"id": meta.ID, "schema_version": meta.SchemaVersion, "revision": meta.Revision,
		"created_at": meta.CreatedAt, "updated_at": meta.UpdatedAt,
		"plan_id": run.PlanID(), "status": run.Status(), "plan_snapshot": snapshot,
	}
	if failure := run.Failure(); failure != nil {
		fields["failure"] = failure
	}
	return marshalCanonicalFields(fields)
}

func reportProjectionPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func checkReportProjectionItemBudgets(report domain.Report) error {
	for index, result := range report.CaseResults {
		if err := checkReportProjectionEncodedItem("result", index, result); err != nil {
			return err
		}
	}
	for index, evidence := range report.Evidence {
		if err := checkReportProjectionEncodedItem("evidence", index, evidence); err != nil {
			return err
		}
	}
	for index, attachment := range report.Attachments {
		if err := checkReportProjectionEncodedItem("artifact", index, attachment); err != nil {
			return err
		}
	}
	for suiteIndex, suite := range report.EntryReports {
		for resultIndex, result := range suite.CaseResults {
			if err := checkReportProjectionEncodedItem(
				fmt.Sprintf("suite %d result", suiteIndex),
				resultIndex,
				result,
			); err != nil {
				return err
			}
		}
		for _, section := range []struct {
			kind  string
			items []json.RawMessage
		}{
			{kind: fmt.Sprintf("suite %d timeline", suiteIndex), items: suite.Timeline},
			{kind: fmt.Sprintf("suite %d distribution", suiteIndex), items: suite.Distributions},
		} {
			for itemIndex, item := range section.items {
				if len(item) > MaxReportProjectionItemBytes {
					return fmt.Errorf("report %s item %d exceeds byte budget", section.kind, itemIndex)
				}
				if err := checkReportProjectionEncodedItem(section.kind, itemIndex, item); err != nil {
					return err
				}
			}
		}
	}
	for _, section := range []struct {
		kind  string
		items []json.RawMessage
	}{
		{kind: "timeline", items: report.Timeline},
		{kind: "distribution", items: report.Distributions},
		{kind: "error cluster", items: report.ErrorClusters},
	} {
		for index, item := range section.items {
			if len(item) > MaxReportProjectionItemBytes {
				return fmt.Errorf("report %s item %d exceeds byte budget", section.kind, index)
			}
			if err := checkReportProjectionEncodedItem(section.kind, index, item); err != nil {
				return err
			}
		}
	}
	if len(report.Baseline) > MaxReportProjectionItemBytes {
		return fmt.Errorf("report baseline exceeds byte budget")
	}
	if err := checkReportProjectionEncodedItem("baseline", 0, report.Baseline); err != nil {
		return err
	}
	return nil
}

func checkReportProjectionEncodedItem(kind string, index int, item any) error {
	document, err := marshalCanonical(item)
	if err != nil {
		return fmt.Errorf("encode report %s item %d: %w", kind, index, err)
	}
	if len(document) > MaxReportProjectionItemBytes {
		return fmt.Errorf("report %s item %d exceeds byte budget", kind, index)
	}
	return nil
}
