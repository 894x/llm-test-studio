package jobhost_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestHostAdmissionFreezesItsOwnOriginAndPreservesItOnReplay(t *testing.T) {
	for _, kind := range []jobcontract.TargetKind{jobcontract.TargetLocal, jobcontract.TargetRemote} {
		t.Run(string(kind), func(t *testing.T) {
			repository, core := openHostRepository(t), newControlledCore()
			dependencies := hostDependencies(repository, core, &countedGuard{})
			dependencies.RunnerVersion = "actual-owner-release"
			if kind == jobcontract.TargetLocal {
				dependencies.Target = jobcontract.ExecutionTarget{Kind: kind}
			}
			host := newTestHost(t, dependencies)
			submission := coreSubmission(t, "https://must-not-be-requested.example.test", false)
			submission.Job.Target = dependencies.Target
			preparation := domain.EnvironmentSnapshot{OS: "client-os", Arch: "client-arch",
				AppVersion: "preparation-release", EngineVersion: "preparation-engine"}
			submission.Job.Spec.Run.Snapshot.Environment = preparation
			secrets := []jobcontract.Secret{}
			for _, secret := range submission.Secrets {
				secrets = append(secrets, jobcontract.Secret{Slot: secret.Slot, Value: bytes.Clone(secret.Value)})
			}
			replay := jobcontract.Submission{Job: submission.Job, Secrets: secrets}
			defer replay.Clear()
			accepted, created, err := host.Submit(t.Context(), &submission)
			if err != nil || !created {
				t.Fatalf("admit with real host origin: %v", err)
			}
			_ = enteredExecution(t, core)
			origin := accepted.Receipt.Origin
			actualIdentity := origin.Kind == string(kind) && origin.NodeID == dependencies.NodeID &&
				origin.InstallationID == dependencies.InstallationID && origin.EngineBuildID == dependencies.EngineBuildID
			actualRuntime := origin.OS == runtime.GOOS && origin.Arch == runtime.GOARCH &&
				origin.RunnerVersion == dependencies.RunnerVersion
			if !actualIdentity || !actualRuntime || host.Identity().Origin != origin {
				t.Fatal("admission copied client preparation metadata instead of the host's actual identity")
			}
			frozen, err := repository.GetJobSpec(t.Context(), submission.Job.JobID)
			if err != nil || frozen.Spec.Run.Snapshot.Environment != preparation {
				t.Fatal("server origin replaced the frozen preparation environment")
			}
			page, err := repository.JobEvents(t.Context(), submission.Job.JobID, 0, 1)
			if err != nil || len(page.Events) != 1 {
				t.Fatal(err)
			}
			var first jobcontract.AcceptedEvent
			if json.Unmarshal(page.Events[0].Payload, &first) != nil || first.Snapshot.Receipt.Origin != origin {
				t.Fatal("accepted journal omitted or replaced the frozen owner origin")
			}
			if err := host.Close(); err != nil {
				t.Fatal(err)
			}
			dependencies.RunnerVersion = "later-compatible-release"
			nextCore := newControlledCore()
			dependencies.Executor = nextCore
			next := newTestHost(t, dependencies)
			replayed, created, err := next.Submit(t.Context(), &replay)
			if err != nil || created || replayed.Receipt.Origin != origin {
				t.Fatalf("replay replaced the admitted origin with the new owner's release: %v", err)
			}
			nextCore.mu.Lock()
			calls := nextCore.calls
			nextCore.mu.Unlock()
			if calls != 0 || next.Identity().Origin.RunnerVersion != dependencies.RunnerVersion {
				t.Fatal("replay executed again or mixed the active owner with historical origin")
			}
		})
	}
}

func TestHostRequiresExplicitTrustedOriginDependencies(t *testing.T) {
	repository := openHostRepository(t)
	for _, change := range []func(*jobhost.Dependencies){
		func(dependencies *jobhost.Dependencies) { dependencies.NodeID = "" },
		func(dependencies *jobhost.Dependencies) { dependencies.RunnerVersion = "" },
		func(dependencies *jobhost.Dependencies) { dependencies.RunnerVersion = "invalid\nrelease" },
		func(dependencies *jobhost.Dependencies) {
			dependencies.NodeID = "88888888-8888-4888-8888-888888888888"
		},
	} {
		dependencies := hostDependencies(repository, newControlledCore(), &countedGuard{})
		change(&dependencies)
		if _, err := jobhost.New(t.Context(), dependencies); !errors.Is(err, jobhost.ErrInvalid) {
			t.Fatalf("host inferred or accepted a mismatched owner origin: %v", err)
		}
	}
}
