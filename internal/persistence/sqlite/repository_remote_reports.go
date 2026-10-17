package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// ImportRemoteRunReport verifies the original downloaded bytes against the
// committed reference. Report, attachments, seal and download state commit
// together. File publication and fsync remain the cache owner's responsibility.
func (repository *Repository) ImportRemoteRunReport(ctx context.Context, input remoteresults.RunReportImport) error {
	validOwner := input.Origin.Validate() == nil && domain.IsUUID(input.JobID)
	validSize := len(input.Document) > 0 && len(input.Document) <= jobcontract.MaxRunReportBytes
	if !validOwner || !validSize {
		return remoteresults.ErrInvalid
	}
	input.Document = bytes.Clone(input.Document)
	if err := remoteresults.CheckRunReportBudget(input.Document); err != nil {
		return err
	}
	var report domain.Report
	if err := decodeCanonical(input.Document, &report, func() error { return report.Validate() }); err != nil {
		return fmt.Errorf("%w: invalid current run report", remoteresults.ErrInvalid)
	}
	if err := checkReportProjectionItemBudgets(report); err != nil {
		return err
	}
	digest := sha256.Sum256(input.Document)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remote report import: %w", err)
	}
	defer tx.Rollback()
	mirror, err := queryRemoteMirror(ctx, tx, input.JobID)
	if err != nil {
		return err
	}
	if mirror.Origin != input.Origin {
		return remoteresults.ErrConflict
	}
	if err := requireRemoteRun(
		ctx,
		tx,
		input.JobID,
		report.RunID,
	); err != nil {
		return err
	}
	var rawReference []byte
	var imported bool
	err = tx.QueryRowContext(
		ctx,
		`SELECT document_json, imported FROM remote_job_report_refs
		WHERE job_id = ? AND reference_type = 'report' AND report_id = ?`,
		input.JobID,
		report.ID,
	).Scan(&rawReference, &imported)
	if err != nil {
		return fmt.Errorf("read remote report reference: %w", err)
	}
	var reference jobcontract.ReportReference
	if err := decodeCanonical(rawReference, &reference, nil); err != nil {
		return fmt.Errorf("%w: remote report reference", ErrCorrupt)
	}
	validReference := reference.Kind == jobcontract.KindRun && reference.RunID == report.RunID && reference.ID == report.ID
	validDigest := reference.Size == int64(len(input.Document)) && reference.SHA256 == hex.EncodeToString(digest[:])
	if !validReference || !validDigest {
		return remoteresults.ErrConflict
	}
	if imported {
		matched, err := importedDocumentMatches(ctx,
			tx.QueryRowContext(ctx, `SELECT document_json FROM reports WHERE id = ?`, report.ID), input.Document)
		if err != nil {
			return err
		}
		if !matched {
			return fmt.Errorf("%w: imported remote report missing", ErrCorrupt)
		}
		return validateReportStorage(ctx, tx, report)
	}
	if err := createReportTx(
		ctx,
		tx,
		report,
		input.Document,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE remote_job_report_refs SET imported = 1
		WHERE job_id = ? AND reference_type = 'report' AND report_id = ?`,
		input.JobID,
		report.ID,
	); err != nil {
		return fmt.Errorf("record remote report import: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit remote report import", err)
	}
	return nil
}

// RemoteRunReportImported distinguishes a pending reference from a verified
// report without loading its full document or claiming evidence files exist.
func (repository *Repository) RemoteRunReportImported(ctx context.Context, jobID, reportID string) (bool, error) {
	if !domain.IsUUID(jobID) || !domain.IsUUID(reportID) {
		return false, remoteresults.ErrInvalid
	}
	var imported bool
	err := repository.db.QueryRowContext(
		ctx,
		`SELECT imported FROM remote_job_report_refs
		WHERE job_id = ? AND reference_type = 'report' AND report_id = ?`,
		jobID,
		reportID,
	).Scan(&imported)
	if errors.Is(err, sql.ErrNoRows) {
		return false, remoteresults.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("read remote report import status: %w", err)
	}
	return imported, nil
}
