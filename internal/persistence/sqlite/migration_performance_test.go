package sqlite

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestUpgradeRealHistoricalPerformanceResultsAndReserveIdentities(t *testing.T) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range catalog.Baselines[6:8] {
		t.Run(baseline.Commit[:8], func(t *testing.T) {
			path := historicalMigrationFixture(t, baseline, "-performance")
			original := readMigrationText(t, path, "SELECT document_json FROM execution_performance_results")
			specJSON := readMigrationText(t, path, "SELECT spec_json FROM execution_jobs")
			if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "performance-upgrade"}); err != nil {
				t.Fatal(err)
			}
			repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			var spec jobcontract.JobSpec
			if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
				t.Fatal(err)
			}
			if !baseline.lacksExecutionOrigin() {
				result, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
				if err != nil {
					t.Fatal(err)
				}
				if string(result.Document) != original {
					t.Fatal("performance result was changed")
				}
				return
			}
			page, err := repository.ReadUpgradeHistory(t.Context(), "execution_performance_results", 0, 100)
			if err != nil || len(page.Rows) != 1 {
				t.Fatalf("lost sealed historical performance result: %v", err)
			}
			stored := page.Rows[0][slices.Index(page.Columns, "document_json")]
			var document string
			switch value := stored.(type) {
			case string:
				document = value
			case []byte:
				document = string(value)
			}
			if document != original {
				t.Fatal("historical performance samples or conclusion changed")
			}
			// A different new remote JobID must not reclaim the archived ReportID.
			spec.JobID = "72000000-0000-4000-8000-000000000099"
			nodeID := "72000000-0000-4000-8000-000000000009"
			spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: nodeID}
			origin := domain.ExecutionOrigin{Kind: "remote", NodeID: nodeID, InstallationID: spec.NodeInstallationID,
				EngineBuildID: spec.EngineBuildID, RunnerVersion: "new", OS: "linux", Arch: "amd64"}
			nodePath := filepath.Join(t.TempDir(), "node.db")
			if err := Migrate(t.Context(), nodePath, MigrateOptions{AppVersion: "new-node"}); err != nil {
				t.Fatal(err)
			}
			node, err := OpenRepository(t.Context(), nodePath, RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer node.Close()
			if _, _, err := node.AcceptJob(t.Context(), jobhost.Acceptance{Spec: spec, Origin: origin,
				AcceptedAt: time.Now().UTC(), SecretCommitment: strings.Repeat("b", 64), ContentClaims: []jobcontent.Claim{}}); err != nil {
				t.Fatal(err)
			}
			events, err := node.JobEvents(t.Context(), spec.JobID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			_, err = repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{JobID: spec.JobID, Events: events.Events,
				Origin: remoteresults.Origin{ConfigurationID: "72000000-0000-4000-8000-000000000098", NodeID: nodeID, InstallationID: spec.NodeInstallationID}})
			if !errors.Is(err, ErrHistoricalJob) {
				t.Fatalf("remote job reclaimed historical report: %v", err)
			}
		})
	}
}
