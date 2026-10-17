package workspace

import (
	"context"
	"errors"
	"io/fs"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (repository Catalog) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	entries, err := repository.Cases.Entries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.TestCase, len(entries))
	for index, entry := range entries {
		result[index] = entry.TestCase
	}
	return result, nil
}

func (repository Catalog) GetTestCase(ctx context.Context, id string) (domain.TestCase, error) {
	entry, err := repository.Cases.Find(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.TestCase{}, catalog.ErrNotFound
	}
	return entry.TestCase, err
}

func (repository Catalog) CreateTestCase(ctx context.Context, testCase domain.TestCase) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.createTestCaseUnlocked(ctx, state, testCase)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredCaseMatches(verifyCtx, testCase)
	})
}

func (repository Catalog) createTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, testCase domain.TestCase) error {
	entries, err := repository.Cases.Entries(ctx)
	if err != nil {
		return err
	}
	directory := CaseDirectory(testCase.Key)
	for _, entry := range entries {
		if entry.TestCase.ID == testCase.ID || entry.TestCase.Key == testCase.Key ||
			entry.Group == "custom" && entry.Directory == directory {
			return catalog.ErrConflict
		}
	}
	return state.write(func() error {
		return mapFileCatalogError(repository.Cases.SaveCase(ctx, "custom", directory, testCase))
	})
}

func (repository Catalog) UpdateTestCase(ctx context.Context, expectedRevision uint64, testCase domain.TestCase) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.updateTestCaseUnlocked(ctx, state, expectedRevision, testCase)
	}, func(verifyCtx context.Context) (bool, error) {
		return repository.authoredCaseMatches(verifyCtx, testCase)
	})
}

func (repository Catalog) updateTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, expectedRevision uint64, testCase domain.TestCase) error {
	entry, err := repository.Cases.Find(ctx, testCase.ID)
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
		return mapFileCatalogError(repository.Cases.SaveCase(ctx, entry.Group, entry.Directory, testCase))
	})
}

func (repository Catalog) DeleteTestCase(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.withMutationLock(ctx, func(state *filesystemCatalogMutationState) error {
		return repository.deleteTestCaseUnlocked(ctx, state, id, expectedRevision)
	}, func(verifyCtx context.Context) (bool, error) {
		_, err := repository.GetTestCase(verifyCtx, id)
		return catalogRecordIsMissing(err)
	})
}

func (repository Catalog) deleteTestCaseUnlocked(ctx context.Context, state *filesystemCatalogMutationState, id string, expectedRevision uint64) error {
	entry, err := repository.Cases.Find(ctx, id)
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
		return mapFileCatalogError(repository.Cases.Delete(ctx, id, expectedRevision))
	})
}

func (repository Catalog) authoredCaseMatches(ctx context.Context, desired domain.TestCase) (bool, error) {
	desiredDigest, err := casecodec.MaterializedSHA256(desired)
	if err != nil {
		return false, err
	}
	entries, err := repository.Cases.Entries(ctx)
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
