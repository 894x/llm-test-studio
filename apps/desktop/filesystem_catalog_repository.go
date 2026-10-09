package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/diagnostics"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

// filesystemCatalogRepository is the complete authored-catalog boundary. It
// deliberately has no SQLite fallback: adding a new catalog operation must be
// implemented against files explicitly or it will fail to compile.
type filesystemCatalogRepository struct {
	// lockPath is shared by every authored catalog rooted beside the same
	// executable. It serializes cross-file validation and the single-file
	// commit so concurrent repository instances preserve compare-and-swap semantics.
	lockPath string
	models   interface {
		List(context.Context) ([]domain.Model, error)
		Get(context.Context, string) (domain.Model, error)
		Create(context.Context, domain.Model) error
		Update(context.Context, uint64, domain.Model) error
		Delete(context.Context, string, uint64) error
	}
	channels interface {
		ListChannels(context.Context) ([]domain.Channel, error)
		ListMappings(context.Context) ([]domain.ChannelModel, error)
		GetChannel(context.Context, string) (domain.Channel, error)
		GetMapping(context.Context, string) (domain.ChannelModel, error)
		CreateChannel(context.Context, domain.Channel) error
		UpdateChannel(context.Context, uint64, domain.Channel) error
		DeleteChannel(context.Context, string, uint64) error
		CreateMapping(context.Context, domain.ChannelModel) error
		UpdateMapping(context.Context, uint64, domain.ChannelModel) error
		DeleteMapping(context.Context, string, uint64) error
	}
	cases interface {
		Entries(context.Context) ([]casecatalog.Entry, error)
		Find(context.Context, string) (casecatalog.Entry, error)
		SaveCase(context.Context, string, string, domain.TestCase) error
		Delete(context.Context, string, uint64) error
	}
	suites interface {
		Entries(context.Context) ([]suitecatalog.Entry, error)
		Find(context.Context, string) (suitecatalog.Entry, error)
		SaveSuite(context.Context, string, string, domain.Suite) error
		Delete(context.Context, string, uint64) error
	}
	plans interface {
		List(context.Context) ([]domain.Plan, error)
		ListDocuments(context.Context) ([]plancatalog.Document, error)
		Get(context.Context, string) (domain.Plan, error)
		GetDocument(context.Context, string) (plancatalog.Document, error)
		Create(context.Context, domain.Plan) error
		CreateDocument(context.Context, plancatalog.Document) error
		Update(context.Context, uint64, domain.Plan) error
		UpdateDocument(context.Context, uint64, plancatalog.Document) error
		Delete(context.Context, string, uint64) error
	}
}

var _ catalog.Repository = filesystemCatalogRepository{}

type filesystemCatalogMutationState struct {
	writeAttempted bool
}

type authoredCatalogLockContextKey struct{}

func (state *filesystemCatalogMutationState) write(action func() error) error {
	state.writeAttempted = true
	return action()
}

func (repository filesystemCatalogRepository) withMutationLock(
	ctx context.Context,
	action func(*filesystemCatalogMutationState) error,
	verify func(context.Context) (bool, error),
) error {
	if ctx == nil || !filepath.IsAbs(repository.lockPath) {
		return catalog.ErrInvalid
	}
	state := &filesystemCatalogMutationState{}
	var actionErr error
	verifiedCommitted := false
	runLocked := func() error {
		actionErr = action(state)
		if actionErr == nil || !state.writeAttempted || isDefinitiveCatalogMutationError(actionErr) || verify == nil {
			return actionErr
		}
		committed, verifyErr := verify(context.WithoutCancel(ctx))
		if verifyErr == nil && committed {
			verifiedCommitted = true
			return nil
		}
		if verifyErr != nil {
			return errors.Join(actionErr, verifyErr)
		}
		return actionErr
	}
	var err error
	if repository.authoredCatalogLockHeld(ctx) {
		err = runLocked()
	} else {
		err = fileconfig.WithExclusiveLock(ctx, repository.lockPath, runLocked)
	}
	if verifiedCommitted || actionErr == nil && state.writeAttempted {
		// The authored document is committed. A later lock-release failure must
		// not be reported as a failed save or trigger keyring compensation.
		return nil
	}
	return err
}

