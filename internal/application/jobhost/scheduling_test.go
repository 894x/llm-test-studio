package jobhost_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type taskSlotStore struct {
	value uint32
	calls int
	err   error
}

func (store *taskSlotStore) Save(_ context.Context, expected, limit uint32) error {
	store.calls++
	if store.err != nil {
		return store.err
	}
	if store.value != expected {
		return jobhost.ErrConflict
	}
	store.value = limit
	return nil
}

func slotCommand(host *jobhost.Host, expected, limit uint32) jobcontract.TaskConcurrencyCommand {
	identity := host.Identity()
	return jobcontract.TaskConcurrencyCommand{Contract: jobcontract.CurrentContract,
		NodeID: identity.Origin.NodeID, NodeInstallationID: identity.InstallationID,
		ExpectedMaxActiveJobs: expected, MaxActiveJobs: limit}
}

func TestTaskSlotsPersistBeforeDispatchAndNeverInterruptActiveJobs(t *testing.T) {
	repository, core, guard := openHostRepository(t), newControlledCore(), &countedGuard{}
	host := newTestHost(t, hostDependencies(repository, core, guard))
	store := &taskSlotStore{value: 1, err: errors.New("write failed")}
	scheduling, err := jobhost.NewScheduling(host, store)
	if err != nil {
		t.Fatal(err)
	}
	first, second := testSubmission(t), testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	one := enteredExecution(t, core)
	if _, _, err := host.Submit(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	if err := scheduling.Apply(t.Context(), slotCommand(host, 1, 2)); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if status := host.Status(); status.Active != 1 || status.Queued != 1 || status.MaxActiveJobs != 1 {
		t.Fatal("failed save changed scheduling")
	}
	store.err = nil
	if err := scheduling.Apply(t.Context(), slotCommand(host, 1, 2)); err != nil {
		t.Fatal(err)
	}
	two := enteredExecution(t, core)
	if status := host.Status(); status.Active != 2 || status.Queued != 0 || store.value != 2 {
		t.Fatal("increasing task slots did not start the queued task")
	}
	if err := scheduling.Apply(t.Context(), slotCommand(host, 2, 1)); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatal("decrease below active task count accepted")
	}
	if one.context.Err() != nil || two.context.Err() != nil {
		t.Fatal("configuration change interrupted active work")
	}
	calls := store.calls
	if err := scheduling.Apply(t.Context(), slotCommand(host, 1, 2)); err != nil || store.calls != calls {
		t.Fatal("retry of the same setting performed another write")
	}
	wrong := slotCommand(host, 2, 4)
	wrong.NodeInstallationID = "99999999-9999-4999-8999-999999999999"
	if err := scheduling.Apply(t.Context(), wrong); !errors.Is(err, jobhost.ErrIdentity) {
		t.Fatal("foreign installation changed the node")
	}
	close(core.release)
	waitHost(t, func() bool { return host.Status().Active == 0 })
	if err := scheduling.Apply(t.Context(), slotCommand(host, 2, 1)); err != nil || store.value != 1 {
		t.Fatal("task slots could not be lowered after active work finished")
	}
}

func TestTaskSlotChangesShareTheRequestBudgetAndRejectConcurrentStaleWrites(t *testing.T) {
	t.Run("request budget", func(t *testing.T) {
		repository, core := openHostRepository(t), newControlledCore()
		dependencies := hostDependencies(repository, core, &countedGuard{})
		dependencies.Policy.MaxConcurrency = 1
		host := newTestHost(t, dependencies)
		store := &taskSlotStore{value: 1}
		scheduling, _ := jobhost.NewScheduling(host, store)
		first, second := testSubmission(t), testSubmission(t)
		_, _, _ = host.Submit(t.Context(), &first)
		enteredExecution(t, core)
		_, _, _ = host.Submit(t.Context(), &second)
		if err := scheduling.Apply(t.Context(), slotCommand(host, 1, 4)); err != nil {
			t.Fatal(err)
		}
		if status := host.Status(); status.Active != 1 || status.Queued != 1 {
			t.Fatal("task slots bypassed the shared request budget")
		}
		close(core.release)
	})
	t.Run("concurrent clients", func(t *testing.T) {
		host := newTestHost(t, hostDependencies(openHostRepository(t), newControlledCore(), &countedGuard{}))
		store := &taskSlotStore{value: 1}
		scheduling, _ := jobhost.NewScheduling(host, store)
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, limit := range []uint32{2, 4} {
			command := slotCommand(host, 1, limit)
			workers.Go(func() { results <- scheduling.Apply(t.Context(), command) })
		}
		workers.Wait()
		close(results)
		var succeeded, conflicted int
		for err := range results {
			if err == nil {
				succeeded++
			} else if errors.Is(err, jobhost.ErrConflict) {
				conflicted++
			} else {
				t.Fatal(err)
			}
		}
		if succeeded != 1 || conflicted != 1 || store.calls != 1 {
			t.Fatal("stale clients overwrote each other")
		}
	})
}
