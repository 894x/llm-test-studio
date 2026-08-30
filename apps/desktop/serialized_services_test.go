package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/workspace"
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

func TestProductionServiceGateSerializesSharedConnectionCalls(t *testing.T) {
	probe := &sharedConnectionProbe{}
	gate := &productionServiceGate{}
	workspaceQuery := serializedWorkspaceQuery{gate: gate, query: probedWorkspaceService{probe: probe}}
	catalogQuery := serializedCatalogService{gate: gate, query: probedCatalogService{probe: probe}}
	reportingQuery := serializedReportingQuery{gate: gate, query: probedReportingService{probe: probe}}

	start := make(chan struct{})
	errors := make(chan error, 3)
	for _, call := range []func() error{
		func() error { _, err := workspaceQuery.Snapshot(context.Background()); return err },
		func() error { _, err := catalogQuery.Snapshot(context.Background()); return err },
		func() error { _, err := reportingQuery.Snapshot(context.Background()); return err },
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
		t.Fatal("production services overlapped on their shared connection")
	}
}
