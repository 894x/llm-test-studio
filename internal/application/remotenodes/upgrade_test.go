package remotenodes

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func upgradeManagerFixture(t *testing.T, remote *remoteFixture) (*nodeFixture, Node, *Manager) {
	t.Helper()
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	if err := fixture.service.RecordObservation(t.Context(), node.ID, node.Revision, readyObservation(node, fixture.now)); err != nil {
		t.Fatal(err)
	}
	config, err := fixture.store.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	node = config.Nodes[0]
	manager := managerFixture(t, fixture, remote)
	manager.artifacts, manager.release = managerArtifactSource(t), "v-upgrade-fixture"
	return fixture, node, manager
}

func upgradeStartCommand(t *testing.T, node Node) UpgradeCommand {
	t.Helper()
	return UpgradeCommand{ID: node.ID, ExpectedRevision: node.Revision, OperationID: fixtureID(t),
		ExpectedBuildID: node.Observation.Runner.EngineBuildID}
}

func waitUpgrade(t *testing.T, manager *Manager, id string) {
	t.Helper()
	manager.mu.Lock()
	task := manager.upgrades[id]
	manager.mu.Unlock()
	if task == nil {
		return
	}
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("upgrade observer did not stop")
	}
}

func completedUpgrade(node Node, request runnerinstall.UpgradeRequest, at time.Time) runnerinstall.UpgradeOperation {
	info := *node.Observation.Runner
	info.EngineBuildID = request.ArtifactSHA256
	return runnerinstall.UpgradeOperation{
		Request: request, Stage: "completed", ChangedAt: at, ResumeAttempted: true,
		Result: &runnerinstall.UpgradeResult{Contract: request.Contract, OperationID: request.OperationID,
			Runner: info, ObservedAt: at},
	}
}

func TestUpgradePersistsIntentBeforeSSHAndObserverCancellationOnlyStopsWaiting(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	remote.upgrade = func(ctx context.Context, request runnerinstall.UpgradeRequest, artifact remotessh.Artifact,
		_ func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		calls.Add(1)
		intent, err := fixture.store.readUpgrade(ctx, node.ID)
		if err != nil || intent == nil || intent.Request != request || intent.TemporaryDirectory != remotessh.UpgradeDirectory(request.OperationID) {
			t.Error("SSH began without its durable exact intent")
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("closing the caller canceled the owned upgrade observer")
			return runnerinstall.UpgradeOperation{}, ctx.Err()
		}
		return completedUpgrade(node, request, fixture.now), nil
	}
	observer, stop := context.WithCancel(t.Context())
	command := upgradeStartCommand(t, node)
	attempt, err := manager.StartUpgrade(observer, command)
	if err != nil || attempt == nil || attempt.Request.OperationID != command.OperationID {
		t.Fatalf("start: %v", err)
	}
	stop()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned upgrade did not begin")
	}
	if _, err := manager.GetUpgrade(t.Context(), node.ID); err != nil || calls.Load() != 1 {
		t.Fatal("local query executed remote work")
	}
	if _, err := manager.StartUpgrade(t.Context(), upgradeStartCommand(t, node)); !errors.Is(err, ErrBusy) {
		t.Fatal("another operation entered the same node")
	}
	close(release)
	waitUpgrade(t, manager, node.ID)
	stored, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || stored.Operation == nil || stored.Operation.Result == nil || calls.Load() != 1 {
		t.Fatal("completed server receipt was not persisted")
	}
	document, err := os.ReadFile(fixture.store.upgradePath(node.ID))
	if err != nil || strings.Contains(string(document), "fixture-password") || strings.Contains(string(document), "token_ref") {
		t.Fatal("upgrade intent contains credential material")
	}
}

