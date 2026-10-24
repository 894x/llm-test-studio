package remotenodes

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remoteclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type schedulingRemote struct {
	*remoteFixture
	apply func(context.Context, Binding, *credentials.Lease, uint32, uint32) (jobcontract.RunnerInfo, error)
}

func (remote *schedulingRemote) SetTaskConcurrency(
	ctx context.Context,
	binding Binding,
	token *credentials.Lease,
	expected uint32,
	limit uint32,
) (jobcontract.RunnerInfo, error) {
	return remote.apply(ctx, binding, token, expected, limit)
}

func TestManagerAppliesTaskConcurrencyAndReconcilesLostResponse(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "lost response"}[lost], func(t *testing.T) {
			fixture := newNodeFixture(t)
			node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
			info := *readyObservation(node, fixture.now).Runner
			calls := 0
			remote := &schedulingRemote{remoteFixture: &remoteFixture{}}
			remote.bound = func(context.Context, Binding, *credentials.Lease) (jobcontract.RunnerInfo, error) {
				return info, nil
			}
			remote.apply = func(_ context.Context, binding Binding, token *credentials.Lease, expected, limit uint32) (jobcontract.RunnerInfo, error) {
				calls++
				if binding.Identity != node.Binding.Identity || expected != 1 || limit != 4 {
					t.Fatal("configuration target or observed limit changed")
				}
				if _, err := token.Bytes(); err != nil {
					t.Fatal("node authorization unavailable")
				}
				info.Budget.MaxActiveJobs = limit
				if lost {
					return jobcontract.RunnerInfo{}, remoteclient.ErrConnection
				}
				return info, nil
			}
			manager, err := NewManager(t.Context(), ManagerDependencies{
				Configuration: fixture.service,
				Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
					return remote, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			command := TaskConcurrencyCommand{ID: node.ID, ExpectedRevision: node.Revision,
				ExpectedMaxActiveJobs: 1, MaxActiveJobs: 4}
			err = manager.SetTaskConcurrency(t.Context(), command)
			if lost && !errors.Is(err, ErrCommitUnconfirmed) || !lost && err != nil {
				t.Fatalf("unexpected confirmation: %v", err)
			}
			current, err := manager.node(t.Context(), node.ID, node.Revision)
			if err != nil || current.Observation == nil || current.Observation.Runner.Budget.MaxActiveJobs != 4 {
				t.Fatal("authoritative setting was not observed")
			}
			if calls != 1 || remote.closed.Load() != 1 {
				t.Fatal("write was repeated or temporary SSH session was retained")
			}
			command.ExpectedRevision++
			if err := manager.SetTaskConcurrency(t.Context(), command); !errors.Is(err, ErrConflict) || calls != 1 {
				t.Fatal("stale node configuration issued a remote write")
			}
		})
	}
}

func TestManagerDoesNotWriteThroughAnUnsupportedSession(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	remote := &remoteFixture{}
	manager := managerFixture(t, fixture, remote)
	err := manager.SetTaskConcurrency(t.Context(), TaskConcurrencyCommand{
		ID: node.ID, ExpectedRevision: node.Revision, ExpectedMaxActiveJobs: 1, MaxActiveJobs: 2,
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal("unsupported scheduling session was not rejected")
	}
	current, err := manager.node(t.Context(), node.ID, node.Revision)
	if err != nil || current.Observation != nil {
		t.Fatal("unsupported session invented a saved runner setting")
	}
}

func TestTaskConcurrencyCannotBeOverwrittenByAnEarlierHealthCheck(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	previous := *readyObservation(node, fixture.now).Runner
	started, release := make(chan struct{}), make(chan struct{})
	remote := &schedulingRemote{remoteFixture: &remoteFixture{}}
	remote.bound = func(ctx context.Context, _ Binding, _ *credentials.Lease) (jobcontract.RunnerInfo, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return previous, nil
	}
	remote.apply = func(context.Context, Binding, *credentials.Lease, uint32, uint32) (jobcontract.RunnerInfo, error) {
		actual := previous.Clone()
		actual.Budget.MaxActiveJobs = 4
		return actual, nil
	}
	manager, err := NewManager(t.Context(), ManagerDependencies{
		Configuration: fixture.service,
		Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
			return remote, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	checked := make(chan error, 1)
	go func() { checked <- manager.Check(t.Context(), node.ID, node.Revision) }()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("health check did not start")
	}
	if err := manager.SetTaskConcurrency(t.Context(), TaskConcurrencyCommand{
		ID: node.ID, ExpectedRevision: node.Revision, ExpectedMaxActiveJobs: 1, MaxActiveJobs: 4,
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-checked
	current, err := manager.node(t.Context(), node.ID, node.Revision)
	if err != nil || current.Observation.Runner.Budget.MaxActiveJobs != 4 {
		t.Fatal("an earlier health check replaced the confirmed task limit")
	}
}