// WithCredentialMutation holds the authored-catalog lock across the queue
// journal, keyring operation, and Channel file commit. The derived context lets
// repository methods reuse the already-held lock without self-deadlocking.
func (repository filesystemCatalogRepository) WithCredentialMutation(
	ctx context.Context,
	action func(context.Context) error,
) error {
	if ctx == nil || action == nil || !filepath.IsAbs(repository.lockPath) {
		return catalog.ErrInvalid
	}
	var actionErr error
	actionStarted := false
	err := fileconfig.WithExclusiveLock(ctx, repository.lockPath, func() error {
		actionStarted = true
		lockedCtx := context.WithValue(ctx, authoredCatalogLockContextKey{}, filepath.Clean(repository.lockPath))
		actionErr = action(lockedCtx)
		return actionErr
	})
	if actionStarted && actionErr == nil {
		// The whole credential mutation completed. A later lock release error
		// cannot safely be reported as an uncommitted Channel write.
		return nil
	}
	return err
}

func (repository filesystemCatalogRepository) authoredCatalogLockHeld(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	lockPath, ok := ctx.Value(authoredCatalogLockContextKey{}).(string)
	return ok && lockPath == filepath.Clean(repository.lockPath)
}

func isDefinitiveCatalogMutationError(err error) bool {
	return errors.Is(err, catalog.ErrInvalid) || errors.Is(err, catalog.ErrNotFound) ||
		errors.Is(err, catalog.ErrConflict) || errors.Is(err, catalog.ErrCorrupt) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (repository filesystemCatalogRepository) ListModels(ctx context.Context) ([]domain.Model, error) {
	return repository.models.List(ctx)
}

func (repository filesystemCatalogRepository) GetModel(ctx context.Context, id string) (domain.Model, error) {
	value, err := repository.models.Get(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository filesystemCatalogRepository) CreateModel(ctx context.Context, model domain.Model) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.models.Create(ctx, model)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetModel(verifyCtx, model.ID)
		return catalogDesiredStateEqual(got, model, err)
	})
}

func (repository filesystemCatalogRepository) UpdateModel(ctx context.Context, expectedRevision uint64, model domain.Model) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.models.Update(ctx, expectedRevision, model)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetModel(verifyCtx, model.ID)
		return catalogDesiredStateEqual(got, model, err)
	})
}

func (repository filesystemCatalogRepository) DeleteModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteModelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetModel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) deleteModelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	mappings, err := repository.ListChannelModels(ctx)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.ModelID == id {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error { return mapFileCatalogError(repository.models.Delete(ctx, id, expectedRevision)) })
}

func (repository filesystemCatalogRepository) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	return repository.channels.ListChannels(ctx)
}

func (repository filesystemCatalogRepository) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	value, err := repository.channels.GetChannel(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository filesystemCatalogRepository) CreateChannel(ctx context.Context, channel domain.Channel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.channels.CreateChannel(ctx, channel)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannel(verifyCtx, channel.ID)
		return catalogDesiredStateEqual(got, channel, err)
	})
}

func (repository filesystemCatalogRepository) UpdateChannel(ctx context.Context, expectedRevision uint64, channel domain.Channel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error {
			return mapFileCatalogError(repository.channels.UpdateChannel(ctx, expectedRevision, channel))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannel(verifyCtx, channel.ID)
		return catalogDesiredStateEqual(got, channel, err)
	})
}

func (repository filesystemCatalogRepository) DeleteChannel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteChannelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetChannel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) deleteChannelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	mappings, err := repository.ListChannelModels(ctx)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.ChannelID == id {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error { return mapFileCatalogError(repository.channels.DeleteChannel(ctx, id, expectedRevision)) })
}

func (repository filesystemCatalogRepository) ListChannelModels(ctx context.Context) ([]domain.ChannelModel, error) {
	return repository.channels.ListMappings(ctx)
}

func (repository filesystemCatalogRepository) GetChannelModel(ctx context.Context, id string) (domain.ChannelModel, error) {
	value, err := repository.channels.GetMapping(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository filesystemCatalogRepository) CreateChannelModel(ctx context.Context, mapping domain.ChannelModel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if err := repository.validateMappingReferencesUnlocked(ctx, mapping); err != nil {
			return err
		}
		return state.write(func() error { return mapFileCatalogError(repository.channels.CreateMapping(ctx, mapping)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannelModel(verifyCtx, mapping.ID)
		return catalogDesiredStateEqual(got, mapping, err)
	})
}

func (repository filesystemCatalogRepository) UpdateChannelModel(ctx context.Context, expectedRevision uint64, mapping domain.ChannelModel) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if err := repository.validateMappingReferencesUnlocked(ctx, mapping); err != nil {
			return err
		}
		return state.write(func() error {
			return mapFileCatalogError(repository.channels.UpdateMapping(ctx, expectedRevision, mapping))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetChannelModel(verifyCtx, mapping.ID)
		return catalogDesiredStateEqual(got, mapping, err)
	})
}

func (repository filesystemCatalogRepository) DeleteChannelModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteChannelModelUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetChannelModel(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) deleteChannelModelUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	return state.write(func() error { return mapFileCatalogError(repository.channels.DeleteMapping(ctx, id, expectedRevision)) })
}

