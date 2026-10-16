package ssh

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestUpgradeLostLaunchAcknowledgmentReadsJournalWithoutAnotherExec(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.onLaunch = func(string) uint32 {
		fixture.journal(fixture.terminal("completed"))
		return 1 // The caller receives a failed SSH ACK after the durable result.
	}
	client := dialPeer(t, fixture.peer)
	operation, err := client.Upgrade(t.Context(), fixture.request, fixture.artifact, nil)
	if err != nil || operation.Stage != "completed" || operation.Result == nil {
		t.Fatalf("lost launch acknowledgment replaced the durable result: %v", err)
	}
	if fixture.launches.Load() != 1 {
		t.Fatal("an uncertain acknowledgment started another upgrade")
	}
	_, err = client.Upgrade(t.Context(), fixture.request, fixture.artifact, nil)
	if err != nil || fixture.launches.Load() != 1 {
		t.Fatal("reconnect did not resolve the exact original operation")
	}
}

func TestUpgradeActiveUnitWithoutJournalRemainsPending(t *testing.T) {
	for _, retry := range []bool{false, true} {
		fixture := newUpgradeFixture(t)
		fixture.active.Store(true)
		before := fixture.fs.snapshot()
		client := dialPeer(t, fixture.peer)
		var err error
		if retry {
			_, err = client.RetryUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
		} else {
			_, err = client.Upgrade(t.Context(), fixture.request, fixture.artifact, nil)
		}
		var failure *UpgradeError
		pending := errors.As(err, &failure) && failure.Pending && failure.OperationID == fixture.request.OperationID &&
			failure.TemporaryDirectory == UpgradeDirectory(fixture.request.OperationID)
		if !pending || !errors.Is(err, ErrUpgradePending) || fixture.launches.Load() != 0 {
			t.Fatal("the active-before-journal window launched a duplicate or lost its operation")
		}
		if before != fixture.fs.snapshot() {
			t.Fatal("an active operation's staging directory was changed")
		}
	}
}

func TestUpgradeCancelledObserverDoesNotStopServiceOrLoseOperation(t *testing.T) {
	fixture := newUpgradeFixture(t)
	fixture.onLaunch = func(string) uint32 {
		fixture.active.Store(true)
		fixture.journal(fixture.operation("checking"))
		return 0
	}
	client := dialPeer(t, fixture.peer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	operation, err := client.Upgrade(
		ctx,
		fixture.request,
		fixture.artifact,
		func(progress runnerinstall.UpgradeProgress) {
			if progress.Stage == "checking" {
				cancel()
			}
		},
	)
	var failure *UpgradeError
	pending := errors.As(err, &failure) && failure.Pending && errors.Is(err, context.Canceled)
	if !pending || operation.Request.OperationID != fixture.request.OperationID || !fixture.active.Load() {
		t.Fatalf("cancelled observer lost the detached server operation: %v", err)
	}
	if fixture.launches.Load() != 1 {
		t.Fatal("observer cancellation relaunched the service")
	}
	reconnected := dialPeer(t, fixture.peer)
	observed, err := reconnected.UpgradeOperation(t.Context(), fixture.request.OperationID)
	if err != nil || observed.Stage != "checking" {
		t.Fatal("the cancelled observer removed its durable journal")
	}
	_, err = reconnected.RetryUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
	if !errors.Is(err, ErrUpgradePending) || fixture.launches.Load() != 1 || !fixture.active.Load() {
		t.Fatal("an active same-ID retry stopped or duplicated the detached service")
	}
}

func TestUpgradeExplicitRetryReusesVerifiedArtifactsAndOnlyRemovesOwnedPartials(t *testing.T) {
	fixture := newUpgradeFixture(t)
	directory := UpgradeDirectory(fixture.request.OperationID)
	partial := directory + "/.upload-AAAAAAAAAAAAAAAAAAAAAAAAAA"
	fixture.fs.put(partial, []byte("interrupted upload"), 0600)
	fixture.onLaunch = func(command string) uint32 {
		if command != upgradeLaunchCommand(fixture.request.OperationID, "upgrade") {
			t.Error("a pre-journal retry selected the journal-required continuation command")
			return 1
		}
		fixture.journal(fixture.terminal("completed"))
		return 0
	}
	client := dialPeer(t, fixture.peer)
	artifact := fixture.artifact
	artifact.Source = rejectingUpgradeReader{}
	operation, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
	if err != nil || operation.Stage != "completed" || fixture.launches.Load() != 1 {
		t.Fatalf("explicit same-operation retry could not recover staging: %v", err)
	}
	fixture.fs.mu.Lock()
	_, remains := fixture.fs.entries[partial]
	fixture.fs.mu.Unlock()
	if remains {
		t.Fatal("validated abandoned partial was not removed")
	}
}

func TestUpgradePartialCleanupRequiresNoJournalAndInactiveUnit(t *testing.T) {
	for _, scenario := range []string{"active_unit", "existing_journal", "foreign_partial", "public_partial", "unknown_partial"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			partial := UpgradeDirectory(fixture.request.OperationID) + "/.upload-AAAAAAAAAAAAAAAAAAAAAAAAAA"
			fixture.fs.put(partial, []byte("preserve this partial"), 0600)
			switch scenario {
			case "active_unit":
				fixture.active.Store(true)
			case "existing_journal":
				fixture.journal(fixture.operation("checking"))
			case "foreign_partial", "public_partial":
				fixture.fs.mu.Lock()
				entry := fixture.fs.entries[partial]
				if scenario == "foreign_partial" {
					entry.owner = 1000
				} else {
					entry.mode = 0644
				}
				fixture.fs.entries[partial] = entry
				fixture.fs.mu.Unlock()
			case "unknown_partial":
				fixture.fs.put(UpgradeDirectory(fixture.request.OperationID)+"/.upload-not-an-owned-random-name", []byte("retain"), 0600)
			}
			before := fixture.fs.snapshot()
			client := dialPeer(t, fixture.peer)
			_, err := client.RetryUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
			if err == nil || before != fixture.fs.snapshot() || fixture.launches.Load() != 0 {
				t.Fatal("partial cleanup crossed an active, journal, metadata, or namespace boundary")
			}
		})
	}
}

