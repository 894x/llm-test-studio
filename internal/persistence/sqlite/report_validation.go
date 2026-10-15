package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

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
			revisionCount = 0
			lastDocumentDigest = [sha256.Size]byte{}
		}
		expected, exists := currentRuns[runID]
		if !exists || revision < 1 || uint64(revision) != revisionCount+1 {
			return fmt.Errorf("%w: report run revision order", ErrCorrupt)
		}
		run, err := decodeRunDocument(document)
		if err != nil {
			return err
		}
		snapshot := run.Snapshot()
		canonicalSnapshot, err := marshalCanonical(snapshot)
		if err != nil || !bytes.Equal(canonicalSnapshot, snapshotDocument) ||
			sha256.Sum256(snapshotDocument) != expected.snapshotDigest {
			return fmt.Errorf("%w: report run snapshot document", ErrCorrupt)
		}
		meta := run.Meta()
		if int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
			formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt ||
			run.PlanID() != planID || int64(snapshot.Plan.Revision) != planRevision ||
			string(run.Status()) != status {
			return fmt.Errorf("%w: report run history columns", ErrCorrupt)
		}
		if revisionCount == 0 {
			if run.Status() != domain.RunQueued || formatTime(meta.CreatedAt) != expected.createdAt {
				return fmt.Errorf("%w: report run initial revision", ErrCorrupt)
			}
		} else {
			var want domain.Run
			var transitionErr error
			if failure := run.Failure(); run.Status() == domain.RunFailed && failure != nil {
				want, transitionErr = previous.Fail(*failure, meta.UpdatedAt)
			} else {
				want, transitionErr = previous.Transition(run.Status(), meta.UpdatedAt)
			}
			if transitionErr != nil || !equalCanonicalDocuments(want, run) {
				return fmt.Errorf("%w: report run revision transition", ErrCorrupt)
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
