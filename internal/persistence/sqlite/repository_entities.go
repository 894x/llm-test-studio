package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (repository *Repository) CreateCredentialRef(ctx context.Context, credential domain.CredentialRef) error {
	return repository.writeCredentialRef(ctx, nil, credential)
}

func (repository *Repository) UpdateCredentialRef(ctx context.Context, expectedRevision uint64, credential domain.CredentialRef) error {
	return repository.writeCredentialRef(ctx, &expectedRevision, credential)
}

// DeleteCredentialRef removes a never-used, single-revision credential record.
// It exists for compensating an atomic channel+key create when the channel
// write fails; historical or referenced credentials are never erased.
func (repository *Repository) DeleteCredentialRef(ctx context.Context, id string, expectedRevision uint64) error {
	if ctx == nil || !domain.IsUUID(id) || expectedRevision != 1 {
		return errors.New("credential reference delete is invalid")
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential reference delete: %w", err)
	}
	defer tx.Rollback()
	var count int
	var revision uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(revision), 0) FROM credential_refs WHERE id = ?`, id).Scan(&count, &revision); err != nil {
		return fmt.Errorf("inspect credential reference delete: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: credential reference", ErrNotFound)
	}
	if count != 1 || revision != expectedRevision {
		return fmt.Errorf("%w: credential reference", ErrConflict)
	}
	var referenced int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM channels WHERE credential_id = ?
			UNION ALL SELECT 1 FROM integrations WHERE credential_id = ?
		)
	`, id, id).Scan(&referenced); err != nil {
		return fmt.Errorf("inspect credential reference usage: %w", err)
	}
	if referenced != 0 {
		return fmt.Errorf("%w: credential reference", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_refs WHERE id = ? AND revision = ?`, id, expectedRevision); err != nil {
		return classifyWriteError("delete credential reference", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit credential reference delete", err)
	}
	return nil
}

func (repository *Repository) writeCredentialRef(ctx context.Context, expected *uint64, credential domain.CredentialRef) error {
	if err := credential.Validate(); err != nil {
		return errors.New("credential reference is invalid")
	}
	if _, err := credentials.StoreRefFromCredential(credential); err != nil {
		return errors.New("credential store reference is not canonical for its purpose and id")
	}
	document, err := marshalCanonical(credential)
	if err != nil {
		return fmt.Errorf("encode credential reference: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential reference write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "credential_refs", credential.EntityMeta, expected); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO credential_refs(
			id, schema_version, revision, created_at, updated_at,
			store_ref, purpose, masked_suffix, fingerprint, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, credential.ID, credential.SchemaVersion, credential.Revision, formatTime(credential.CreatedAt), formatTime(credential.UpdatedAt),
		credential.StoreRef, credential.Purpose, credential.MaskedSuffix, credential.Fingerprint, document); err != nil {
		return classifyWriteError("write credential reference", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit credential reference", err)
	}
	return nil
}

func (repository *Repository) GetCredentialRef(ctx context.Context, id string) (domain.CredentialRef, error) {
	document, err := repository.getLatestDocument(ctx, "credential_refs", id, "credential reference")
	if err != nil {
		return domain.CredentialRef{}, err
	}
	var credential domain.CredentialRef
	if err := decodeCanonical(document, &credential, func() error { return credential.Validate() }); err != nil {
		return domain.CredentialRef{}, fmt.Errorf("%w: credential reference document", ErrCorrupt)
	}
	if err := validateCredentialStorage(ctx, repository.conn, credential); err != nil {
		return domain.CredentialRef{}, err
	}
	return credential, nil
}

func (repository *Repository) ListCredentialRefs(ctx context.Context) ([]domain.CredentialRef, error) {
	documents, err := repository.listLatestDocuments(ctx, "credential_refs", "credential references")
	if err != nil {
		return nil, err
	}
	result := make([]domain.CredentialRef, 0, len(documents))
	for _, document := range documents {
		var credential domain.CredentialRef
		if err := decodeCanonical(document, &credential, func() error { return credential.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: credential reference document", ErrCorrupt)
		}
		if err := validateCredentialStorage(ctx, repository.conn, credential); err != nil {
			return nil, err
		}
		result = append(result, credential)
	}
	return result, nil
}

func (repository *Repository) CreateChannel(ctx context.Context, channel domain.Channel) error {
	return repository.writeChannel(ctx, nil, channel)
}

func (repository *Repository) UpdateChannel(ctx context.Context, expectedRevision uint64, channel domain.Channel) error {
	return repository.writeChannel(ctx, &expectedRevision, channel)
}

func (repository *Repository) DeleteChannel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "channels", "channel", id, expectedRevision)
}

func (repository *Repository) writeChannel(ctx context.Context, expected *uint64, channel domain.Channel) error {
	if err := channel.Validate(); err != nil {
		return fmt.Errorf("validate channel: %w", err)
	}
	document, err := marshalCanonical(channel)
	if err != nil {
		return fmt.Errorf("encode channel: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin channel write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "channels", channel.EntityMeta, expected); err != nil {
		return err
	}
	var credentialID any
	var credentialRevision any
	if channel.CredentialID != "" {
		revision, err := credentialRevisionForPurpose(ctx, tx, channel.CredentialID, domain.CredentialChannelAPIKey)
		if err != nil {
			return err
		}
		credentialID, credentialRevision = channel.CredentialID, revision
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channels(
			id, schema_version, revision, created_at, updated_at,
			credential_id, credential_revision, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, channel.ID, channel.SchemaVersion, channel.Revision, formatTime(channel.CreatedAt), formatTime(channel.UpdatedAt), credentialID, credentialRevision, document); err != nil {
		return classifyWriteError("write channel", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit channel", err)
	}
	return nil
}

func (repository *Repository) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	document, err := repository.getLatestDocument(ctx, "channels", id, "channel")
	if err != nil {
		return domain.Channel{}, err
	}
	var channel domain.Channel
	if err := decodeCanonical(document, &channel, func() error { return channel.Validate() }); err != nil {
		return domain.Channel{}, fmt.Errorf("%w: channel document", ErrCorrupt)
	}
	if err := validateChannelStorage(ctx, repository.conn, channel); err != nil {
		return domain.Channel{}, err
	}
	return channel, nil
}

func (repository *Repository) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	documents, err := repository.listLatestDocuments(ctx, "channels", "channels")
	if err != nil {
		return nil, err
	}
	result := make([]domain.Channel, 0, len(documents))
	for _, document := range documents {
		var channel domain.Channel
		if err := decodeCanonical(document, &channel, func() error { return channel.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: channel document", ErrCorrupt)
		}
		if err := validateChannelStorage(ctx, repository.conn, channel); err != nil {
			return nil, err
		}
		result = append(result, channel)
	}
	return result, nil
}

func (repository *Repository) CreateChannelModel(ctx context.Context, mapping domain.ChannelModel) error {
	return repository.writeChannelModel(ctx, nil, mapping)
}

func (repository *Repository) UpdateChannelModel(ctx context.Context, expectedRevision uint64, mapping domain.ChannelModel) error {
	return repository.writeChannelModel(ctx, &expectedRevision, mapping)
}

func (repository *Repository) DeleteChannelModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "channel_models", "channel model", id, expectedRevision)
}

func (repository *Repository) writeChannelModel(ctx context.Context, expected *uint64, mapping domain.ChannelModel) error {
	if err := mapping.Validate(); err != nil {
		return fmt.Errorf("validate channel model: %w", err)
	}
	document, err := marshalCanonical(mapping)
	if err != nil {
		return fmt.Errorf("encode channel model: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin channel model write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "channel_models", mapping.EntityMeta, expected); err != nil {
		return err
	}
	if expected != nil {
		currentDocument, err := exactDocument(ctx, tx, "channel_models", mapping.ID, *expected, "channel model")
		if err != nil {
			return err
		}
		var current domain.ChannelModel
		if err := decodeCanonical(currentDocument, &current, func() error { return current.Validate() }); err != nil {
			return fmt.Errorf("%w: channel model document", ErrCorrupt)
		}
		if current.ChannelID != mapping.ChannelID || current.ModelID != mapping.ModelID {
			return errors.New("updated channel model must preserve its channel and model identity")
		}
	}
	channelRevision, err := latestRevision(ctx, tx, "channels", mapping.ChannelID, "channel")
	if err != nil {
		return err
	}
	modelRevision, err := latestRevision(ctx, tx, "models", mapping.ModelID, "model")
	if err != nil {
		return err
	}
	channelDocument, err := exactDocument(ctx, tx, "channels", mapping.ChannelID, channelRevision, "channel")
	if err != nil {
		return err
	}
	var channel domain.Channel
	if err := decodeCanonical(channelDocument, &channel, func() error { return channel.Validate() }); err != nil {
		return fmt.Errorf("%w: channel document", ErrCorrupt)
	}
	modelDocument, err := exactDocument(ctx, tx, "models", mapping.ModelID, modelRevision, "model")
	if err != nil {
		return err
	}
	var model domain.Model
	if err := decodeCanonical(modelDocument, &model, func() error { return model.Validate() }); err != nil {
		return fmt.Errorf("%w: model document", ErrCorrupt)
	}
	if channel.Protocol != model.Protocol {
		return errors.New("channel model requires matching channel and model protocols")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channel_models(
			id, schema_version, revision, created_at, updated_at,
			channel_id, channel_revision, model_id, model_revision, binding_key, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, mapping.ID, mapping.SchemaVersion, mapping.Revision, formatTime(mapping.CreatedAt), formatTime(mapping.UpdatedAt),
		mapping.ChannelID, channelRevision, mapping.ModelID, modelRevision, mapping.ChannelID+"|"+mapping.ModelID, document); err != nil {
		return classifyWriteError("write channel model", err)
	}
	if err := validateChannelModelStorage(ctx, tx, mapping); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit channel model", err)
	}
	return nil
}

func (repository *Repository) GetChannelModel(ctx context.Context, id string) (domain.ChannelModel, error) {
	document, err := repository.getLatestDocument(ctx, "channel_models", id, "channel model")
	if err != nil {
		return domain.ChannelModel{}, err
	}
	var mapping domain.ChannelModel
	if err := decodeCanonical(document, &mapping, func() error { return mapping.Validate() }); err != nil {
		return domain.ChannelModel{}, fmt.Errorf("%w: channel model document", ErrCorrupt)
	}
	if err := validateChannelModelStorage(ctx, repository.conn, mapping); err != nil {
		return domain.ChannelModel{}, err
	}
	return mapping, nil
}

func (repository *Repository) ListChannelModels(ctx context.Context) ([]domain.ChannelModel, error) {
	documents, err := repository.listLatestDocuments(ctx, "channel_models", "channel models")
	if err != nil {
		return nil, err
	}
	result := make([]domain.ChannelModel, 0, len(documents))
	for _, document := range documents {
		var mapping domain.ChannelModel
		if err := decodeCanonical(document, &mapping, func() error { return mapping.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: channel model document", ErrCorrupt)
		}
		if err := validateChannelModelStorage(ctx, repository.conn, mapping); err != nil {
			return nil, err
		}
		result = append(result, mapping)
	}
	return result, nil
}

func (repository *Repository) CreateTestCase(ctx context.Context, testCase domain.TestCase) error {
	return repository.writeTestCase(ctx, nil, testCase)
}

func (repository *Repository) UpdateTestCase(ctx context.Context, expectedRevision uint64, testCase domain.TestCase) error {
	return repository.writeTestCase(ctx, &expectedRevision, testCase)
}

func (repository *Repository) DeleteTestCase(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "test_cases", "test case", id, expectedRevision)
}

func (repository *Repository) writeTestCase(ctx context.Context, expected *uint64, testCase domain.TestCase) error {
	if err := testCase.Validate(); err != nil {
		return fmt.Errorf("validate test case: %w", err)
	}
	document, err := marshalCanonical(testCase)
	if err != nil {
		return fmt.Errorf("encode test case: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin test case write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "test_cases", testCase.EntityMeta, expected); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, testCase.ID, testCase.SchemaVersion, testCase.Revision, formatTime(testCase.CreatedAt), formatTime(testCase.UpdatedAt), document); err != nil {
		return classifyWriteError("write test case", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit test case", err)
	}
	return nil
}

func (repository *Repository) GetTestCase(ctx context.Context, id string) (domain.TestCase, error) {
	var pending []byte
	err := repository.conn.QueryRowContext(ctx, `SELECT document_json FROM pending_test_case_snapshots WHERE id = ? ORDER BY updated_at DESC LIMIT 1`, id).Scan(&pending)
	if err == nil {
		var testCase domain.TestCase
		if err := decodeCanonical(pending, &testCase, func() error { return testCase.Validate() }); err != nil {
			return domain.TestCase{}, fmt.Errorf("%w: pending test case document", ErrCorrupt)
		}
		return testCase, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.TestCase{}, fmt.Errorf("get pending test case: %w", err)
	}
	document, err := repository.getLatestDocument(ctx, "test_cases", id, "test case")
	if err != nil {
		return domain.TestCase{}, err
	}
	var testCase domain.TestCase
	if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
		return domain.TestCase{}, fmt.Errorf("%w: test case document", ErrCorrupt)
	}
	return testCase, nil
}

func (repository *Repository) GetTestCaseRevision(ctx context.Context, id string, revision uint64) (domain.TestCase, error) {
	document, err := repository.getExactDocument(ctx, "test_cases", id, revision, "test case")
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return domain.TestCase{}, err
		}
		if pendingErr := repository.conn.QueryRowContext(ctx, `SELECT document_json FROM pending_test_case_snapshots WHERE id = ? AND revision = ?`, id, revision).Scan(&document); pendingErr != nil {
			if errors.Is(pendingErr, sql.ErrNoRows) {
				return domain.TestCase{}, err
			}
			return domain.TestCase{}, fmt.Errorf("get pending test case revision: %w", pendingErr)
		}
	}
	var testCase domain.TestCase
	if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
		return domain.TestCase{}, fmt.Errorf("%w: test case document", ErrCorrupt)
	}
	return testCase, nil
}

func (repository *Repository) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	documents, err := repository.listLatestDocuments(ctx, "test_cases", "test cases")
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, 0, len(documents))
	for _, document := range documents {
		var testCase domain.TestCase
		if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: test case document", ErrCorrupt)
		}
		result = append(result, testCase)
	}
	return result, nil
}

func (repository *Repository) CreateSuite(ctx context.Context, suite domain.Suite) error {
	return repository.writeSuite(ctx, nil, suite)
}

func (repository *Repository) UpdateSuite(ctx context.Context, expectedRevision uint64, suite domain.Suite) error {
	return repository.writeSuite(ctx, &expectedRevision, suite)
}

func (repository *Repository) DeleteSuite(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "test_suites", "suite", id, expectedRevision)
}

func (repository *Repository) writeSuite(ctx context.Context, expected *uint64, suite domain.Suite) error {
	if err := suite.Validate(); err != nil {
		return fmt.Errorf("validate suite: %w", err)
	}
	document, err := marshalCanonical(suite)
	if err != nil {
		return fmt.Errorf("encode suite: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin suite write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "test_suites", suite.EntityMeta, expected); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_suites(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, suite.ID, suite.SchemaVersion, suite.Revision, formatTime(suite.CreatedAt), formatTime(suite.UpdatedAt), document); err != nil {
		return classifyWriteError("write suite", err)
	}
	for position, ref := range suite.Cases {
		if err := promotePendingCaseSnapshot(ctx, tx, ref); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO suite_cases(suite_id, suite_revision, position, case_id, case_revision)
			VALUES(?, ?, ?, ?, ?)
		`, suite.ID, suite.Revision, position, ref.CaseID, ref.Revision); err != nil {
			return classifyWriteError("write suite cases", err)
		}
	}
	if err := validateSuiteStorage(ctx, tx, suite); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit suite", err)
	}
	return nil
}