func containsCatalogID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (repository filesystemCatalogRepository) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	entries, err := repository.cases.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, len(entries))
	for index, entry := range entries {
		result[index] = entry.TestCase
	}
	return result, nil
}

func (repository filesystemCatalogRepository) GetTestCase(ctx context.Context, id string) (domain.TestCase, error) {
	entry, err := repository.cases.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.TestCase{}, catalog.ErrNotFound
	}
	return entry.TestCase, err
}

func (repository filesystemCatalogRepository) CreateTestCase(ctx context.Context, testCase domain.TestCase) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.createTestCaseUnlocked(ctx, state, testCase)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredCaseMatches(verifyCtx, testCase)
	})
}

func (repository filesystemCatalogRepository) createTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, testCase domain.TestCase) error {
	entries, err := repository.cases.Entries(ctx)
	if err != nil {
		return err
	}
	directory := filesystemCaseDirectory(testCase.Key)
	for _, entry := range entries {
		if entry.TestCase.ID == testCase.ID || entry.TestCase.Key == testCase.Key ||
			entry.Group == "custom" && entry.Directory == directory {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.cases.SaveCase(ctx, "custom", directory, testCase))
	})
}

func (repository filesystemCatalogRepository) UpdateTestCase(ctx context.Context, expectedRevision uint64, testCase domain.TestCase) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.updateTestCaseUnlocked(ctx, state, expectedRevision, testCase)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredCaseMatches(verifyCtx, testCase)
	})
}

func (repository filesystemCatalogRepository) updateTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, expectedRevision uint64, testCase domain.TestCase) error {
	entry, err := repository.cases.Find(ctx, testCase.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog.ErrNotFound
	}
	if err != nil {
		return err
	}
	if entry.TestCase.Revision != expectedRevision {
		return catalog.ErrConflict
	}
	if entry.TestCase.Key != testCase.Key {
		return catalog.ErrInvalid
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.cases.SaveCase(ctx, entry.Group, entry.Directory, testCase))
	})
}

func (repository filesystemCatalogRepository) DeleteTestCase(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteTestCaseUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetTestCase(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) deleteTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	entry, err := repository.cases.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog.ErrNotFound
	}
	if err != nil {
		return err
	}
	if entry.TestCase.Revision != expectedRevision {
		return catalog.ErrConflict
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.cases.Delete(ctx, id, expectedRevision))
	})
}

func (repository filesystemCatalogRepository) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	entries, err := repository.suites.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Suite, len(entries))
	for index, entry := range entries {
		result[index] = entry.Suite
	}
	return result, nil
}

func (repository filesystemCatalogRepository) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	entry, err := repository.suites.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Suite{}, catalog.ErrNotFound
	}
	return entry.Suite, err
}

func (repository filesystemCatalogRepository) CreateSuite(ctx context.Context, suite domain.Suite) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.createSuiteUnlocked(ctx, state, suite)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredSuiteMatches(verifyCtx, suite)
	})
}

func (repository filesystemCatalogRepository) createSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, suite domain.Suite) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	entries, err := repository.suites.Entries(ctx)
	if err != nil {
		return err
	}
	directory := filesystemSuiteDirectory(suite.Key)
	for _, entry := range entries {
		if entry.Suite.ID == suite.ID || entry.Suite.Protocol == suite.Protocol && entry.Suite.Key == suite.Key ||
			entry.Group == string(suite.Protocol) && entry.Directory == directory {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.suites.SaveSuite(ctx, string(suite.Protocol), directory, suite))
	})
}

func (repository filesystemCatalogRepository) UpdateSuite(ctx context.Context, expectedRevision uint64, suite domain.Suite) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.updateSuiteUnlocked(ctx, state, expectedRevision, suite)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredSuiteMatches(verifyCtx, suite)
	})
}

