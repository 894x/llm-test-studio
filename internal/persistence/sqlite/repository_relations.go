package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type relationQueryer interface {
	rowQueryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validateCredentialStorage(ctx context.Context, queryer rowQueryer, credential domain.CredentialRef) error {
	var storeRef, purpose, suffix, fingerprint string
	err := queryer.QueryRowContext(ctx, `
		SELECT store_ref, purpose, masked_suffix, fingerprint
		FROM credential_refs WHERE id = ? AND revision = ?
	`, credential.ID, credential.Revision).Scan(&storeRef, &purpose, &suffix, &fingerprint)
	if err != nil {
		return relationStorageError(ctx, "credential reference columns", err)
	}
	canonical, err := credentials.StoreRefFromCredential(credential)
	if err != nil || storeRef != canonical.Value() || purpose != string(credential.Purpose) ||
		suffix != credential.MaskedSuffix || fingerprint != credential.Fingerprint {
		return storageCorrupt("credential reference columns")
	}
	return nil
}

func validateChannelStorage(ctx context.Context, queryer rowQueryer, channel domain.Channel) error {
	var credentialID sql.NullString
	var credentialRevision sql.NullInt64
	err := queryer.QueryRowContext(ctx, `
		SELECT credential_id, credential_revision FROM channels WHERE id = ? AND revision = ?
	`, channel.ID, channel.Revision).Scan(&credentialID, &credentialRevision)
	if err != nil {
		return relationStorageError(ctx, "channel credential relation", err)
	}
	if channel.CredentialID == "" {
		if credentialID.Valid || credentialRevision.Valid {
			return storageCorrupt("channel credential relation")
		}
		return nil
	}
	if !credentialID.Valid || !credentialRevision.Valid || credentialID.String != channel.CredentialID || credentialRevision.Int64 < 1 {
		return storageCorrupt("channel credential relation")
	}
	document, err := exactDocument(ctx, queryer, "credential_refs", credentialID.String, uint64(credentialRevision.Int64), "credential reference")
	if err != nil {
		return relationStorageError(ctx, "channel credential relation", err)
	}
	var credential domain.CredentialRef
	if err := decodeCanonical(document, &credential, func() error { return credential.Validate() }); err != nil ||
		credential.Purpose != domain.CredentialChannelAPIKey {
		return storageCorrupt("channel credential relation")
	}
	if err := validateCredentialStorage(ctx, queryer, credential); err != nil {
		return relationStorageError(ctx, "channel credential relation", err)
	}
	return nil
}

func validateChannelModelStorage(ctx context.Context, queryer rowQueryer, mapping domain.ChannelModel) error {
	var channelID, modelID, bindingKey string
	var channelRevision, modelRevision int64
	err := queryer.QueryRowContext(ctx, `
		SELECT channel_id, channel_revision, model_id, model_revision, binding_key
		FROM channel_models WHERE id = ? AND revision = ?
	`, mapping.ID, mapping.Revision).Scan(&channelID, &channelRevision, &modelID, &modelRevision, &bindingKey)
	if err != nil {
		return relationStorageError(ctx, "channel model relation", err)
	}
	if channelID != mapping.ChannelID || modelID != mapping.ModelID ||
		bindingKey != mapping.ChannelID+"|"+mapping.ModelID || channelRevision < 1 || modelRevision < 1 {
		return storageCorrupt("channel model relation")
	}
	channelDocument, err := exactDocument(ctx, queryer, "channels", channelID, uint64(channelRevision), "channel")
	if err != nil {
		return relationStorageError(ctx, "channel model channel revision", err)
	}
	var channel domain.Channel
	if err := decodeCanonical(channelDocument, &channel, func() error { return channel.Validate() }); err != nil {
		return storageCorrupt("channel model channel revision")
	}
	if err := validateChannelStorage(ctx, queryer, channel); err != nil {
		return relationStorageError(ctx, "channel model channel revision", err)
	}
	modelDocument, err := exactDocument(ctx, queryer, "models", modelID, uint64(modelRevision), "model")
	if err != nil {
		return relationStorageError(ctx, "channel model model revision", err)
	}
	var model domain.Model
	if err := decodeCanonical(modelDocument, &model, func() error { return model.Validate() }); err != nil {
		return storageCorrupt("channel model model revision")
	}
	if channel.Protocol != model.Protocol {
		return storageCorrupt("channel model protocol relation")
	}
	return nil
}

func validateSuiteStorage(ctx context.Context, queryer relationQueryer, suite domain.Suite) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT position, case_id, case_revision FROM suite_cases
		WHERE suite_id = ? AND suite_revision = ? ORDER BY position
	`, suite.ID, suite.Revision)
	if err != nil {
		return relationStorageError(ctx, "suite case relations", err)
	}
	stored := make([]domain.CaseRevisionRef, 0, len(suite.Cases))
	position := 0
	for rows.Next() {
		var storedPosition int
		var caseID string
		var revision int64
		if err := rows.Scan(&storedPosition, &caseID, &revision); err != nil {
			rows.Close()
			return relationStorageError(ctx, "suite case relations", err)
		}
		if position >= len(suite.Cases) {
			rows.Close()
			return storageCorrupt("suite case relations")
		}
		want := suite.Cases[position]
		if storedPosition != position || caseID != want.CaseID || revision < 1 || uint64(revision) != want.Revision {
			rows.Close()
			return storageCorrupt("suite case relations")
		}
		stored = append(stored, want)
		position++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "suite case relations", err)
	}
	if position != len(suite.Cases) {
		rows.Close()
		return storageCorrupt("suite case relations")
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "suite case relations", err)
	}
	for _, ref := range stored {
		document, err := exactDocument(ctx, queryer, "test_cases", ref.CaseID, ref.Revision, "test case")
		if err != nil {
			return relationStorageError(ctx, "suite case revision", err)
		}
		var testCase domain.TestCase
		if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
			return storageCorrupt("suite case revision")
		}
	}
	return nil
}

func validatePlanStorage(ctx context.Context, queryer relationQueryer, plan domain.Plan) error {
	var suiteID sql.NullString
	var suiteRevision sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `
		SELECT suite_id, suite_revision FROM test_plans WHERE id = ? AND revision = ?
	`, plan.ID, plan.Revision).Scan(&suiteID, &suiteRevision); err != nil {
		return relationStorageError(ctx, "plan suite relation", err)
	}
	if plan.SuiteID == "" {
		if suiteID.Valid || suiteRevision.Valid {
			return storageCorrupt("plan suite relation")
		}
	} else if suiteID.Valid != suiteRevision.Valid {
		return storageCorrupt("plan suite relation")
	} else if suiteID.Valid && (suiteID.String != plan.SuiteID || suiteRevision.Int64 < 1 || uint64(suiteRevision.Int64) != plan.SuiteRevision) {
		return storageCorrupt("plan suite relation")
	}
	if plan.SuiteID != "" && suiteID.Valid {
		document, err := exactDocument(ctx, queryer, "test_suites", plan.SuiteID, plan.SuiteRevision, "suite")
		if err != nil {
			return relationStorageError(ctx, "plan suite revision", err)
		}
		var suite domain.Suite
		if err := decodeCanonical(document, &suite, func() error { return suite.Validate() }); err != nil {
			return storageCorrupt("plan suite revision")
		}
		if err := validateSuiteStorage(ctx, queryer, suite); err != nil {
			return relationStorageError(ctx, "plan suite revision", err)
		}
	}

	modelRevisions, err := validatePlanEntityRelations(ctx, queryer, "plan_models", "model", "model_id", "model_revision", plan.ID, plan.Revision, plan.ModelIDs)
	if err != nil {
		return err
	}
	channelRevisions, err := validatePlanEntityRelations(ctx, queryer, "plan_channels", "channel", "channel_id", "channel_revision", plan.ID, plan.Revision, plan.ChannelIDs)
	if err != nil {
		return err
	}
	if err := validatePlanCaseRelations(ctx, queryer, plan); err != nil {
		return err
	}
	return validatePlanMappingRelations(ctx, queryer, plan, modelRevisions, channelRevisions)
}

func validatePlanEntityRelations(ctx context.Context, queryer relationQueryer, table, kind, idColumn, revisionColumn, planID string, planRevision uint64, expected []string) (map[string]uint64, error) {
	rows, err := queryer.QueryContext(ctx, fmt.Sprintf(`
		SELECT position, %s, %s FROM %s
		WHERE plan_id = ? AND plan_revision = ? ORDER BY position
	`, idColumn, revisionColumn, table), planID, planRevision)
	if err != nil {
		return nil, relationStorageError(ctx, "plan "+kind+" relations", err)
	}
	revisions := make(map[string]uint64, len(expected))
	position := 0
	for rows.Next() {
		var storedPosition int
		var id string
		var revision int64
		if err := rows.Scan(&storedPosition, &id, &revision); err != nil {
			rows.Close()
			return nil, relationStorageError(ctx, "plan "+kind+" relations", err)
		}
		if position >= len(expected) || storedPosition != position || id != expected[position] || revision < 1 {
			rows.Close()
			return nil, storageCorrupt("plan " + kind + " relations")
		}
		revisions[id] = uint64(revision)
		position++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, relationStorageError(ctx, "plan "+kind+" relations", err)
	}
	if position != len(expected) {
		rows.Close()
		return nil, storageCorrupt("plan " + kind + " relations")
	}
	if err := rows.Close(); err != nil {
		return nil, relationStorageError(ctx, "plan "+kind+" relations", err)
	}
	targetTable := "models"
	if kind == "channel" {
		targetTable = "channels"
	}
	for id, revision := range revisions {
		document, err := exactDocument(ctx, queryer, targetTable, id, revision, kind)
		if err != nil {
			return nil, relationStorageError(ctx, "plan "+kind+" revision", err)
		}
		if kind == "model" {
			var model domain.Model
			if err := decodeCanonical(document, &model, func() error { return model.Validate() }); err != nil {
				return nil, storageCorrupt("plan model revision")
			}
		} else {
			var channel domain.Channel
			if err := decodeCanonical(document, &channel, func() error { return channel.Validate() }); err != nil {
				return nil, storageCorrupt("plan channel revision")
			}
			if err := validateChannelStorage(ctx, queryer, channel); err != nil {
				return nil, relationStorageError(ctx, "plan channel revision", err)
			}
		}
	}
	return revisions, nil
}

func validatePlanCaseRelations(ctx context.Context, queryer relationQueryer, plan domain.Plan) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT position, case_id, case_revision FROM plan_cases
		WHERE plan_id = ? AND plan_revision = ? ORDER BY position
	`, plan.ID, plan.Revision)
	if err != nil {
		return relationStorageError(ctx, "plan case relations", err)
	}
	stored := make([]domain.CaseRevisionRef, 0, len(plan.Cases))
	position := 0
	for rows.Next() {
		var storedPosition int
		var id string
		var revision int64
		if err := rows.Scan(&storedPosition, &id, &revision); err != nil {
			rows.Close()
			return relationStorageError(ctx, "plan case relations", err)
		}
		if position >= len(plan.Cases) {
			rows.Close()
			return storageCorrupt("plan case relations")
		}
		want := plan.Cases[position]
		if storedPosition != position || id != want.CaseID || revision < 1 || uint64(revision) != want.Revision {
			rows.Close()
			return storageCorrupt("plan case relations")
		}
		stored = append(stored, want)
		position++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "plan case relations", err)
	}
	if position != len(plan.Cases) {
		rows.Close()
		return storageCorrupt("plan case relations")
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "plan case relations", err)
	}
	for _, ref := range stored {
		document, err := exactDocument(ctx, queryer, "test_cases", ref.CaseID, ref.Revision, "test case")
		if err != nil {
			return relationStorageError(ctx, "plan case revision", err)
		}
		var testCase domain.TestCase
		if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
			return storageCorrupt("plan case revision")
		}
	}
	return nil
}