func (repository *Repository) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	document, err := repository.getLatestDocument(ctx, "test_suites", id, "suite")
	if err != nil {
		return domain.Suite{}, err
	}
	var suite domain.Suite
	if err := decodeCanonical(document, &suite, func() error { return suite.Validate() }); err != nil {
		return domain.Suite{}, fmt.Errorf("%w: suite document", ErrCorrupt)
	}
	if err := validateSuiteStorage(ctx, repository.conn, suite); err != nil {
		return domain.Suite{}, err
	}
	return suite, nil
}

func (repository *Repository) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	documents, err := repository.listLatestDocuments(ctx, "test_suites", "suites")
	if err != nil {
		return nil, err
	}
	result := make([]domain.Suite, 0, len(documents))
	for _, document := range documents {
		var suite domain.Suite
		if err := decodeCanonical(document, &suite, func() error { return suite.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: suite document", ErrCorrupt)
		}
		if err := validateSuiteStorage(ctx, repository.conn, suite); err != nil {
			return nil, err
		}
		result = append(result, suite)
	}
	return result, nil
}

func (repository *Repository) CreatePlan(ctx context.Context, plan domain.Plan) error {
	return repository.writePlan(ctx, nil, plan, false)
}

func (repository *Repository) UpdatePlan(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	return repository.writePlan(ctx, &expectedRevision, plan, false)
}