func (repository filesystemCatalogRepository) updateSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, expectedRevision uint64, suite domain.Suite) error {
	entry, err := repository.suites.Find(ctx, suite.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog.ErrNotFound
	}
	if err != nil {
		return err
	}
	if entry.Suite.Revision != expectedRevision {
		return catalog.ErrConflict
	}
	if entry.Suite.Key != suite.Key || entry.Suite.Protocol != suite.Protocol {
		return catalog.ErrInvalid
	}
	if err := suite.Validate(); err != nil {
		return err
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.suites.SaveSuite(ctx, entry.Group, entry.Directory, suite))
	})
}

func (repository filesystemCatalogRepository) DeleteSuite(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteSuiteUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetSuite(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) deleteSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	entry, err := repository.suites.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return catalog.ErrNotFound
	}
	if err != nil {
		return err
	}
	if entry.Suite.Revision != expectedRevision {
		return catalog.ErrConflict
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.suites.Delete(ctx, id, expectedRevision))
	})
}

func (repository filesystemCatalogRepository) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	return repository.plans.List(ctx)
}

func (repository filesystemCatalogRepository) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	value, err := repository.plans.Get(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository filesystemCatalogRepository) ListPlanDocuments(ctx context.Context) ([]plancatalog.Document, error) {
	values, err := repository.plans.ListDocuments(ctx)
	if err != nil {
		return nil, mapFileCatalogError(err)
	}
	return values, nil
}

func (repository filesystemCatalogRepository) GetPlanDocument(ctx context.Context, id string) (plancatalog.Document, error) {
	value, err := repository.plans.GetDocument(ctx, id)
	return value, mapFileCatalogError(err)
}

func (repository filesystemCatalogRepository) CreatePlan(ctx context.Context, plan domain.Plan) error {
	desired := plancatalog.Document{FileSchemaVersion: plancatalog.CurrentFileSchemaVersion, Plan: plan}
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.plans.CreateDocument(ctx, desired)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, plan.ID)
		return catalogDesiredStateEqual(got, desired, err)
	})
}

func (repository filesystemCatalogRepository) UpdatePlan(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	desired := plancatalog.Document{FileSchemaVersion: plancatalog.CurrentFileSchemaVersion, Plan: plan}
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error {
			return mapFileCatalogError(repository.plans.UpdateDocument(ctx, expectedRevision, desired))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, plan.ID)
		return catalogDesiredStateEqual(got, desired, err)
	})
}

func (repository filesystemCatalogRepository) CreatePlanDocument(ctx context.Context, document plancatalog.Document) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if document.Validate() != nil {
			return catalog.ErrInvalid
		}
		return state.write(func() error { return mapFileCatalogError(repository.plans.CreateDocument(ctx, document)) })
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, document.ID)
		return catalogDesiredStateEqual(got, document, err)
	})
}

func (repository filesystemCatalogRepository) UpdatePlanDocument(ctx context.Context, expectedRevision uint64, document plancatalog.Document) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		if document.Validate() != nil {
			return catalog.ErrInvalid
		}
		return state.write(func() error {
			return mapFileCatalogError(repository.plans.UpdateDocument(ctx, expectedRevision, document))
		})
	}, func(verifyCtx context.Context) (bool, error) {
		got, err := repository.GetPlanDocument(verifyCtx, document.ID)
		return catalogDesiredStateEqual(got, document, err)
	})
}

