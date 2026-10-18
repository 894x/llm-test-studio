package workspace

import "context"

// CountAuthored uses metadata projections where available. Content is still
// fingerprinted so external edits and invalid overrides are noticed immediately.
func (repository Catalog) CountAuthored(ctx context.Context) (cases, suites, plans int, err error) {
	cases, err = countCatalog(ctx, repository.Cases, repository.Cases.Entries)
	if err != nil {
		return
	}
	suites, err = countCatalog(ctx, repository.Suites, repository.Suites.Entries)
	if err != nil {
		return
	}
	plans, err = countCatalog(ctx, repository.Plans, repository.Plans.ListDocuments)
	return
}

func countCatalog[T any](ctx context.Context, source any, list func(context.Context) ([]T, error)) (int, error) {
	if counter, ok := source.(interface {
		Count(context.Context) (int, error)
	}); ok {
		return counter.Count(ctx)
	}
	values, err := list(ctx)
	return len(values), err
}