// CreatePlanWithExternalSuite persists a plan whose Suite definition is owned
// by the filesystem catalog. The immutable suite identity stays in the plan
// document and deliberately has no SQLite foreign-key row.
func (repository *Repository) CreatePlanWithExternalSuite(ctx context.Context, plan domain.Plan) error {
	return repository.writePlan(ctx, nil, plan, true)
}

func (repository *Repository) UpdatePlanWithExternalSuite(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	return repository.writePlan(ctx, &expectedRevision, plan, true)
}

func (repository *Repository) DeletePlan(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "test_plans", "plan", id, expectedRevision)
}

func (repository *Repository) writePlan(ctx context.Context, expected *uint64, plan domain.Plan, externalSuite bool) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate plan: %w", err)
	}
	document, err := marshalCanonical(plan)
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plan write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "test_plans", plan.EntityMeta, expected); err != nil {
		return err
	}
	var suiteID any
	var suiteRevision any
	if plan.SuiteID != "" && !externalSuite {
		if err := requireExactVersion(ctx, tx, "test_suites", plan.SuiteID, plan.SuiteRevision, "suite"); err != nil {
			return err
		}
		suiteID, suiteRevision = plan.SuiteID, plan.SuiteRevision
	}
	modelRevisions := make([]uint64, len(plan.ModelIDs))
	for index, id := range plan.ModelIDs {
		modelRevisions[index], err = latestRevision(ctx, tx, "models", id, "model")
		if err != nil {
			return err
		}
	}
	channelRevisions := make([]uint64, len(plan.ChannelIDs))
	for index, id := range plan.ChannelIDs {
		channelRevisions[index], err = latestRevision(ctx, tx, "channels", id, "channel")
		if err != nil {
			return err
		}
	}
	for _, ref := range plan.Cases {
		if err := promotePendingCaseSnapshot(ctx, tx, ref); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_plans(
			id, schema_version, revision, created_at, updated_at,
			suite_id, suite_revision, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, plan.ID, plan.SchemaVersion, plan.Revision, formatTime(plan.CreatedAt), formatTime(plan.UpdatedAt), suiteID, suiteRevision, document); err != nil {
		return classifyWriteError("write plan", err)
	}
	for position, id := range plan.ModelIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_models(plan_id, plan_revision, position, model_id, model_revision) VALUES(?, ?, ?, ?, ?)`, plan.ID, plan.Revision, position, id, modelRevisions[position]); err != nil {
			return classifyWriteError("write plan models", err)
		}
	}
	for position, id := range plan.ChannelIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_channels(plan_id, plan_revision, position, channel_id, channel_revision) VALUES(?, ?, ?, ?, ?)`, plan.ID, plan.Revision, position, id, channelRevisions[position]); err != nil {
			return classifyWriteError("write plan channels", err)
		}
	}
	for position, ref := range plan.Cases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_cases(plan_id, plan_revision, position, case_id, case_revision) VALUES(?, ?, ?, ?, ?)`, plan.ID, plan.Revision, position, ref.CaseID, ref.Revision); err != nil {
			return classifyWriteError("write plan cases", err)
		}
	}
	mappingPosition := 0
	for channelPosition, channelID := range plan.ChannelIDs {
		for modelPosition, modelID := range plan.ModelIDs {
			var mappingID string
			var mappingRevision int64
			err := tx.QueryRowContext(ctx, `
				SELECT id, revision FROM channel_models
				WHERE channel_id = ? AND channel_revision = ?
				  AND model_id = ? AND model_revision = ?
				  AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'channel_models' AND entity_id = channel_models.id)
				ORDER BY revision DESC LIMIT 1
			`, channelID, channelRevisions[channelPosition], modelID, modelRevisions[modelPosition]).Scan(&mappingID, &mappingRevision)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: channel model mapping for plan", ErrNotFound)
			}
			if err != nil {
				return fmt.Errorf("resolve channel model mapping for plan: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO plan_channel_models(
					plan_id, plan_revision, position, channel_id, model_id, mapping_id, mapping_revision
				) VALUES(?, ?, ?, ?, ?, ?, ?)
			`, plan.ID, plan.Revision, mappingPosition, channelID, modelID, mappingID, mappingRevision); err != nil {
				return classifyWriteError("write plan channel models", err)
			}
			mappingPosition++
		}
	}
	if err := validatePlanStorage(ctx, tx, plan); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit plan", err)
	}
	return nil
}