func (repository filesystemCatalogRepository) DeletePlan(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return state.write(func() error { return mapFileCatalogError(repository.plans.Delete(ctx, id, expectedRevision)) })
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetPlan(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository filesystemCatalogRepository) validateMappingReferencesUnlocked(ctx context.Context, mapping domain.ChannelModel) error {
	model, err := repository.GetModel(ctx, mapping.ModelID)
	if err != nil {
		return err
	}
	channel, err := repository.GetChannel(ctx, mapping.ChannelID)
	if err != nil {
		return err
	}
	if model.Protocol != channel.Protocol {
		return catalog.ErrInvalid
	}
	return nil
}

func (repository filesystemCatalogRepository) IsCredentialReferenced(ctx context.Context, credentialID string) (bool, error) {
	if ctx == nil || !domain.IsUUID(credentialID) {
		return false, catalog.ErrInvalid
	}
	return repository.isCredentialReferencedUnlocked(ctx, credentialID)
}

func (repository filesystemCatalogRepository) WithCredentialUnreferenced(
	ctx context.Context,
	credentialID string,
	action func() error,
) (bool, error) {
	if ctx == nil || !domain.IsUUID(credentialID) || action == nil || !filepath.IsAbs(repository.lockPath) {
		return false, catalog.ErrInvalid
	}
	executed := false
	actionWhileLocked := func() error {
		referenced, err := repository.isCredentialReferencedUnlocked(ctx, credentialID)
		if err != nil || referenced {
			return err
		}
		executed = true
		return action()
	}
	var err error
	if repository.authoredCatalogLockHeld(ctx) {
		err = actionWhileLocked()
	} else {
		err = fileconfig.WithExclusiveLock(ctx, repository.lockPath, actionWhileLocked)
	}
	return executed, err
}

func (repository filesystemCatalogRepository) isCredentialReferencedUnlocked(ctx context.Context, credentialID string) (bool, error) {
	channels, err := repository.ListChannels(ctx)
	if err != nil {
		return false, err
	}
	for _, channel := range channels {
		if channel.CredentialID == credentialID {
			return true, nil
		}
	}
	return false, nil
}

func (repository filesystemCatalogRepository) authoredCaseMatches(ctx context.Context, desired domain.TestCase) (bool, error) {
	desiredDigest, err := casecodec.MaterializedSHA256(desired)
	if err != nil {
		return false, err
	}
	entries, err := repository.cases.Entries(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		current := entry.TestCase
		if current.ID != desired.ID || current.Key != desired.Key {
			continue
		}
		currentDigest, err := casecodec.MaterializedSHA256(current)
		if err != nil {
			return false, err
		}
		return currentDigest == desiredDigest, nil
	}
	return false, nil
}

func (repository filesystemCatalogRepository) authoredSuiteMatches(ctx context.Context, desired domain.Suite) (bool, error) {
	entries, err := repository.suites.Entries(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		current := entry.Suite
		if current.Protocol != desired.Protocol || current.Key != desired.Key {
			continue
		}
		// Compare all authored fields so new Suite metadata cannot be omitted
		// from uncertain-write recovery. Filesystem revisions derive their own
		// identity metadata from the authored content.
		current.EntityMeta = domain.EntityMeta{}
		desired.EntityMeta = domain.EntityMeta{}
		return canonicalCatalogValuesEqual(current, desired), nil
	}
	return false, nil
}

func canonicalCatalogValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func catalogDesiredStateEqual(current, desired any, err error) (bool, error) {
	if errors.Is(err, catalog.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(current, desired), nil
}

func catalogRecordIsMissing(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if errors.Is(err, catalog.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func mapFileCatalogError(err error) error {
	if err == nil {
		return nil
	}
	var diagnostic catalog.SafeDiagnosticCause
	if errors.As(err, &diagnostic) {
		return err
	}
	switch {
	case errors.Is(err, casecatalog.ErrInvalid), errors.Is(err, suitecatalog.ErrInvalid),
		errors.Is(err, modelcatalog.ErrInvalid), errors.Is(err, channelcatalog.ErrInvalid), errors.Is(err, plancatalog.ErrInvalid):
		return catalog.ErrInvalid
	case errors.Is(err, casecatalog.ErrCollision), errors.Is(err, suitecatalog.ErrCollision):
		return catalog.ErrConflict
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, modelcatalog.ErrNotFound), errors.Is(err, channelcatalog.ErrNotFound), errors.Is(err, plancatalog.ErrNotFound):
		return catalog.ErrNotFound
	case errors.Is(err, modelcatalog.ErrConflict), errors.Is(err, channelcatalog.ErrConflict), errors.Is(err, plancatalog.ErrConflict):
		return catalog.ErrConflict
	case errors.Is(err, modelcatalog.ErrCorrupt), errors.Is(err, channelcatalog.ErrCorrupt), errors.Is(err, plancatalog.ErrCorrupt):
		return catalog.ErrCorrupt
	default:
		detail := strings.TrimSpace(diagnostics.RedactText(err.Error()))
		if detail == "" {
			detail = "filesystem catalog operation failed"
		}
		return filesystemCatalogDiagnosticError{cause: err, detail: detail}
	}
}

type filesystemCatalogDiagnosticError struct {
	cause  error
	detail string
}

func (err filesystemCatalogDiagnosticError) Error() string {
	return err.detail
}

func (err filesystemCatalogDiagnosticError) Unwrap() error {
	return err.cause
}

func (err filesystemCatalogDiagnosticError) SafeDiagnosticCause() string {
	return err.detail
}
