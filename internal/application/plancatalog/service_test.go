package plancatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

func TestPlanCatalogRoundTripsTargetedDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 8, 0, 0, 0, time.UTC)
	document := validTargetedDocument("60000000-0000-4000-8000-000000000001", now)
	if err := service.CreateDocument(context.Background(), document); err != nil {
		t.Fatal(err)
	}

	got, err := service.GetDocument(context.Background(), document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, document) {
		t.Fatalf("GetDocument() = %#v, want %#v", got, document)
	}
	plan, err := service.Get(context.Background(), document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, document.Plan) {
		t.Fatalf("Get() = %#v, want plan projection %#v", plan, document.Plan)
	}
	listed, err := service.ListDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], document) {
		t.Fatalf("ListDocuments() = %#v, want document", listed)
	}

	// Public reads must not expose any of the mutable data held by the catalog.
	got.ModelIDs[0] = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	got.SLA.Thresholds["e2e_p95_ms"] = 1
	got.TargetBindings[0].Model.Capabilities[0] = "mutated"
	got.TargetBindings[0].Mapping.UpstreamModelName = "mutated"
	again, err := service.GetDocument(context.Background(), document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, document) {
		t.Fatalf("GetDocument() after caller mutation = %#v, want original", again)
	}

	updated := cloneDocument(document)
	updated.Revision = 2
	updated.UpdatedAt = now.Add(time.Minute)
	updated.Name = "updated targeted plan"
	if err := service.UpdateDocument(context.Background(), 1, updated); err != nil {
		t.Fatal(err)
	}
	got, err = service.GetDocument(context.Background(), document.ID)
	if err != nil || !reflect.DeepEqual(got, updated) {
		t.Fatalf("GetDocument() after update = %#v, %v; want %#v", got, err, updated)
	}
}

