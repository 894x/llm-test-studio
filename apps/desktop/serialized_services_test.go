package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

type sharedConnectionProbe struct {
	active  atomic.Int32
	overlap atomic.Bool
}

func (probe *sharedConnectionProbe) call() func() {
	if probe.active.Add(1) > 1 {
		probe.overlap.Store(true)
	}
	time.Sleep(20 * time.Millisecond)
	return func() { probe.active.Add(-1) }
}

type probedWorkspaceService struct{ probe *sharedConnectionProbe }

func (service probedWorkspaceService) Snapshot(context.Context) (workspace.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return workspace.Snapshot{}, nil
}

type probedCatalogService struct{ probe *sharedConnectionProbe }

func (service probedCatalogService) Snapshot(context.Context) (catalog.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return catalog.Snapshot{}, nil
}

type probedReportingService struct{ probe *sharedConnectionProbe }

func (service probedReportingService) Snapshot(context.Context) (reporting.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return reporting.Snapshot{}, nil
}

type probedQuickPerformanceArchive struct{ probe *sharedConnectionProbe }

func (archive probedQuickPerformanceArchive) SaveQuickPerformanceReport(context.Context, quicktest.PerformanceReport) error {
	release := archive.probe.call()
	defer release()
	return nil
}

func TestProductionServiceGateSerializesCatalogSpanningWorkflows(t *testing.T) {
	probe := &sharedConnectionProbe{}
	gate := &productionServiceGate{}
	workspaceQuery := serializedWorkspaceQuery{gate: gate, query: probedWorkspaceService{probe: probe}}
	catalogQuery := serializedCatalogService{gate: gate, query: probedCatalogService{probe: probe}}
	quickArchive := serializedQuickPerformanceArchive{gate: gate, archive: probedQuickPerformanceArchive{probe: probe}}

	start := make(chan struct{})
	errors := make(chan error, 3)
	for _, call := range []func() error{
		func() error { _, err := workspaceQuery.Snapshot(context.Background()); return err },
		func() error { _, err := catalogQuery.Snapshot(context.Background()); return err },
		func() error {
			return quickArchive.SaveQuickPerformanceReport(context.Background(), quicktest.PerformanceReport{})
		},
	} {
		go func(call func() error) {
			<-start
			errors <- call()
		}(call)
	}
	close(start)
	for range 3 {
		if err := <-errors; err != nil {
			t.Fatalf("serialized service call: %v", err)
		}
	}
	if probe.overlap.Load() {
		t.Fatal("catalog-spanning workflows overlapped")
	}
}

func TestReportListDoesNotWaitForTheAuthoredCatalogGate(t *testing.T) {
	gate := &productionServiceGate{}
	release := gate.enter()
	defer release()
	query := serializedReportingQuery{gate: gate, query: probedReportingService{probe: &sharedConnectionProbe{}}}
	done := make(chan error, 1)
	go func() {
		_, err := query.Snapshot(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("report list waited for an unrelated catalog workflow")
	}
}

type staticCatalogQuery struct {
	snapshot catalog.Snapshot
}

func (query staticCatalogQuery) Snapshot(context.Context) (catalog.Snapshot, error) {
	return query.snapshot, nil
}

func TestSerializedCatalogServiceUpdatesAndDeletesReferencePlans(t *testing.T) {
	commands := &recordingCatalogCommands{}
	service := serializedCatalogService{gate: &productionServiceGate{}, commands: commands}
	if _, err := service.UpdatePlan(context.Background(), catalog.UpdatePlanCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeletePlan(context.Background(), catalog.DeleteCommand{}); err != nil {
		t.Fatal(err)
	}
}