func validatePlanMappingRelations(ctx context.Context, queryer relationQueryer, plan domain.Plan, modelRevisions, channelRevisions map[string]uint64) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT position, channel_id, model_id, mapping_id, mapping_revision
		FROM plan_channel_models WHERE plan_id = ? AND plan_revision = ? ORDER BY position
	`, plan.ID, plan.Revision)
	if err != nil {
		return relationStorageError(ctx, "plan channel model relations", err)
	}
	type storedMapping struct {
		position           int
		channelID, modelID string
		mappingID          string
		mappingRevision    int64
	}
	stored := make([]storedMapping, 0, len(plan.ChannelIDs)*len(plan.ModelIDs))
	for rows.Next() {
		var item storedMapping
		if err := rows.Scan(&item.position, &item.channelID, &item.modelID, &item.mappingID, &item.mappingRevision); err != nil {
			rows.Close()
			return relationStorageError(ctx, "plan channel model relations", err)
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "plan channel model relations", err)
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "plan channel model relations", err)
	}
	if len(stored) != len(plan.ChannelIDs)*len(plan.ModelIDs) {
		return storageCorrupt("plan channel model relations")
	}
	position := 0
	for _, channelID := range plan.ChannelIDs {
		for _, modelID := range plan.ModelIDs {
			item := stored[position]
			if item.position != position || item.channelID != channelID || item.modelID != modelID || item.mappingRevision < 1 {
				return storageCorrupt("plan channel model relations")
			}
			document, err := exactDocument(ctx, queryer, "channel_models", item.mappingID, uint64(item.mappingRevision), "channel model")
			if err != nil {
				return relationStorageError(ctx, "plan channel model revision", err)
			}
			var mapping domain.ChannelModel
			if err := decodeCanonical(document, &mapping, func() error { return mapping.Validate() }); err != nil || mapping.ChannelID != channelID || mapping.ModelID != modelID {
				return storageCorrupt("plan channel model revision")
			}
			if err := validateChannelModelStorage(ctx, queryer, mapping); err != nil {
				return relationStorageError(ctx, "plan channel model revision", err)
			}
			var storedChannelRevision, storedModelRevision int64
			if err := queryer.QueryRowContext(ctx, `
				SELECT channel_revision, model_revision FROM channel_models WHERE id = ? AND revision = ?
			`, item.mappingID, item.mappingRevision).Scan(&storedChannelRevision, &storedModelRevision); err != nil {
				return relationStorageError(ctx, "plan channel model revision", err)
			}
			if storedChannelRevision < 1 || storedModelRevision < 1 ||
				uint64(storedChannelRevision) != channelRevisions[channelID] || uint64(storedModelRevision) != modelRevisions[modelID] {
				return storageCorrupt("plan channel model revision")
			}
			position++
		}
	}
	return nil
}

func validateIntegrationStorage(ctx context.Context, queryer rowQueryer, integration domain.Integration) error {
	var credentialID sql.NullString
	var credentialRevision sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `
		SELECT credential_id, credential_revision FROM integrations WHERE id = ? AND revision = ?
	`, integration.ID, integration.Revision).Scan(&credentialID, &credentialRevision); err != nil {
		return relationStorageError(ctx, "integration credential relation", err)
	}
	if integration.CredentialID == "" {
		if credentialID.Valid || credentialRevision.Valid {
			return storageCorrupt("integration credential relation")
		}
		return nil
	}
	if !credentialID.Valid || !credentialRevision.Valid || credentialID.String != integration.CredentialID || credentialRevision.Int64 < 1 {
		return storageCorrupt("integration credential relation")
	}
	document, err := exactDocument(ctx, queryer, "credential_refs", credentialID.String, uint64(credentialRevision.Int64), "credential reference")
	if err != nil {
		return relationStorageError(ctx, "integration credential relation", err)
	}
	var credential domain.CredentialRef
	if err := decodeCanonical(document, &credential, func() error { return credential.Validate() }); err != nil ||
		credential.Purpose != domain.CredentialIntegrationAdmin {
		return storageCorrupt("integration credential relation")
	}
	if err := validateCredentialStorage(ctx, queryer, credential); err != nil {
		return relationStorageError(ctx, "integration credential relation", err)
	}
	return nil
}

func validateReportStorage(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	run, err := queryCurrentRun(ctx, queryer, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report run relation", err)
	}
	if run.Status() != report.RunStatus || !reflect.DeepEqual(run.Snapshot(), report.PlanSnapshot) {
		return storageCorrupt("report run relation")
	}
	if err := validateStoredRunReferences(ctx, queryer, run); err != nil {
		return err
	}
	var sealed int
	if err := queryer.QueryRowContext(ctx, `SELECT sealed FROM execution_runs WHERE id = ?`, report.RunID).Scan(&sealed); err != nil {
		return relationStorageError(ctx, "report run seal", err)
	}
	if sealed != 1 {
		return storageCorrupt("report run seal")
	}
	if err := validateReportAttachments(ctx, queryer, report); err != nil {
		return err
	}
	if err := validateReportResults(ctx, queryer, report); err != nil {
		return err
	}
	return validateReportEvidence(ctx, queryer, report)
}

func validateReportAttachments(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT link.position, artifact.id, artifact.run_id, artifact.name,
		       artifact.relative_path, artifact.sha256, artifact.media_type,
		       artifact.redacted, artifact.document_json
		FROM report_attachments AS link
		JOIN artifacts AS artifact ON artifact.id = link.artifact_id
		WHERE link.report_id = ? ORDER BY link.position
	`, report.ID)
	if err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	defer rows.Close()
	position := 0
	for rows.Next() {
		var storedPosition, redacted int
		var id, runID, name, relativePath, digest, mediaType string
		var document []byte
		if err := rows.Scan(&storedPosition, &id, &runID, &name, &relativePath, &digest, &mediaType, &redacted, &document); err != nil {
			return relationStorageError(ctx, "report attachment relations", err)
		}
		if position >= len(report.Attachments) {
			return storageCorrupt("report attachment relations")
		}
		var attachment domain.ReportAttachment
		if err := decodeCanonical(document, &attachment, func() error { return attachment.Validate(report.RunID) }); err != nil {
			return storageCorrupt("report attachment document")
		}
		want := report.Attachments[position]
		if storedPosition != position || !reflect.DeepEqual(attachment, want) || id != attachment.ArtifactID ||
			runID != attachment.RunID || name != attachment.Name || relativePath != attachment.RelativePath ||
			digest != attachment.SHA256 || mediaType != attachment.MediaType || redacted != boolInt(attachment.Redacted) {
			return storageCorrupt("report attachment relations")
		}
		position++
	}
	if err := rows.Err(); err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	if position != len(report.Attachments) {
		return storageCorrupt("report attachment relations")
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	var artifactCount int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts WHERE run_id = ?`, report.RunID).Scan(&artifactCount); err != nil {
		return relationStorageError(ctx, "report attachment artifacts", err)
	}
	if artifactCount != len(report.Attachments) {
		return storageCorrupt("report attachment artifacts")
	}
	return nil
}

func validateReportResults(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, case_id, request_id, document_json
		FROM case_results
		WHERE run_id = ? AND request_id IS NULL
		ORDER BY created_at, id
	`, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report result collection", err)
	}
	storedRows := make([]storedResultRow, 0, len(report.CaseResults))
	for rows.Next() {
		var row storedResultRow
		if err := row.scan(rows); err != nil {
			rows.Close()
			return relationStorageError(ctx, "report result collection", err)
		}
		storedRows = append(storedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "report result collection", err)
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report result collection", err)
	}
	if len(storedRows) != len(report.CaseResults) {
		return storageCorrupt("report result collection")
	}
	stored := make(map[string]domain.Result, len(storedRows))
	for _, row := range storedRows {
		result, err := row.decode(report.RunID)
		if err != nil {
			return storageCorrupt("report result collection")
		}
		stored[result.ID] = result
	}
	for _, want := range report.CaseResults {
		if got, exists := stored[want.ID]; !exists || !canonicalValuesEqual(got, want) {
			return storageCorrupt("report result collection")
		}
	}
	return nil
}