func TestUpgradeLostAcknowledgmentQueriesOriginalOperationBeforeAnyRetry(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	command := upgradeStartCommand(t, node)
	var launches, queries atomic.Int32
	var accepted runnerinstall.UpgradeOperation
	remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
		queries.Add(1)
		if accepted.Stage == "" {
			return runnerinstall.UpgradeOperation{}, os.ErrNotExist
		}
		return accepted, nil
	}
	remote.upgrade = func(_ context.Context, request runnerinstall.UpgradeRequest, _ remotessh.Artifact,
		_ func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		launches.Add(1)
		accepted = completedUpgrade(node, request, fixture.now)
		return runnerinstall.UpgradeOperation{}, errors.New("private SSH/password/remote-body must not escape")
	}
	if _, err := manager.StartUpgrade(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	uncertain, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || uncertain.Operation != nil || uncertain.Request.OperationID != command.OperationID {
		t.Fatal("an uncertain acknowledgment became a receipt or lost its ID")
	}
	if uncertain.ErrorCode != "runner_installation_failed" {
		t.Fatal("a raw remote error escaped the fixed diagnostic vocabulary")
	}
	if _, err := manager.RetryUpgrade(t.Context(), UpgradeAction{ID: node.ID, ExpectedRevision: node.Revision, OperationID: command.OperationID}); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	confirmed, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || confirmed.Operation == nil || confirmed.Operation.Stage != "completed" || launches.Load() != 1 || queries.Load() != 2 {
		t.Fatal("retry did not first recover the original journal")
	}
	if _, err := manager.RetryUpgrade(t.Context(), UpgradeAction{ID: node.ID, ExpectedRevision: node.Revision, OperationID: fixtureID(t)}); !errors.Is(err, ErrConflict) {
		t.Fatal("retry accepted another operation ID")
	}
}

func TestUpgradeRejectsStaleCASWithoutIntentAndCheckNeverStartsUpgrade(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	var upgrades atomic.Int32
	remote.bound = func(context.Context, Binding, *credentials.Lease) (jobcontract.RunnerInfo, error) {
		info := *node.Observation.Runner
		info.RunnerVersion = "independent-runner-version"
		return info, nil
	}
	remote.upgrade = func(context.Context, runnerinstall.UpgradeRequest, remotessh.Artifact,
		func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		upgrades.Add(1)
		return runnerinstall.UpgradeOperation{}, runnerinstall.ErrService
	}
	command := upgradeStartCommand(t, node)
	command.ExpectedBuildID = strings.Repeat("a", 64)
	if _, err := manager.StartUpgrade(t.Context(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("stale expected build was accepted")
	}
	if _, err := os.Stat(fixture.store.upgradePath(node.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a rejected request wrote an intent")
	}
	if err := manager.Check(t.Context(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	if upgrades.Load() != 0 {
		t.Fatal("background check upgraded a compatible independent runner build")
	}
	attempt, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || attempt != nil {
		t.Fatal("a normal compatible check invented an upgrade intent")
	}
}

func TestUpgradeManagerCloseRetainsUncertainIntentAndDoesNotControlJobs(t *testing.T) {
	remote := &remoteFixture{}
	_, node, manager := upgradeManagerFixture(t, remote)
	entered := make(chan struct{})
	remote.upgrade = func(ctx context.Context, _ runnerinstall.UpgradeRequest, _ remotessh.Artifact,
		_ func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		close(entered)
		<-ctx.Done()
		return runnerinstall.UpgradeOperation{}, ctx.Err()
	}
	command := upgradeStartCommand(t, node)
	if _, err := manager.StartUpgrade(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upgrade did not begin")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	attempt, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || attempt == nil || attempt.Request.OperationID != command.OperationID || attempt.Operation != nil {
		t.Fatal("shutdown changed or invented an upgrade acknowledgment")
	}
}

func TestUpgradeProgressConfirmationCannotRegressToOlderTransportSnapshot(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		terminal bool
		drift    time.Duration
	}{
		{name: "completed", terminal: true, drift: time.Second},
		{name: "resume_same_clock"},
		{name: "resume_clock_reversed", drift: -time.Second},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			var observed *runnerinstall.UpgradeOperation
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				if observed == nil {
					return runnerinstall.UpgradeOperation{}, os.ErrNotExist
				}
				return *observed, nil
			}
			remote.upgrade = func(_ context.Context, request runnerinstall.UpgradeRequest, _ remotessh.Artifact,
				progress func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
				old := runnerinstall.UpgradeOperation{Request: request, Stage: "cutover", ChangedAt: fixture.now,
					Restorable: true, ErrorCode: runnerinstall.ErrorCode(runnerinstall.ErrService)}
				newest := completedUpgrade(node, request, fixture.now.Add(scenario.drift))
				if !scenario.terminal {
					newest.Stage, newest.Result = "resume_attempted", nil
				}
				observed = &newest
				progress(runnerinstall.UpgradeProgress{OperationID: request.OperationID, Stage: "cutover"})
				return old, runnerinstall.ErrService
			}
			if _, err := manager.StartUpgrade(t.Context(), upgradeStartCommand(t, node)); err != nil {
				t.Fatal(err)
			}
			waitUpgrade(t, manager, node.ID)
			stored, err := manager.GetUpgrade(t.Context(), node.ID)
			if err != nil || stored.Operation == nil || !stored.Operation.ResumeAttempted || stored.Operation.Restorable {
				t.Fatal("an older transport return discarded an irreversible confirmed fact")
			}
			if scenario.terminal && (stored.Stage != "completed" || stored.ErrorCode != "") {
				t.Fatal("a stale transport error downgraded the confirmed terminal receipt")
			}
		})
	}
}