func TestUpgradeExplicitRetryAndRecoveryUseOnlyOriginalJournalIdentity(t *testing.T) {
	for _, recover := range []bool{false, true} {
		fixture := newUpgradeFixture(t)
		baseline := fixture.operation("cutover")
		baseline.Restorable = true
		baseline.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrService)
		fixture.journal(baseline)
		verb, terminal := "retry-upgrade", "completed"
		if recover {
			verb, terminal = "recover-upgrade", "rolled_back"
		}
		fixture.onLaunch = func(command string) uint32 {
			if command != upgradeLaunchCommand(fixture.request.OperationID, verb) {
				t.Error("explicit continuation selected another command or operation")
				return 1
			}
			fixture.journal(fixture.terminal(terminal))
			return 0
		}
		client := dialPeer(t, fixture.peer)
		var operation runnerinstall.UpgradeOperation
		var err error
		if recover {
			operation, err = client.RecoverUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
		} else {
			operation, err = client.RetryUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
		}
		if err != nil || operation.Stage != terminal || fixture.launches.Load() != 1 {
			t.Fatalf("explicit continuation did not resolve the original operation: %v", err)
		}
	}
}

func TestUpgradeRecoveryWithoutJournalDoesNotCreateAnOperation(t *testing.T) {
	fixture := newUpgradeFixture(t)
	client := dialPeer(t, fixture.peer)
	_, err := client.RecoverUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
	if !errors.Is(err, os.ErrNotExist) || fixture.execs.Load() != 0 {
		t.Fatal("recovering an absent operation created or executed one")
	}
}

func TestUpgradePollingDoesNotMistakeOriginalErrorForRetryResult(t *testing.T) {
	fixture := newUpgradeFixture(t)
	baseline := fixture.operation("checking")
	baseline.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrBusy)
	fixture.journal(baseline)
	finished := make(chan struct{})
	fixture.onLaunch = func(string) uint32 {
		fixture.active.Store(true)
		go func() {
			defer close(finished)
			timer := time.NewTimer(20 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
			fixture.journal(fixture.terminal("completed"))
			fixture.active.Store(false)
		}()
		return 0
	}
	client := dialPeer(t, fixture.peer)
	operation, err := client.RetryUpgrade(t.Context(), fixture.request, fixture.artifact, nil)
	<-finished
	if err != nil || operation.Stage != "completed" {
		t.Fatalf("the old failure was treated as a fresh retry result: %v", err)
	}
}

func TestUpgradeObserverRechecksJournalAfterUnitExit(t *testing.T) {
	fixture := newUpgradeFixture(t)
	baseline := fixture.operation("cutover")
	baseline.Restorable = true
	baseline.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrService)
	fixture.journal(baseline)
	fixture.onLaunch = func(string) uint32 { fixture.active.Store(true); return 0 }
	client := dialPeer(t, fixture.peer)
	operation, err := client.RetryUpgrade(
		t.Context(),
		fixture.request,
		fixture.artifact,
		func(runnerinstall.UpgradeProgress) {
			fixture.journal(fixture.terminal("completed"))
			fixture.active.Store(false)
		},
	)
	valid := operation.Stage == "completed" && operation.ResumeAttempted && !operation.Restorable
	if err != nil || !valid {
		t.Fatalf("unit exit returned an obsolete failed restorable journal: %v", err)
	}
}

