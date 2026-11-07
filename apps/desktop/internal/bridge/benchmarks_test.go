package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/benchmarks"
	"github.com/894x/llm-test-studio/internal/benchmarkruntime"
)

type benchmarkRuntimeStub struct{}

func (benchmarkRuntimeStub) Ready() bool                   { return false }
func (benchmarkRuntimeStub) Prepare(context.Context) error { return nil }
func (benchmarkRuntimeStub) Execute(context.Context, benchmarks.WorkerRequest, func(benchmarks.Event) error) error {
	return benchmarks.ErrRuntime
}

func TestBenchmarkBindingsEnforceLifecycleAndSafeErrors(t *testing.T) {
	service, err := benchmarks.New(t.Context(), benchmarks.Dependencies{Directory: t.TempDir(), Runtime: benchmarkRuntimeStub{},
		CatalogJSON: benchmarkruntime.CatalogJSON(), Resolve: benchmarks.DirectConnection})
	if err != nil {
		t.Fatal(err)
	}
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{Benchmarks: service, Close: service.Close}, nil
	})
	if _, err := app.GetBenchmarkCatalog(); err == nil {
		t.Fatal("benchmark binding available before startup")
	}
	app.onStartup(t.Context())
	catalog, err := app.GetBenchmarkCatalog()
	if err != nil || len(catalog.Datasets) < 24 || catalog.Runtime.State != "missing" {
		t.Fatalf("bad catalog: %+v, %v", catalog, err)
	}
	inventory, err := app.GetBenchmarkDatasets()
	if err != nil || len(inventory.Datasets) != len(catalog.Datasets) || inventory.Datasets[0].Download.State != "missing" {
		t.Fatalf("dataset inventory unavailable: %+v, %v", inventory, err)
	}
	if _, err := app.ManageBenchmarkDataset("arc_easy", "download"); err == nil {
		t.Fatal("download admitted without a prepared runtime")
	}
	if _, err := app.StartBenchmark(benchmarks.StartCommand{}); err == nil {
		t.Fatal("invalid benchmark accepted")
	}
	for _, internal := range []error{benchmarks.ErrInvalid, benchmarks.ErrUnavailable, benchmarks.ErrRuntime, benchmarks.ErrBusy, benchmarks.ErrNotFound, benchmarks.ErrFormat} {
		public := app.safeBindingError(internal)
		var safe DesktopBindingError
		if !errors.As(public, &safe) || safe.Code != internal.Error() || safe.Error() != safe.Code {
			t.Fatalf("unsafe benchmark error: %v", public)
		}
	}
	if err := app.shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetBenchmarkCatalog(); err == nil {
		t.Fatal("benchmark binding available after shutdown")
	}
}
