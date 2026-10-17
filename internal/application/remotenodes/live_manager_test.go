package remotenodes

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

// This opt-in check only probes and pairs an existing runner. Artifacts are not
// configured, so it cannot install or upgrade a server or call a provider.
func TestLiveNodeManagerPairsIndependentClientsAndReusesSavedBinding(t *testing.T) {
	if os.Getenv("LLM_TEST_LIVE_NODE_MANAGER") != "1" {
		t.Skip("explicit existing physical-node validation only")
	}
	document, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<10+1))
	if err != nil || len(document) > 8<<10 {
		t.Fatal("invalid live-node input")
	}
	defer clear(document)
	var input struct {
		Endpoint             remotessh.Endpoint `json:"endpoint"`
		Password             []byte             `json:"password"`
		ExpectedNodeID       string             `json:"expected_node_id"`
		ExpectedInstallation string             `json:"expected_installation_id"`
	}
	if json.Unmarshal(document, &input) != nil {
		t.Fatal("invalid live-node input")
	}
	defer clear(input.Password)
	validEndpoint := input.Endpoint.Validate() == nil && input.Endpoint.Fingerprint != ""
	if !validEndpoint || len(input.Password) == 0 {
		t.Fatal("live validation requires a pinned endpoint and password")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := ""
	for index := range 2 {
		fixture := newNodeFixture(t)
		fixture.now = time.Now().UTC()
		manager, err := NewManager(ctx, ManagerDependencies{Configuration: fixture.service})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = manager.Close() })
		password, err := credentials.NewTemporaryLease(input.Password)
		if err != nil {
			t.Fatal("cannot create transient credential")
		}
		node, err := manager.Save(ctx, SaveCommand{
			ID: fixtureID(t), Name: "physical manager validation", Region: "validation",
			Host: input.Endpoint.Host, Port: input.Endpoint.Port, User: input.Endpoint.User, Enabled: true,
			Authentication: AuthenticationPassword,
			HostKey:        &HostKey{Type: input.Endpoint.HostKeyType, Fingerprint: input.Endpoint.Fingerprint},
			Secrets:        SecretUpdate{Password: password},
		})
		_ = password.Close()
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.Lock()
		task := manager.checks[node.ID]
		manager.mu.Unlock()
		if task != nil {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-task.done:
				if task.err != nil {
					t.Fatal(task.err)
				}
			}
		}
		snapshot, err := manager.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		view := snapshot.Nodes[0]
		identityMatches := view.RunnerNodeID == input.ExpectedNodeID && view.InstallationID == input.ExpectedInstallation
		reused := view.Eligible && identityMatches && view.Installation == nil
		if !reused {
			t.Fatal("existing runner was not reused with the expected shared identity")
		}
		info := view.Observation.Runner
		if index == 0 {
			build = info.EngineBuildID
		}
		if info.EngineBuildID != build {
			t.Fatal("compatible shared connection changed the actual runner build")
		}
		if err := manager.SelectDefault(ctx, snapshot.Default.Revision, jobcontract.ExecutionTarget{
			Kind: jobcontract.TargetRemote, NodeID: node.ID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Check(ctx, node.ID, view.Revision); err != nil {
			t.Fatal(err)
		}
		persisted, err := fixture.store.Read(ctx)
		if err != nil || persisted.Default.NodeID != node.ID {
			t.Fatal("remote default or saved binding did not persist")
		}
		t.Logf(
			"client=%d node=%s installation=%s version=%s build=%s",
			index+1,
			view.RunnerNodeID,
			view.InstallationID,
			info.RunnerVersion,
			info.EngineBuildID,
		)
		_ = manager.Close()
	}
}
