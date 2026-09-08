package main

import (
	"context"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionPlanSaveOnlyStoresReferencesWithoutMappings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	options := defaultProductionOptions()
	options.credentialStore = credentials.NewMemoryStore()
	options.userConfigDir = func() (string, error) { return filepath.Join(root, "config"), nil }
	options.executablePath = func() (string, error) { return filepath.Join(root, "bin", "studio.exe"), nil }
	deps, err := newProductionInitializer(options)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deps.close()
	model, err := deps.catalogCommands.CreateModel(ctx, catalog.CreateModelCommand{Name: "glm-5.3", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := deps.catalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{Name: "unmapped channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true, APIKey: "test-only-key"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := deps.catalog.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	command := catalog.CreatePlanCommand{Name: "reference plan", ModelIDs: []string{model.ID}, ChannelIDs: []string{channel.ID}}
	for _, suite := range snapshot.Suites {
		if !strings.Contains(suite.Key, "glm-5.3") {
			continue
		}
		command.Suites = append(command.Suites, catalog.PlanSuiteInput{SuiteID: suite.ID, SuiteRevision: suite.Revision, LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 60000, SLAThresholds: map[string]float64{"e2e_p95_ms": 3000}})
		if len(command.Suites) == 2 {
			break
		}
	}
	if len(command.Suites) != 2 {
		t.Fatal("expected two bundled GLM suites")
	}
	start := time.Now()
	result, err := deps.catalogCommands.CreatePlan(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("save two Suite references: %s", time.Since(start))
	start = time.Now()
	refreshed, err := deps.catalog.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Plans) != 1 || refreshed.Plans[0].ID != result.ID || len(refreshed.Plans[0].Suites) != 2 {
		t.Fatalf("saved Plan missing from refresh: %#v", refreshed.Plans)
	}
	if _, err := deps.query.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("catalog and workspace refresh: %s", time.Since(start))
	files, err := filepath.Glob(filepath.Join(root, "bin", "data", "plans", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("plan files=%v: %v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"cases"`, `"target_bindings"`, `"credential_id"`} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("Plan contains execution field %s", field)
		}
	}
}