func TestUpgradeRecoveryCanUseTheConfirmedOperationStageAfterLocalReleaseRemoval(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	var journal runnerinstall.UpgradeOperation
	remote.upgrade = func(_ context.Context, request runnerinstall.UpgradeRequest, _ remotessh.Artifact,
		_ func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		journal = runnerinstall.UpgradeOperation{Request: request, Stage: "verifying", ChangedAt: fixture.now,
			Restorable: true, ErrorCode: runnerinstall.ErrorCode(runnerinstall.ErrService)}
		return journal, runnerinstall.ErrService
	}
	command := upgradeStartCommand(t, node)
	if _, err := manager.StartUpgrade(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	manager.artifacts = nil
	remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) { return journal, nil }
	var recoveries atomic.Int32
	remote.recoverUpgrade = func(_ context.Context, request runnerinstall.UpgradeRequest, artifact remotessh.Artifact,
		_ func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		recoveries.Add(1)
		if request != journal.Request || artifact.Source != nil || artifact.Size != 0 || artifact.SHA256 != request.ArtifactSHA256 {
			t.Error("recovery changed the operation or supplied unverified executable bytes")
		}
		return completedUpgrade(node, request, fixture.now.Add(time.Second)), nil
	}
	if _, err := manager.RecoverUpgrade(t.Context(), UpgradeAction{ID: node.ID,
		ExpectedRevision: node.Revision, OperationID: command.OperationID}); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	if recoveries.Load() != 1 {
		t.Fatal("a missing local release prevented recovery of its confirmed retained stage")
	}
}

func TestUpgradeRollbackResumeBoundarySurvivesEqualOrReversedServerClock(t *testing.T) {
	for _, drift := range []time.Duration{0, -time.Second} {
		t.Run(drift.String(), func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			var observed *runnerinstall.UpgradeOperation
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				if observed == nil {
					return runnerinstall.UpgradeOperation{}, os.ErrNotExist
				}
				return *observed, nil
			}
			remote.upgrade = func(_ context.Context, request runnerinstall.UpgradeRequest, _ remotessh.Artifact,
				progress func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
				before := runnerinstall.UpgradeOperation{Request: request, Stage: "verifying", ChangedAt: fixture.now,
					Restorable: true, ErrorCode: runnerinstall.ErrorCode(runnerinstall.ErrService)}
				observed = &before
				progress(runnerinstall.UpgradeProgress{OperationID: request.OperationID, Stage: before.Stage})
				after := before
				after.Stage, after.Restorable, after.ChangedAt = "rolling_back", false, fixture.now.Add(drift)
				observed = &after
				progress(runnerinstall.UpgradeProgress{OperationID: request.OperationID, Stage: after.Stage})
				return before, runnerinstall.ErrService
			}
			command := upgradeStartCommand(t, node)
			if _, err := manager.StartUpgrade(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			waitUpgrade(t, manager, node.ID)
			stored, err := manager.GetUpgrade(t.Context(), node.ID)
			if err != nil || stored.Operation == nil || stored.Operation.Stage != "rolling_back" || stored.Operation.Restorable {
				t.Fatal("rollback resume's irreversible fact depended on wall-clock ordering")
			}
			if !stored.ChangedAt.After(fixture.now) {
				t.Fatal("local observation order did not advance with a fixed desktop clock")
			}
		})
	}
}
