package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/domain"
)

// ResolvePlanTarget returns the immutable model, channel, and mapping revisions
// pinned by a single-target plan. Multi-target plans require an explicit target
// selection at the Application boundary and are never resolved implicitly.
func (repository *Repository) ResolvePlanTarget(ctx context.Context, plan domain.Plan) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	if len(plan.ModelIDs) != 1 || len(plan.ChannelIDs) != 1 {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, ErrAmbiguousPlanTarget
	}
	return repository.ResolvePlanTargetSelection(ctx, plan, plan.ModelIDs[0], plan.ChannelIDs[0])
}

// ResolvePlanTargetSelection resolves one explicit pair from a multi-target
// plan. Comparison orchestration calls this once per selected channel.
func (repository *Repository) ResolvePlanTargetSelection(ctx context.Context, plan domain.Plan, modelID, channelID string) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	if ctx == nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, errors.New("resolve plan target: context is required")
	}
	if err := ctx.Err(); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	if err := plan.Validate(); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("resolve plan target: invalid plan: %w", err)
	}
	if len(plan.ModelIDs) > 0 && (!containsString(plan.ModelIDs, modelID) || !containsString(plan.ChannelIDs, channelID)) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("%w: selected target is outside the plan", ErrNotFound)
	}

	planDocument, err := exactDocument(ctx, repository.conn, "test_plans", plan.ID, plan.Revision, "plan")
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	var persistedPlan domain.Plan
	if err := decodeCanonical(planDocument, &persistedPlan, func() error { return persistedPlan.Validate() }); err != nil || !reflect.DeepEqual(persistedPlan, plan) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("%w: plan target input does not match persisted revision", ErrCorrupt)
	}
	if len(plan.ModelIDs) == 0 {
		return repository.resolveCurrentTargetSelection(ctx, modelID, channelID)
	}

	var modelRevision, channelRevision, mappingRevision uint64
	var mappingID string
	if err := repository.conn.QueryRowContext(ctx, `
		SELECT model_revision FROM plan_models
		WHERE plan_id = ? AND plan_revision = ? AND model_id = ?
	`, plan.ID, plan.Revision, modelID).Scan(&modelRevision); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, relationStorageError(ctx, "plan model target", err)
	}
	if err := repository.conn.QueryRowContext(ctx, `
		SELECT channel_revision FROM plan_channels
		WHERE plan_id = ? AND plan_revision = ? AND channel_id = ?
	`, plan.ID, plan.Revision, channelID).Scan(&channelRevision); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, relationStorageError(ctx, "plan channel target", err)
	}
	if err := repository.conn.QueryRowContext(ctx, `
		SELECT mapping_id, mapping_revision FROM plan_channel_models
		WHERE plan_id = ? AND plan_revision = ? AND channel_id = ? AND model_id = ?
	`, plan.ID, plan.Revision, channelID, modelID).Scan(&mappingID, &mappingRevision); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, relationStorageError(ctx, "plan channel model target", err)
	}

	model, err := resolveExactModel(ctx, repository.conn, modelID, modelRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channel, err := resolveExactChannel(ctx, repository.conn, channelID, channelRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	mapping, err := resolveExactChannelModel(ctx, repository.conn, mappingID, mappingRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	if mapping.ModelID != model.ID || mapping.ChannelID != channel.ID || model.Protocol != channel.Protocol || !channel.Enabled {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("%w: resolved plan target is inconsistent or disabled", ErrCorrupt)
	}
	return model, channel, mapping, nil
}

func (repository *Repository) resolveCurrentTargetSelection(ctx context.Context, modelID, channelID string) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	tx, err := repository.conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("begin runtime target read: %w", err)
	}
	defer tx.Rollback()
	modelRevision, err := latestRevision(ctx, tx, "models", modelID, "model")
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channelRevision, err := latestRevision(ctx, tx, "channels", channelID, "channel")
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	model, err := resolveExactModel(ctx, tx, modelID, modelRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	channel, err := resolveExactChannel(ctx, tx, channelID, channelRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	var mappingID string
	var mappingRevision uint64
	err = tx.QueryRowContext(ctx, `
		SELECT candidate.id, candidate.revision
		FROM channel_models AS candidate
		WHERE candidate.channel_id = ? AND candidate.model_id = ?
		  AND candidate.revision = (SELECT MAX(revision) FROM channel_models WHERE id = candidate.id)
		  AND NOT EXISTS (SELECT 1 FROM catalog_tombstones WHERE entity_table = 'channel_models' AND entity_id = candidate.id)
		ORDER BY candidate.created_at, candidate.id LIMIT 1
	`, channelID, modelID).Scan(&mappingID, &mappingRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("%w: selected runtime target has no model mapping", ErrNotFound)
	}
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("read runtime target mapping: %w", err)
	}
	mapping, err := resolveExactChannelModel(ctx, tx, mappingID, mappingRevision)
	if err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, err
	}
	if model.Protocol != channel.Protocol || !channel.Enabled || mapping.ModelID != model.ID || mapping.ChannelID != channel.ID {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("%w: selected runtime target is incompatible or disabled", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return domain.Model{}, domain.Channel{}, domain.ChannelModel{}, fmt.Errorf("commit runtime target read: %w", err)
	}
	return model, channel, mapping, nil
}

func resolveExactModel(ctx context.Context, queryer rowQueryer, id string, revision uint64) (domain.Model, error) {
	document, err := exactDocument(ctx, queryer, "models", id, revision, "model")
	if err != nil {
		return domain.Model{}, err
	}
	var value domain.Model
	if err := decodeCanonical(document, &value, func() error { return value.Validate() }); err != nil {
		return domain.Model{}, fmt.Errorf("%w: model target document", ErrCorrupt)
	}
	return value, nil
}

func resolveExactChannel(ctx context.Context, queryer rowQueryer, id string, revision uint64) (domain.Channel, error) {
	document, err := exactDocument(ctx, queryer, "channels", id, revision, "channel")
	if err != nil {
		return domain.Channel{}, err
	}
	var value domain.Channel
	if err := decodeCanonical(document, &value, func() error { return value.Validate() }); err != nil {
		return domain.Channel{}, fmt.Errorf("%w: channel target document", ErrCorrupt)
	}
	return value, nil
}

func resolveExactChannelModel(ctx context.Context, queryer rowQueryer, id string, revision uint64) (domain.ChannelModel, error) {
	document, err := exactDocument(ctx, queryer, "channel_models", id, revision, "channel model")
	if err != nil {
		return domain.ChannelModel{}, err
	}
	var value domain.ChannelModel
	if err := decodeCanonical(document, &value, func() error { return value.Validate() }); err != nil {
		return domain.ChannelModel{}, fmt.Errorf("%w: channel model target document", ErrCorrupt)
	}
	return value, nil
}