func (repository *Repository) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	document, err := repository.getLatestDocument(ctx, "test_plans", id, "plan")
	if err != nil {
		return domain.Plan{}, err
	}
	plan, err := decodePlanDocument(document)
	if err != nil {
		return domain.Plan{}, err
	}
	if err := validatePlanStorage(ctx, repository.conn, plan); err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func (repository *Repository) GetPlanRevision(ctx context.Context, id string, revision uint64) (domain.Plan, error) {
	document, err := repository.getExactDocument(ctx, "test_plans", id, revision, "plan")
	if err != nil {
		return domain.Plan{}, err
	}
	plan, err := decodePlanDocument(document)
	if err != nil {
		return domain.Plan{}, err
	}
	if err := validatePlanStorage(ctx, repository.conn, plan); err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func (repository *Repository) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	documents, err := repository.listLatestDocuments(ctx, "test_plans", "plans")
	if err != nil {
		return nil, err
	}
	result := make([]domain.Plan, 0, len(documents))
	for _, document := range documents {
		plan, err := decodePlanDocument(document)
		if err != nil {
			return nil, err
		}
		if err := validatePlanStorage(ctx, repository.conn, plan); err != nil {
			return nil, err
		}
		result = append(result, plan)
	}
	return result, nil
}

func decodePlanDocument(document []byte) (domain.Plan, error) {
	var plan domain.Plan
	if err := decodeCanonical(document, &plan, func() error { return plan.Validate() }); err != nil {
		return domain.Plan{}, fmt.Errorf("%w: plan document", ErrCorrupt)
	}
	return plan, nil
}