func TestPlanCatalogRejectsInvalidTargetBindings(t *testing.T) {
	now := time.Date(2026, time.September, 4, 9, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*Document)
	}{
		{
			name: "missing cartesian binding",
			mutate: func(document *Document) {
				document.TargetBindings = document.TargetBindings[:len(document.TargetBindings)-1]
			},
		},
		{
			name: "duplicate binding",
			mutate: func(document *Document) {
				document.TargetBindings[1] = cloneTargetBinding(document.TargetBindings[0])
			},
		},
		{
			name: "inconsistent repeated model revision",
			mutate: func(document *Document) {
				document.TargetBindings[2].Model.Revision = 2
				document.TargetBindings[2].Model.UpdatedAt = now.Add(time.Second)
			},
		},
		{
			name: "mapping relation mismatch",
			mutate: func(document *Document) {
				document.TargetBindings[0].Mapping.ModelID = document.ModelIDs[1]
			},
		},
		{
			name: "model and channel protocol mismatch",
			mutate: func(document *Document) {
				document.TargetBindings[0].Model.Protocol = domain.ProtocolSeedance
			},
		},
		{
			name: "duplicate mapping identity",
			mutate: func(document *Document) {
				document.TargetBindings[1].Mapping.ID = document.TargetBindings[0].Mapping.ID
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := New(filepath.Join(t.TempDir(), "plans"))
			if err != nil {
				t.Fatal(err)
			}
			document := validTargetedDocument("60000000-0000-4000-8000-000000000001", now)
			test.mutate(&document)
			if err := service.CreateDocument(context.Background(), document); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateDocument() error = %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestPlanCatalogTargetlessDocumentRequiresEmptyBindings(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "plans"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
	document := Document{FileSchemaVersion: CurrentFileSchemaVersion, Plan: plan, TargetBindings: []TargetBinding{}}
	if err := service.CreateDocument(context.Background(), document); err != nil {
		t.Fatalf("CreateDocument() targetless: %v", err)
	}
	got, err := service.GetDocument(context.Background(), plan.ID)
	if err != nil || !reflect.DeepEqual(got, document) {
		t.Fatalf("GetDocument() = %#v, %v; want targetless wrapper", got, err)
	}

	invalid := Document{FileSchemaVersion: CurrentFileSchemaVersion, Plan: validTargetlessPlan("60000000-0000-4000-8000-000000000002", now)}
	invalid.TargetBindings = []TargetBinding{validTargetedDocument("60000000-0000-4000-8000-000000000003", now).TargetBindings[0]}
	if err := service.CreateDocument(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDocument() targetless with binding error = %v, want %v", err, ErrInvalid)
	}

	targeted := validTargetedDocument("60000000-0000-4000-8000-000000000004", now).Plan
	if err := service.Create(context.Background(), targeted); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() targeted plan error = %v, want %v", err, ErrInvalid)
	}
}

func TestPlanCatalogRejectsLegacyRawPlanAndInvalidWrapper(t *testing.T) {
	now := time.Date(2026, time.September, 4, 11, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		raw  func(domain.Plan) []byte
	}{
		{
			name: "legacy raw plan",
			raw: func(plan domain.Plan) []byte {
				raw, err := jsonMarshalForTest(plan)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			},
		},
		{
			name: "unsupported file schema",
			raw: func(plan domain.Plan) []byte {
				document := Document{FileSchemaVersion: CurrentFileSchemaVersion + 1, Plan: plan, TargetBindings: []TargetBinding{}}
				raw, err := jsonMarshalForTest(document)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "plans")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
			if err := os.WriteFile(filepath.Join(root, plan.ID+".json"), test.raw(plan), 0o600); err != nil {
				t.Fatal(err)
			}
			service, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.GetDocument(context.Background(), plan.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("GetDocument() error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestPlanCatalogConcurrentDocumentCASHasOneWinner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	document := validTargetedDocument("60000000-0000-4000-8000-000000000001", now)
	if err := first.CreateDocument(context.Background(), document); err != nil {
		t.Fatal(err)
	}
	left := cloneDocument(document)
	left.Revision = 2
	left.UpdatedAt = now.Add(time.Minute)
	left.Name = "left"
	right := cloneDocument(left)
	right.Name = "right"

	start := make(chan struct{})
	errorsByUpdate := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	update := func(service *Service, value Document) {
		ready.Done()
		<-start
		errorsByUpdate <- service.UpdateDocument(context.Background(), 1, value)
	}
	go update(first, left)
	go update(second, right)
	ready.Wait()
	close(start)
	firstErr, secondErr := <-errorsByUpdate, <-errorsByUpdate
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("concurrent UpdateDocument() errors = %v, %v; want one winner", firstErr, secondErr)
	}
	loser := firstErr
	if loser == nil {
		loser = secondErr
	}
	if !errors.Is(loser, ErrConflict) {
		t.Fatalf("losing UpdateDocument() error = %v, want %v", loser, ErrConflict)
	}
	got, err := first.GetDocument(context.Background(), document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.Name != "left" && got.Name != "right" {
		t.Fatalf("stored document = %#v, want one revision-2 update", got)
	}
}

func TestPlanCatalogRoundTripsTargetlessPlan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC))
	if err := service.Create(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, plan) {
		t.Fatalf("Get() = %#v, want targetless plan %#v", got, plan)
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], plan) {
		t.Fatalf("List() = %#v, want targetless plan", listed)
	}

	updated := plan
	updated.Revision = 2
	updated.UpdatedAt = plan.UpdatedAt.Add(time.Minute)
	updated.Name = "Updated runtime target plan"
	if err := service.Update(context.Background(), 1, updated); err != nil {
		t.Fatal(err)
	}
	got, err = service.Get(context.Background(), plan.ID)
	if err != nil || !reflect.DeepEqual(got, updated) {
		t.Fatalf("Get() after update = %#v, %v", got, err)
	}
}

func TestPlanCatalogRevisionAndMissingGuards(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "plans"))
	if err != nil {
		t.Fatal(err)
	}
	plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC))
	if err := service.Create(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	conflicting := plan
	conflicting.Revision = 3
	conflicting.UpdatedAt = plan.UpdatedAt.Add(2 * time.Minute)
	if err := service.Update(context.Background(), 2, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update() conflict error = %v, want %v", err, ErrConflict)
	}
	if err := service.Delete(context.Background(), plan.ID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() conflict error = %v, want %v", err, ErrConflict)
	}
	if _, err := service.GetRevision(context.Background(), plan.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRevision() missing revision error = %v, want %v", err, ErrNotFound)
	}
	missingID := "60000000-0000-4000-8000-000000000099"
	if _, err := service.Get(context.Background(), missingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() missing error = %v, want %v", err, ErrNotFound)
	}
	if err := service.Delete(context.Background(), missingID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() missing error = %v, want %v", err, ErrNotFound)
	}
}

func TestPlanCatalogRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
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
			root := filepath.Join(t.TempDir(), "plans")
			service, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC))
			if err := service.Create(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, plan.ID+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutated := test.mutate(raw)
			if bytes.Equal(mutated, raw) {
				t.Fatal("fixture mutation did not change plan file")
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Get(context.Background(), plan.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Get() error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestPlanCatalogIgnoresOnlyAtomicWriteTemporaryFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 4, 6, 0, 0, 0, time.UTC)
	first := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
	if err := service.Create(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(root, ".config-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.WriteString("incomplete atomic payload"); err != nil {
		_ = temporary.Close()
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}

	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() with orphan atomic temp: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("List() = %#v, want first plan only", listed)
	}
	second := validTargetlessPlan("60000000-0000-4000-8000-000000000002", now.Add(time.Second))
	if err := service.Create(context.Background(), second); err != nil {
		t.Fatalf("Create() with orphan atomic temp: %v", err)
	}
	first.Revision = 2
	first.UpdatedAt = now.Add(time.Minute)
	first.Name = "updated with orphan temp present"
	if err := service.Update(context.Background(), 1, first); err != nil {
		t.Fatalf("Update() with orphan atomic temp: %v", err)
	}
}

func TestPlanCatalogRejectsOtherHiddenOrNonRegularEntries(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(string) error
	}{
		{
			name: ".hidden",
			create: func(path string) error {
				return os.WriteFile(path, []byte("unexpected"), 0o600)
			},
		},
		{
			name: ".config-not-decimal.tmp",
			create: func(path string) error {
				return os.WriteFile(path, []byte("unexpected"), 0o600)
			},
		},
		{
			name: ".config-123.tmp",
			create: func(path string) error {
				return os.Mkdir(path, 0o700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "plans")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := test.create(filepath.Join(root, test.name)); err != nil {
				t.Fatal(err)
			}
			service, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.List(context.Background()); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("List() error = %v, want %v", err, ErrCorrupt)
			}
		})
	}
}

