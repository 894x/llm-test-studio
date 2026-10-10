package modelcatalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

func TestModelCatalogRoundTripAndRevisionGuards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	service, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	model := validModel("40000000-0000-4000-8000-000000000001", time.Date(2026, time.September, 4, 2, 0, 0, 0, time.UTC))
	if err := service.Create(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("Get() = %#v, want %#v", got, model)
	}

	conflicting := model
	conflicting.Revision = 3
	conflicting.UpdatedAt = model.UpdatedAt.Add(2 * time.Minute)
	conflicting.Name = "must not replace current"
	if err := service.Update(context.Background(), 2, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update() stale current revision error = %v, want %v", err, ErrConflict)
	}
	if err := service.Delete(context.Background(), model.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() revision error = %v, want %v", err, ErrConflict)
	}
	if _, err := service.Get(context.Background(), "40000000-0000-4000-8000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() missing error = %v, want %v", err, ErrNotFound)
	}
	if err := service.Delete(context.Background(), "40000000-0000-4000-8000-000000000099", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() missing error = %v, want %v", err, ErrNotFound)
	}

	updated := model
	updated.Revision = 2
	updated.UpdatedAt = model.UpdatedAt.Add(time.Minute)
	updated.Name = "Updated model"
	updated.Capabilities = []string{"chat", "tools"}
	if err := service.Update(context.Background(), 1, updated); err != nil {
		t.Fatal(err)
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], updated) {
		t.Fatalf("List() = %#v, want updated model", listed)
	}
}

func TestModelCatalogRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "unknown field",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"name":`), []byte(`"unexpected": true, "name":`), 1)
			},
		},
		{name: "trailing document", mutate: func(raw []byte) []byte { return append(raw, []byte("{}\n")...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.json")
			service, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			model := validModel("40000000-0000-4000-8000-000000000001", time.Date(2026, time.September, 4, 2, 0, 0, 0, time.UTC))
			if err := service.Create(context.Background(), model); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutated := test.mutate(raw)
			if bytes.Equal(mutated, raw) {
				t.Fatal("fixture mutation did not change models.json")
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.List(context.Background()); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("List() error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestIndependentModelCatalogServicesSerializeCASUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 4, 0, 0, 0, time.UTC)
	model := validModel("40000000-0000-4000-8000-000000000001", now)
	if err := first.Create(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	left := model
	left.Revision = 2
	left.UpdatedAt = now.Add(time.Minute)
	left.Name = "left update"
	right := left
	right.Name = "right update"

	results := runModelMutationsContendingOnLock(t, path+".lock",
		func() error { return first.Update(context.Background(), 1, left) },
		func() error { return second.Update(context.Background(), 1, right) },
	)
	successes := 0
	conflicts := 0
	for _, result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("Update() error = %v, want success or %v", result, ErrConflict)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS results = %v, want one success and one conflict", results)
	}
	got, err := first.Get(context.Background(), model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.Name != left.Name && got.Name != right.Name {
		t.Fatalf("stored model = %#v, want one complete revision 2 update", got)
	}
}

func TestIndependentModelCatalogServicesDoNotLoseDifferentEntityUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 4, 0, 0, 0, time.UTC)
	left := validModel("40000000-0000-4000-8000-000000000001", now)
	right := validModel("40000000-0000-4000-8000-000000000002", now.Add(time.Second))
	if err := first.Create(context.Background(), left); err != nil {
		t.Fatal(err)
	}
	if err := first.Create(context.Background(), right); err != nil {
		t.Fatal(err)
	}
	left.Revision = 2
	left.UpdatedAt = now.Add(time.Minute)
	left.Name = "updated left"
	right.Revision = 2
	right.UpdatedAt = now.Add(time.Minute)
	right.Name = "updated right"

	results := runModelMutationsContendingOnLock(t, path+".lock",
		func() error { return first.Update(context.Background(), 1, left) },
		func() error { return second.Update(context.Background(), 1, right) },
	)
	for _, result := range results {
		if result != nil {
			t.Fatalf("Update() error = %v, want both different-entity updates to succeed", result)
		}
	}
	for _, want := range []domain.Model{left, right} {
		got, err := first.Get(context.Background(), want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Get(%q) = %#v, want %#v", want.ID, got, want)
		}
	}
}

func TestEveryModelCatalogMutationWaitsForTheCatalogLock(t *testing.T) {
	now := time.Date(2026, time.September, 4, 5, 0, 0, 0, time.UTC)
	newCatalog := func(t *testing.T) (*Service, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		service, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		return service, path
	}

	t.Run("create", func(t *testing.T) {
		service, path := newCatalog(t)
		model := validModel("40000000-0000-4000-8000-000000000001", now)
		assertModelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.Create(ctx, model)
		})
		if _, err := service.Get(context.Background(), model.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() after cancelled create error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("update", func(t *testing.T) {
		service, path := newCatalog(t)
		model := validModel("40000000-0000-4000-8000-000000000001", now)
		if err := service.Create(context.Background(), model); err != nil {
			t.Fatal(err)
		}
		updated := model
		updated.Revision = 2
		updated.UpdatedAt = now.Add(time.Minute)
		updated.Name = "updated model"
		assertModelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.Update(ctx, 1, updated)
		})
		got, err := service.Get(context.Background(), model.ID)
		if err != nil || !reflect.DeepEqual(got, model) {
			t.Fatalf("Get() after cancelled update = %#v, %v; want original", got, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		service, path := newCatalog(t)
		model := validModel("40000000-0000-4000-8000-000000000001", now)
		if err := service.Create(context.Background(), model); err != nil {
			t.Fatal(err)
		}
		assertModelMutationWaitsForLock(t, path, func(ctx context.Context) error {
			return service.Delete(ctx, model.ID, 1)
		})
		if _, err := service.Get(context.Background(), model.ID); err != nil {
			t.Fatalf("Get() after cancelled delete: %v", err)
		}
	})
}

func assertModelMutationWaitsForLock(t *testing.T, catalogPath string, mutation func(context.Context) error) {
	t.Helper()
	var mutationErr error
	if err := fileconfig.WithExclusiveLock(context.Background(), catalogPath+".lock", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()
		mutationErr = mutation(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(mutationErr, context.DeadlineExceeded) {
		t.Fatalf("mutation while catalog lock held error = %v, want %v", mutationErr, context.DeadlineExceeded)
	}
}

func runModelMutationsContendingOnLock(t *testing.T, lockPath string, operations ...func() error) []error {
	t.Helper()
	ready := make(chan struct{}, len(operations))
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var completedWhileLocked error
	completedEarly := false
	if err := fileconfig.WithExclusiveLock(context.Background(), lockPath, func() error {
		for _, operation := range operations {
			operation := operation
			go func() {
				ready <- struct{}{}
				<-start
				results <- operation()
			}()
		}
		for range operations {
			<-ready
		}
		close(start)
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case completedWhileLocked = <-results:
			completedEarly = true
		case <-timer.C:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	collected := make([]error, 0, len(operations))
	if completedEarly {
		collected = append(collected, completedWhileLocked)
	}
	for len(collected) < len(operations) {
		collected = append(collected, <-results)
	}
	if completedEarly {
		// The service must wait for the same catalog-wide lock used by this
		// helper. Reaching this branch catches a missing service lock even if
		// the two writes happen to serialize by scheduler luck.
		t.Fatal("model mutation completed while the catalog lock was held")
	}
	return collected
}

func validModel(id string, now time.Time) domain.Model {
	return domain.Model{
		EntityMeta:   domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:         "Model " + id,
		Protocols:    []domain.Protocol{domain.ProtocolOpenAIChat},
		Capabilities: []string{"chat"},
	}
}
