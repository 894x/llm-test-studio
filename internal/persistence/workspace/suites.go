package workspace

import (
	"context"
	"errors"
	"io/fs"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository Catalog) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	entries, err := repository.Suites.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Suite, len(entries))
	for index, entry := range entries {
		result[index] = entry.Suite
	}
	return result, nil
}

func (repository Catalog) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	entry, err := repository.Suites.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Suite{}, catalog.ErrNotFound
	}
	return entry.Suite, err
}

func (repository Catalog) CreateSuite(ctx context.Context, suite domain.Suite) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.createSuiteUnlocked(ctx, state, suite)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredSuiteMatches(verifyCtx, suite)
	})
}

func (repository Catalog) createSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, suite domain.Suite) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	entries, err := repository.Suites.Entries(ctx)
	if err != nil {
		return err
	}
	directory := SuiteDirectory(suite.Key)
	for _, entry := range entries {
		if entry.Suite.ID == suite.ID || entry.Suite.Protocol == suite.Protocol && entry.Suite.Key == suite.Key ||
			entry.Group == string(suite.Protocol) && entry.Directory == directory {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.Suites.SaveSuite(ctx, string(suite.Protocol), directory, suite))
	})
}

func (repository Catalog) UpdateSuite(ctx context.Context, expectedRevision uint64, suite domain.Suite) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.updateSuiteUnlocked(ctx, state, expectedRevision, suite)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredSuiteMatches(verifyCtx, suite)
	})
}

func (repository Catalog) updateSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, expectedRevision uint64, suite domain.Suite) error {
	entry, err := repository.Suites.Find(ctx, suite.ID)
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
		return mapFileCatalogError(repository.Suites.SaveSuite(ctx, entry.Group, entry.Directory, suite))
	})
}

func (repository Catalog) DeleteSuite(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteSuiteUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetSuite(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository Catalog) deleteSuiteUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	entry, err := repository.Suites.Find(ctx, id)
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
		return mapFileCatalogError(repository.Suites.Delete(ctx, id, expectedRevision))
	})
}

func (repository Catalog) authoredSuiteMatches(ctx context.Context, desired domain.Suite) (bool, error) {
	entries, err := repository.Suites.Entries(ctx)
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
