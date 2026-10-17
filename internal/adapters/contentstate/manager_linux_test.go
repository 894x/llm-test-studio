package contentstate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/adapters/contentstate"
	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func TestManagerWithPhysicalStateAndSQLiteOwnsActualLinuxFDs(t *testing.T) {
	state, physical, ledger, manager := managerLinuxFixture(t)
	installation := physical.Store().InstallationID
	body := []byte("verified kernel descriptors have independent offsets")
	sum := sha256.Sum256(body)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	first, err := manager.Upload(t.Context(), identity, io.NopCloser(bytes.NewReader(body)))
	if err != nil || !first.PhysicalCreated || first.Generation.Object.Content != identity {
		t.Fatalf("physical upload=%+v err=%v", first, err)
	}
	second, err := manager.Upload(t.Context(), identity, io.NopCloser(bytes.NewReader(body)))
	if err != nil || second.PhysicalCreated || !reflect.DeepEqual(first.Generation, second.Generation) {
		t.Fatalf("physical duplicate changed generation=%+v err=%v", second, err)
	}
	stored, err := ledger.ContentPublication(t.Context(), physical.Store().ID, identity.SHA256)
	if err != nil || stored.Object != first.Generation.Object {
		t.Fatalf("SQLite did not retain actual physical identity: %+v %v", stored, err)
	}
	filename := filepath.Join(state.Path(), "content-"+identity.SHA256+".blob")
	if count := managerContentFDCount(t, filename); count != 0 {
		t.Fatalf("upload retained %d real descriptors", count)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	spec := managerLinuxSpec(t, installation, identity)
	prepared, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if count := managerContentFDCount(t, filename); count != 1 {
		t.Fatalf("prepared anchor owns %d kernel descriptors, want one", count)
	}
	digest, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ValidatePermit(prepared.Permit(), digest)
	if err != nil || len(claims) != 1 || claims[0].Object != first.Generation.Object {
		t.Fatalf("permit lost physical ownership: %+v %v", claims, err)
	}
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	cancel()
	one, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal("submission cancellation affected actual bound FDs", err)
	}
	defer one.Close()
	prefix := make([]byte, 4)
	if _, err := io.ReadFull(one, prefix); err != nil || !bytes.Equal(prefix, body[:4]) {
		t.Fatal("first kernel reader failed", err)
	}
	two, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	if count := managerContentFDCount(t, filename); count != 3 {
		t.Fatalf("anchor and two borrowers own %d kernel descriptors, want three", count)
	}
	all, err := io.ReadAll(two)
	if err != nil || !bytes.Equal(all, body) {
		t.Fatal("second kernel reader shared first reader offset", err)
	}
	rest, err := io.ReadAll(one)
	if err != nil || !bytes.Equal(rest, body[4:]) {
		t.Fatal("second reader moved first reader's kernel offset", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if count := managerContentFDCount(t, filename); count != 0 {
		t.Fatalf("Manager.Close retained %d actual content descriptors", count)
	}
	if got := manager.Status(); got != (jobcontent.Status{}) {
		t.Fatalf("Manager.Close retained logical ownership: %+v", got)
	}
	if _, err := one.Read(make([]byte, 1)); err == nil {
		t.Fatal("Manager.Close left a physical borrower usable")
	}
	if err := physical.Ready(t.Context()); err != nil {
		t.Fatal("Manager.Close closed runtime-owned State", err)
	}
	if _, err := ledger.ContentPublication(t.Context(), physical.Store().ID, identity.SHA256); err != nil {
		t.Fatal("Manager.Close closed runtime-owned SQLite", err)
	}
	t.Logf("actual store=%s incarnation=%s verified content FD counts: 0 -> 1 -> 3 -> 0",
		physical.Store().ID, first.Generation.Object.IncarnationID)
}

func managerLinuxFixture(t *testing.T) (
	*runnerstate.State,
	*contentstate.Adapter,
	*sqlite.Repository,
	*jobcontent.Manager,
) {
	t.Helper()
	state, err := runnerstate.Open(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	const installation = "fc000000-0000-4000-8000-000000000001"
	physical, err := contentstate.New(t.Context(), contentstate.Config{State: state, InstallationID: installation})
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "content.db")
	if err := sqlite.Migrate(t.Context(), databasePath, sqlite.MigrateOptions{AppVersion: "manager-fd-test"}); err != nil {
		t.Fatal(err)
	}
	ledger, err := sqlite.OpenRepository(t.Context(), databasePath, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	manager, err := jobcontent.New(t.Context(), jobcontent.Dependencies{
		Physical: physical, Ledger: ledger, Limits: jobcontent.DefaultLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return state, physical, ledger, manager
}

func TestManagerEmptyPrepareRejectsClosedOrReplacedPhysicalRoot(t *testing.T) {
	for _, mode := range []string{"closed", "root replaced"} {
		t.Run(mode, func(t *testing.T) {
			state, physical, _, manager := managerLinuxFixture(t)
			spec := managerLinuxSpec(t, physical.Store().InstallationID, jobcontract.ContentIdentity{
				SHA256: strings.Repeat("a", 64), Size: 1,
			})
			spec.Assets = []jobcontract.Asset{}
			if mode == "closed" {
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(state.Path(), state.Path()+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(state.Path(), 0700); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := manager.Prepare(t.Context(), spec)
			if err == nil || prepared != nil {
				if prepared != nil {
					_ = prepared.Close()
				}
				t.Fatalf("empty resources ignored physical failure: %v", err)
			}
			if got := manager.Status(); got != (jobcontent.Status{}) {
				t.Fatalf("failed empty preparation retained ownership: %+v", got)
			}
		})
	}
}

func TestManagerAfterStateCloseRejectsBorrowAndReleasesActualAnchor(t *testing.T) {
	state, physical, _, manager := managerLinuxFixture(t)
	body := []byte("state closes before its manager")
	sum := sha256.Sum256(body)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	if _, err := manager.Upload(t.Context(), identity, io.NopCloser(bytes.NewReader(body))); err != nil {
		t.Fatal(err)
	}
	spec := managerLinuxSpec(t, physical.Store().InstallationID, identity)
	prepared, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	filename := filepath.Join(state.Path(), "content-"+identity.SHA256+".blob")
	if count := managerContentFDCount(t, filename); count != 1 {
		t.Fatalf("actual live anchor count=%d", count)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if stream, err := resources.Resolver().Open(t.Context(), "input"); err == nil || stream != nil {
		if stream != nil {
			_ = stream.Close()
		}
		t.Fatalf("closed State admitted a borrower: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Logf("manager reported closed physical state while releasing ownership: %v", err)
	}
	if got := manager.Status(); got != (jobcontent.Status{}) {
		t.Fatalf("closed physical state retained manager ownership: %+v", got)
	}
	if count := managerContentFDCount(t, filename); count != 0 {
		t.Fatalf("closed physical state retained %d actual anchors", count)
	}
}

func managerLinuxSpec(t *testing.T, installation string, identity jobcontract.ContentIdentity) jobcontract.JobSpec {
	t.Helper()
	prepared, secret, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "fixture", APIKey: "fixture-only-not-a-provider-key",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	if err := secret.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.ReportID = "fc000000-0000-4000-8000-000000000002"
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: "fc000000-0000-4000-8000-000000000003",
		NodeInstallationID: installation, EngineBuildID: strings.Repeat("a", 64),
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}, Kind: jobcontract.KindPerformance,
		Spec:   jobcontract.Payload{Performance: &prepared},
		Assets: []jobcontract.Asset{{Ref: "input", SHA256: identity.SHA256, Size: identity.Size, MediaType: "text/plain"}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	return spec
}

func managerContentFDCount(t *testing.T, filename string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if target == filename {
			count++
		}
	}
	return count
}
