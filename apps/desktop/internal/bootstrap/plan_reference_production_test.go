package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestProductionPlanSaveOnlyStoresReferencesWithoutMappings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	options := DefaultOptions("dev")
	options.CredentialStore = credentials.NewMemoryStore()
	options.UserConfigDir = func() (string, error) { return filepath.Join(root, "config"), nil }
	options.ExecutablePath = func() (string, error) { return filepath.Join(root, "bin", "studio.exe"), nil }
	deps, err := NewInitializer(options)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deps.Close()
	_, err = deps.CatalogCommands.CreateModel(ctx, catalog.CreateModelCommand{Name: "glm-5.3", Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, Capabilities: []string{"chat"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = deps.CatalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{Name: "unmapped channel", BaseURL: "https://example.test/v1", Enabled: true, APIKey: "test-only-key"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := deps.Catalog.Snapshot(ctx, catalog.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	command := catalog.CreatePlanCommand{Name: "reference plan", Protocol: domain.ProtocolOpenAIChat, Seed: 1}
	for _, suite := range snapshot.Suites {
		if !strings.Contains(suite.Key, "glm-5.3") {
			continue
		}
		command.Entries = append(command.Entries, catalog.PlanEntryInput{TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 60000, SLAThresholds: map[string]float64{"e2e_p95_ms": 3000}})
		if len(command.Entries) == 2 {
			break
		}
	}
	if len(command.Entries) != 2 {
		t.Fatal("expected two bundled GLM suites")
	}
	start := time.Now()
	result, err := deps.CatalogCommands.CreatePlan(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("save two Suite references: %s", time.Since(start))
	start = time.Now()
	refreshed, err := deps.Catalog.Snapshot(ctx, catalog.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	customPlans := []catalog.PlanSummary{}
	for _, plan := range refreshed.Plans {
		if !plan.Builtin {
			customPlans = append(customPlans, plan)
		}
	}
	if len(customPlans) != 1 || customPlans[0].ID != result.ID || len(customPlans[0].Entries) != 2 {
		t.Fatalf("saved Plan missing from refresh: %#v", refreshed.Plans)
	}
	if _, err := deps.Query.Snapshot(ctx); err != nil {
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