func (repository *Repository) CreateIntegration(ctx context.Context, integration domain.Integration) error {
	return repository.writeIntegration(ctx, nil, integration)
}

func (repository *Repository) UpdateIntegration(ctx context.Context, expectedRevision uint64, integration domain.Integration) error {
	return repository.writeIntegration(ctx, &expectedRevision, integration)
}

func (repository *Repository) writeIntegration(ctx context.Context, expected *uint64, integration domain.Integration) error {
	if err := integration.Validate(); err != nil {
		return fmt.Errorf("validate integration: %w", err)
	}
	document, err := marshalCanonical(integration)
	if err != nil {
		return fmt.Errorf("encode integration: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin integration write: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "integrations", integration.EntityMeta, expected); err != nil {
		return err
	}
	var credentialID any
	var credentialRevision any
	if integration.CredentialID != "" {
		revision, err := credentialRevisionForPurpose(ctx, tx, integration.CredentialID, domain.CredentialIntegrationAdmin)
		if err != nil {
			return err
		}
		credentialID, credentialRevision = integration.CredentialID, revision
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO integrations(
			id, schema_version, revision, created_at, updated_at,
			credential_id, credential_revision, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, integration.ID, integration.SchemaVersion, integration.Revision, formatTime(integration.CreatedAt), formatTime(integration.UpdatedAt), credentialID, credentialRevision, document); err != nil {
		return classifyWriteError("write integration", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit integration", err)
	}
	return nil
}

func credentialRevisionForPurpose(ctx context.Context, tx *sql.Tx, id string, purpose domain.CredentialPurpose) (uint64, error) {
	revision, err := latestRevision(ctx, tx, "credential_refs", id, "credential reference")
	if err != nil {
		return 0, err
	}
	document, err := exactDocument(ctx, tx, "credential_refs", id, revision, "credential reference")
	if err != nil {
		return 0, err
	}
	var credential domain.CredentialRef
	if err := decodeCanonical(document, &credential, func() error { return credential.Validate() }); err != nil {
		return 0, fmt.Errorf("%w: credential reference document", ErrCorrupt)
	}
	if err := validateCredentialStorage(ctx, tx, credential); err != nil {
		return 0, err
	}
	if credential.Purpose != purpose {
		return 0, errors.New("credential purpose is not valid for this owner")
	}
	return revision, nil
}

func (repository *Repository) GetIntegration(ctx context.Context, id string) (domain.Integration, error) {
	document, err := repository.getLatestDocument(ctx, "integrations", id, "integration")
	if err != nil {
		return domain.Integration{}, err
	}
	var integration domain.Integration
	if err := decodeCanonical(document, &integration, func() error { return integration.Validate() }); err != nil {
		return domain.Integration{}, fmt.Errorf("%w: integration document", ErrCorrupt)
	}
	if err := validateIntegrationStorage(ctx, repository.conn, integration); err != nil {
		return domain.Integration{}, err
	}
	return integration, nil
}

func (repository *Repository) ListIntegrations(ctx context.Context) ([]domain.Integration, error) {
	documents, err := repository.listLatestDocuments(ctx, "integrations", "integrations")
	if err != nil {
		return nil, err
	}
	result := make([]domain.Integration, 0, len(documents))
	for _, document := range documents {
		var integration domain.Integration
		if err := decodeCanonical(document, &integration, func() error { return integration.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: integration document", ErrCorrupt)
		}
		if err := validateIntegrationStorage(ctx, repository.conn, integration); err != nil {
			return nil, err
		}
		result = append(result, integration)
	}
	return result, nil
}

func checkVersionedWrite(ctx context.Context, tx *sql.Tx, table string, meta domain.EntityMeta, expected *uint64) error {
	if meta.Revision > math.MaxInt64 {
		return errors.New("entity revision exceeds SQLite integer range")
	}
	var tombstoned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_tombstones WHERE entity_table = ? AND entity_id = ?`, table, meta.ID).Scan(&tombstoned); err != nil {
		return fmt.Errorf("inspect entity tombstone: %w", err)
	}
	if tombstoned != 0 {
		if expected == nil {
			return fmt.Errorf("%w: entity id is retired", ErrConflict)
		}
		return fmt.Errorf("%w: entity", ErrNotFound)
	}
	var currentRevision int64
	var createdAt, updatedAt string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT revision, created_at, updated_at FROM %s
		WHERE id = ? ORDER BY revision DESC LIMIT 1
	`, table), meta.ID).Scan(&currentRevision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != nil {
			return fmt.Errorf("%w: entity", ErrNotFound)
		}
		if meta.Revision != 1 {
			return errors.New("new entity revision must be 1")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect entity revision: %w", err)
	}
	if expected == nil {
		return fmt.Errorf("%w: entity already exists", ErrConflict)
	}
	if *expected > math.MaxInt64 || uint64(currentRevision) != *expected || meta.Revision != *expected+1 {
		return fmt.Errorf("%w: entity", ErrConflict)
	}
	if createdAt != formatTime(meta.CreatedAt) {
		return errors.New("updated entity must preserve creation timestamp")
	}
	previousUpdate, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return fmt.Errorf("%w: invalid stored update timestamp", ErrCorrupt)
	}
	if meta.UpdatedAt.Before(previousUpdate) {
		return errors.New("updated entity timestamp must not move backwards")
	}
	return nil
}

func (repository *Repository) deleteVersionedEntity(ctx context.Context, table, kind, id string, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !domain.IsUUID(id) || expectedRevision < 1 {
		return fmt.Errorf("%w: %s", ErrNotFound, kind)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s delete: %w", kind, err)
	}
	defer tx.Rollback()
	currentRevision, err := latestRevision(ctx, tx, table, id, kind)
	if err != nil {
		return err
	}
	if currentRevision != expectedRevision {
		return fmt.Errorf("%w: %s", ErrConflict, kind)
	}
	referenced, err := hasLiveCatalogReferences(ctx, tx, table, id)
	if err != nil {
		return fmt.Errorf("inspect %s references: %w", kind, err)
	}
	if referenced {
		return fmt.Errorf("%w: %s is still referenced", ErrConflict, kind)
	}
	if expectedRevision == math.MaxUint64 {
		return fmt.Errorf("%w: %s", ErrConflict, kind)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO catalog_tombstones(entity_table, entity_id, deleted_revision, deleted_at)
		VALUES(?, ?, ?, ?)
	`, table, id, expectedRevision+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return classifyWriteError("write "+kind+" tombstone", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit "+kind+" delete", err)
	}
	return nil
}

func hasLiveCatalogReferences(ctx context.Context, tx *sql.Tx, table, id string) (bool, error) {
	queries := map[string]string{
		"models": `SELECT EXISTS(
			SELECT 1 FROM channel_models child WHERE child.model_id = ? AND child.revision = (SELECT MAX(revision) FROM channel_models WHERE id = child.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'channel_models' AND entity_id = child.id)
			UNION ALL SELECT 1 FROM plan_models ref JOIN test_plans owner ON owner.id = ref.plan_id AND owner.revision = ref.plan_revision WHERE ref.model_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_plans WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_plans' AND entity_id = owner.id)
		)`,
		"channels": `SELECT EXISTS(
			SELECT 1 FROM channel_models child WHERE child.channel_id = ? AND child.revision = (SELECT MAX(revision) FROM channel_models WHERE id = child.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'channel_models' AND entity_id = child.id)
			UNION ALL SELECT 1 FROM plan_channels ref JOIN test_plans owner ON owner.id = ref.plan_id AND owner.revision = ref.plan_revision WHERE ref.channel_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_plans WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_plans' AND entity_id = owner.id)
		)`,
		"channel_models": `SELECT EXISTS(SELECT 1 FROM plan_channel_models ref JOIN test_plans owner ON owner.id = ref.plan_id AND owner.revision = ref.plan_revision WHERE ref.mapping_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_plans WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_plans' AND entity_id = owner.id))`,
		"test_cases": `SELECT EXISTS(
			SELECT 1 FROM suite_cases ref JOIN test_suites owner ON owner.id = ref.suite_id AND owner.revision = ref.suite_revision WHERE ref.case_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_suites WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_suites' AND entity_id = owner.id)
			UNION ALL SELECT 1 FROM plan_cases ref JOIN test_plans owner ON owner.id = ref.plan_id AND owner.revision = ref.plan_revision WHERE ref.case_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_plans WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_plans' AND entity_id = owner.id)
			UNION ALL SELECT 1 FROM test_case_import_sources WHERE entity_id = ? AND retired_at IS NULL
		)`,
		"test_suites": `SELECT EXISTS(SELECT 1 FROM test_plans owner WHERE owner.suite_id = ? AND owner.revision = (SELECT MAX(revision) FROM test_plans WHERE id = owner.id) AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'test_plans' AND entity_id = owner.id))`,
		"test_plans":  `SELECT EXISTS(SELECT 1 FROM execution_run_revisions WHERE plan_id = ?)`,
	}
	query, exists := queries[table]
	if !exists {
		return false, fmt.Errorf("unsupported catalog table %q", table)
	}
	args := []any{}
	switch table {
	case "models", "channels":
		args = []any{id, id}
	case "test_cases":
		args = []any{id, id, id}
	default:
		args = []any{id}
	}
	var referenced int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&referenced); err != nil {
		return false, err
	}
	return referenced != 0, nil
}

func latestRevision(ctx context.Context, queryer rowQueryer, table, id, kind string) (uint64, error) {
	var tombstoned int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_tombstones WHERE entity_table = ? AND entity_id = ?`, table, id).Scan(&tombstoned); err != nil {
		return 0, fmt.Errorf("resolve %s tombstone: %w", kind, err)
	}
	if tombstoned != 0 {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, kind)
	}
	var revision sql.NullInt64
	err := queryer.QueryRowContext(ctx, fmt.Sprintf("SELECT MAX(revision) FROM %s WHERE id = ?", table), id).Scan(&revision)
	if err != nil {
		return 0, fmt.Errorf("resolve %s revision: %w", kind, err)
	}
	if !revision.Valid || revision.Int64 < 1 {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, kind)
	}
	return uint64(revision.Int64), nil
}

func requireExactVersion(ctx context.Context, queryer rowQueryer, table, id string, revision uint64, kind string) error {
	if revision > math.MaxInt64 {
		return fmt.Errorf("%w: %s revision", ErrNotFound, kind)
	}
	var tombstoned int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_tombstones WHERE entity_table = ? AND entity_id = ?`, table, id).Scan(&tombstoned); err != nil {
		return fmt.Errorf("resolve %s tombstone: %w", kind, err)
	}
	if tombstoned != 0 {
		return fmt.Errorf("%w: %s is deleted", ErrNotFound, kind)
	}
	var count int
	if err := queryer.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE id = ? AND revision = ?", table), id, revision).Scan(&count); err != nil {
		return fmt.Errorf("resolve %s revision: %w", kind, err)
	}
	if count != 1 {
		return fmt.Errorf("%w: %s revision", ErrNotFound, kind)
	}
	return nil
}