func TestUpgradeExplicitContinuationReusesStagedProgramWithoutLocalRelease(t *testing.T) {
	for _, recover := range []bool{false, true} {
		fixture := newUpgradeFixture(t)
		baseline := fixture.operation("cutover")
		baseline.Restorable = true
		fixture.journal(baseline)
		verb, terminal := "retry-upgrade", "completed"
		if recover {
			verb, terminal = "recover-upgrade", "rolled_back"
		}
		fixture.onLaunch = func(command string) uint32 {
			if command != upgradeLaunchCommand(fixture.request.OperationID, verb) {
				t.Error("staged reuse changed the operation command")
				return 1
			}
			fixture.journal(fixture.terminal(terminal))
			return 0
		}
		artifact := fixture.artifact
		artifact.Source, artifact.Size = nil, 0
		client := dialPeer(t, fixture.peer)
		var operation runnerinstall.UpgradeOperation
		var err error
		if recover {
			operation, err = client.RecoverUpgrade(t.Context(), fixture.request, artifact, nil)
		} else {
			operation, err = client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
		}
		if err != nil || operation.Stage != terminal || fixture.launches.Load() != 1 {
			t.Fatalf("the original staged program could not continue after local release removal: %v", err)
		}
	}
}

func TestUpgradeStagedReuseRequiresCompleteExactOwnedFiles(t *testing.T) {
	for _, scenario := range []string{
		"missing_directory", "missing_program", "missing_request", "wrong_program", "wrong_request",
		"foreign_program", "public_program", "symlink_program", "extra_partial", "zero_size", "wrong_size",
	} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpgradeFixture(t)
			fixture.journal(fixture.operation("checking"))
			directory := UpgradeDirectory(fixture.request.OperationID)
			artifact := fixture.artifact
			artifact.Source, artifact.Size = nil, 0
			fixture.fs.mu.Lock()
			program := fixture.fs.entries[directory+"/llm-test-runner"]
			switch scenario {
			case "missing_directory":
				delete(fixture.fs.entries, directory)
			case "missing_program":
				delete(fixture.fs.entries, directory+"/llm-test-runner")
			case "missing_request":
				delete(fixture.fs.entries, directory+"/request.json")
			case "wrong_program":
				program.data[0] ^= 1
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "wrong_request":
				request := fixture.fs.entries[directory+"/request.json"]
				request.data[0] ^= 1
				fixture.fs.entries[directory+"/request.json"] = request
			case "foreign_program":
				program.owner = 1000
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "public_program":
				program.mode = 0755
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "symlink_program":
				program.mode = os.ModeSymlink | 0700
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "extra_partial":
				fixture.fs.entries[directory+"/.upload-AAAAAAAAAAAAAAAAAAAAAAAAAA"] = upgradeFixtureEntry{
					data: []byte("do not delete during stage-only reuse"), mode: 0600,
				}
			case "zero_size":
				program.data = []byte{}
				fixture.fs.entries[directory+"/llm-test-runner"] = program
			case "wrong_size":
				artifact.Size = fixture.artifact.Size + 1
			}
			fixture.fs.mu.Unlock()
			before := fixture.fs.snapshot()
			client := dialPeer(t, fixture.peer)
			_, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
			if err == nil || fixture.launches.Load() != 0 || before != fixture.fs.snapshot() {
				t.Fatal("stage-only reuse created, changed, deleted, or launched unverified artifacts")
			}
		})
	}
}

func TestUpgradeStagedReuseCannotStartWithoutOriginalJournal(t *testing.T) {
	fixture := newUpgradeFixture(t)
	artifact := fixture.artifact
	artifact.Source, artifact.Size = nil, 0
	client := dialPeer(t, fixture.peer)
	if _, err := client.Upgrade(t.Context(), fixture.request, artifact, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("initial upgrade accepted a missing local verified artifact")
	}
	_, err := client.RetryUpgrade(t.Context(), fixture.request, artifact, nil)
	if !errors.Is(err, ErrUpgradeArtifactUnavailable) || fixture.execs.Load() != 0 {
		t.Fatal("stage-only retry created an operation without its original journal")
	}
}