func validateReportEvidence(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
		FROM evidence WHERE run_id = ? ORDER BY created_at, id
	`, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report evidence collection", err)
	}
	storedRows := make([]storedEvidenceRow, 0, len(report.Evidence))
	for rows.Next() {
		var row storedEvidenceRow
		if err := row.scan(rows); err != nil {
			rows.Close()
			return relationStorageError(ctx, "report evidence collection", err)
		}
		storedRows = append(storedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "report evidence collection", err)
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report evidence collection", err)
	}
	if len(storedRows) != len(report.Evidence) {
		return storageCorrupt("report evidence collection")
	}
	stored := make(map[string]domain.Evidence, len(storedRows))
	for _, row := range storedRows {
		evidence, err := row.decode(report.RunID)
		if err != nil {
			return storageCorrupt("report evidence collection")
		}
		stored[evidence.ID] = evidence
	}
	for _, want := range report.Evidence {
		if got, exists := stored[want.ID]; !exists || !reflect.DeepEqual(got, want) {
			return storageCorrupt("report evidence collection")
		}
	}
	return nil
}

func storageCorrupt(kind string) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, kind)
}

func relationStorageError(ctx context.Context, kind string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return storageCorrupt(kind)
}

func canonicalValuesEqual(left, right any) bool {
	leftDocument, leftErr := marshalCanonical(left)
	rightDocument, rightErr := marshalCanonical(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDocument, rightDocument)
}