func (repository *Repository) getLatestDocument(ctx context.Context, table, id, kind string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var schemaVersion, revision int64
	var createdAt, updatedAt string
	var document []byte
	err := repository.conn.QueryRowContext(ctx, fmt.Sprintf(`SELECT schema_version, revision, created_at, updated_at, document_json FROM %s WHERE id = ? AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = ? AND entity_id = ?) ORDER BY revision DESC LIMIT 1`, table), id, table, id).Scan(&schemaVersion, &revision, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", kind, err)
	}
	if err := verifyEntityRow(document, id, schemaVersion, revision, createdAt, updatedAt); err != nil {
		return nil, fmt.Errorf("%w: %s row does not match document", ErrCorrupt, kind)
	}
	return document, nil
}

func (repository *Repository) getExactDocument(ctx context.Context, table, id string, revision uint64, kind string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var schemaVersion, storedRevision int64
	var createdAt, updatedAt string
	var document []byte
	err := repository.conn.QueryRowContext(ctx, fmt.Sprintf(`SELECT schema_version, revision, created_at, updated_at, document_json FROM %s WHERE id = ? AND revision = ?`, table), id, revision).Scan(&schemaVersion, &storedRevision, &createdAt, &updatedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s revision", ErrNotFound, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("get %s revision: %w", kind, err)
	}
	if err := verifyEntityRow(document, id, schemaVersion, storedRevision, createdAt, updatedAt); err != nil {
		return nil, fmt.Errorf("%w: %s row does not match document", ErrCorrupt, kind)
	}
	return document, nil
}