func TestEveryPlanCatalogMutationWaitsForTheCatalogLock(t *testing.T) {
	now := time.Date(2026, time.September, 4, 7, 0, 0, 0, time.UTC)
	newCatalog := func(t *testing.T) (*Service, string) {
		t.Helper()
		root := filepath.Join(t.TempDir(), "plans")
		service, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		return service, root
	}

	t.Run("create", func(t *testing.T) {
		service, root := newCatalog(t)
		plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
		assertPlanMutationWaitsForLock(t, root, func(ctx context.Context) error {
			return service.Create(ctx, plan)
		})
		if _, err := service.Get(context.Background(), plan.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() after cancelled create error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("update", func(t *testing.T) {
		service, root := newCatalog(t)
		plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
		if err := service.Create(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		updated := plan
		updated.Revision = 2
		updated.UpdatedAt = now.Add(time.Minute)
		updated.Name = "updated plan"
		assertPlanMutationWaitsForLock(t, root, func(ctx context.Context) error {
			return service.Update(ctx, 1, updated)
		})
		got, err := service.Get(context.Background(), plan.ID)
		if err != nil || !reflect.DeepEqual(got, plan) {
			t.Fatalf("Get() after cancelled update = %#v, %v; want original", got, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		service, root := newCatalog(t)
		plan := validTargetlessPlan("60000000-0000-4000-8000-000000000001", now)
		if err := service.Create(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		assertPlanMutationWaitsForLock(t, root, func(ctx context.Context) error {
			return service.Delete(ctx, plan.ID, 1)
		})
		if _, err := service.Get(context.Background(), plan.ID); err != nil {
			t.Fatalf("Get() after cancelled delete: %v", err)
		}
	})
}

func assertPlanMutationWaitsForLock(t *testing.T, root string, mutation func(context.Context) error) {
	t.Helper()
	var mutationErr error
	if err := fileconfig.WithExclusiveLock(context.Background(), root+".lock", func() error {
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

func validTargetlessPlan(id string, now time.Time) domain.Plan {
	return domain.Plan{
		EntityMeta: domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "Runtime target plan",
		ModelIDs:   []string{},
		ChannelIDs: []string{},
		Cases: []domain.CaseRevisionRef{{
			CaseID:   "70000000-0000-4000-8000-000000000001",
			Revision: 1,
		}},
		Load: domain.LoadProfile{
			Mode:             domain.LoadSingle,
			Concurrency:      1,
			RequestCount:     1,
			RequestTimeoutMS: 30_000,
		},
		SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 3_000}},
	}
}

func validTargetedDocument(id string, now time.Time) Document {
	modelIDs := []string{
		"61000000-0000-4000-8000-000000000001",
		"61000000-0000-4000-8000-000000000002",
	}
	channelIDs := []string{
		"62000000-0000-4000-8000-000000000001",
		"62000000-0000-4000-8000-000000000002",
	}
	models := []domain.Model{
		{
			EntityMeta:   testEntityMeta(modelIDs[0], 3, now),
			Name:         "model one",
			Protocol:     domain.ProtocolOpenAIChat,
			Capabilities: []string{"chat", "stream"},
		},
		{
			EntityMeta:   testEntityMeta(modelIDs[1], 5, now),
			Name:         "model two",
			Protocol:     domain.ProtocolOpenAIChat,
			Capabilities: []string{"chat"},
		},
	}
	channels := []domain.Channel{
		{
			EntityMeta:   testEntityMeta(channelIDs[0], 7, now),
			Name:         "channel one",
			BaseURL:      "https://one.example.test/v1",
			Protocol:     domain.ProtocolOpenAIChat,
			Enabled:      true,
			CredentialID: "63000000-0000-4000-8000-000000000001",
		},
		{
			EntityMeta:   testEntityMeta(channelIDs[1], 11, now),
			Name:         "channel two",
			BaseURL:      "https://two.example.test/v1",
			Protocol:     domain.ProtocolOpenAIChat,
			Enabled:      true,
			CredentialID: "63000000-0000-4000-8000-000000000002",
		},
	}
	plan := validTargetlessPlan(id, now)
	plan.Name = "targeted plan"
	plan.ModelIDs = append([]string{}, modelIDs...)
	plan.ChannelIDs = append([]string{}, channelIDs...)
	bindings := make([]TargetBinding, 0, len(modelIDs)*len(channelIDs))
	mappingIndex := 0
	for channelIndex, channel := range channels {
		for modelIndex, model := range models {
			mappingIndex++
			bindings = append(bindings, TargetBinding{
				Model:   model,
				Channel: channel,
				Mapping: domain.ChannelModel{
					EntityMeta:        testEntityMeta("64000000-0000-4000-8000-"+formatUUIDTail(mappingIndex), uint64(13+mappingIndex), now),
					ChannelID:         channelIDs[channelIndex],
					ModelID:           modelIDs[modelIndex],
					UpstreamModelName: "upstream-model",
				},
			})
		}
	}
	return Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings:    bindings,
	}
}

func testEntityMeta(id string, revision uint64, now time.Time) domain.EntityMeta {
	return domain.EntityMeta{
		ID:            id,
		SchemaVersion: domain.CurrentEntitySchemaVersion,
		Revision:      revision,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func formatUUIDTail(value int) string {
	return "00000000000" + string(rune('0'+value))
}

func jsonMarshalForTest(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
