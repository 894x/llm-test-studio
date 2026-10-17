package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) ListRemoteDocuments(ctx context.Context, jobID string) ([]remoteresults.DocumentReference, error) {
	if !domain.IsUUID(jobID) {
		return nil, remoteresults.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin remote document list: %w", err)
	}
	defer tx.Rollback()
	mirror, err := queryRemoteMirror(ctx, tx, jobID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(
		ctx,
		`SELECT reference_type, report_id, document_json, imported FROM remote_job_report_refs
		WHERE job_id = ? ORDER BY reference_type, report_id LIMIT ?`,
		jobID,
		jobcontract.MaxSecrets+3,
	)
	if err != nil {
		return nil, fmt.Errorf("list remote documents: %w", err)
	}
	defer rows.Close()
	documents := []remoteresults.DocumentReference{}
	rowIDs := []string{}
	for rows.Next() {
		var value remoteresults.DocumentReference
		var rowID string
		var raw []byte
		if err := rows.Scan(
			&value.Kind,
			&rowID,
			&raw,
			&value.Imported,
		); err != nil {
			return nil, fmt.Errorf("scan remote document: %w", err)
		}
		switch value.Kind {
		case jobcontract.EventReport:
			if err := decodeCanonical(raw, &value.Reference, nil); err != nil {
				return nil, storageCorrupt("remote report reference")
			}
		case jobcontract.EventPerformanceResult:
			var reference jobcontract.PerformanceResultReference
			if err := decodeCanonical(raw, &reference, func() error { return reference.Validate() }); err != nil {
				return nil, storageCorrupt("remote performance reference")
			}
			validOwner := mirror.Spec.Kind == jobcontract.KindPerformance && reference.JobID == jobID &&
				reference.ReportID == mirror.Spec.Spec.Performance.ReportID
			if !validOwner {
				return nil, storageCorrupt("remote performance reference owner")
			}
			value.Reference = jobcontract.ReportReference{
				ID: reference.ReportID, Kind: jobcontract.KindPerformance, SHA256: reference.SHA256, Size: reference.Size,
			}
		default:
			return nil, storageCorrupt("remote document type")
		}
		documents = append(documents, value)
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate remote documents: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close remote document list: %w", err)
	}
	if len(documents) > jobcontract.MaxSecrets+2 {
		return nil, storageCorrupt("remote document count")
	}
	for index, document := range documents {
		reference := document.Reference
		validIdentity := domain.IsUUID(reference.ID) && reference.ID == rowIDs[index]
		validDigest := jobcontract.IsSHA256(reference.SHA256) && reference.Size > 0
		limit := int64(jobcontract.MaxRunReportBytes)
		if document.Kind == jobcontract.EventPerformanceResult {
			limit = jobcontract.MaxPerformanceResultBytes
		}
		if !validIdentity || !validDigest || reference.Size > limit {
			return nil, storageCorrupt("remote document identity or byte budget")
		}
		switch reference.Kind {
		case jobcontract.KindRun:
			if mirror.Spec.Kind == jobcontract.KindPerformance || !domain.IsUUID(reference.RunID) {
				return nil, storageCorrupt("remote run report kind")
			}
			if err := requireRemoteRun(
				ctx,
				tx,
				jobID,
				reference.RunID,
			); err != nil {
				return nil, storageCorrupt("remote report run owner")
			}
		case jobcontract.KindPerformance:
			validOwner := mirror.Spec.Kind == jobcontract.KindPerformance &&
				reference.ID == mirror.Spec.Spec.Performance.ReportID && reference.RunID == ""
			if !validOwner {
				return nil, storageCorrupt("remote performance report owner")
			}
		default:
			return nil, storageCorrupt("remote report kind")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit remote document list: %w", err)
	}
	return documents, nil
}