func (repository *Repository) listLatestDocuments(ctx context.Context, table, kind string) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, schema_version, revision, created_at, updated_at, document_json FROM %s AS candidate
		WHERE revision = (SELECT MAX(revision) FROM %s WHERE id = candidate.id)
		AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = ? AND entity_id = candidate.id)
		ORDER BY created_at, id
	`, table, table), table)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, err)
	}
	defer rows.Close()
	result := make([][]byte, 0)
	for rows.Next() {
		var id string
		var schemaVersion, revision int64
		var createdAt, updatedAt string
		var document []byte
		if err := rows.Scan(&id, &schemaVersion, &revision, &createdAt, &updatedAt, &document); err != nil {
			return nil, fmt.Errorf("scan %s: %w", kind, err)
		}
		if err := verifyEntityRow(document, id, schemaVersion, revision, createdAt, updatedAt); err != nil {
			return nil, fmt.Errorf("%w: %s row does not match document", ErrCorrupt, kind)
		}
		result = append(result, append([]byte(nil), document...))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", kind, err)
	}
	return result, nil
}

func verifyDocumentIdentity(document []byte, id string, revision uint64) error {
	var identity struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(document, &identity); err != nil {
		return err
	}
	if identity.ID != id || identity.Revision != revision {
		return errors.New("document identity does not match its row")
	}
	return nil
}

func verifyEntityRow(document []byte, id string, schemaVersion, revision int64, createdAt, updatedAt string) error {
	var meta domain.EntityMeta
	if err := json.Unmarshal(document, &meta); err != nil {
		return err
	}
	if meta.ID != id || int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
		formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt {
		return errors.New("document metadata does not match its row")
	}
	return nil
}

func decodeCanonical(document []byte, destination any, validate func() error) error {
	if err := json.Unmarshal(document, destination); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	canonical, err := marshalCanonical(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, document) {
		return errors.New("stored document is not canonical JSON")
	}
	return nil
}
