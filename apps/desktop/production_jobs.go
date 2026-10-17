package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type productionJobBackends struct {
	configuration *remotenodes.Service
	connect       runnerConnector
	local         func(context.Context) (jobs.Backend, error)
}

func (backends productionJobBackends) Open(
	ctx context.Context,
	location jobs.Location,
	access jobs.Access,
) (jobs.Backend, error) {
	validAccess := access == jobs.SubmissionAccess || access == jobs.ObservationAccess
	if ctx == nil || location.Validate() != nil || !validAccess {
		return nil, jobs.ErrInvalid
	}
	if location.Kind == jobcontract.TargetLocal {
		if backends.local == nil {
			return nil, jobs.ErrUnavailable
		}
		return backends.local(ctx)
	}
	if backends.configuration == nil {
		return nil, jobs.ErrUnavailable
	}
	snapshot, err := backends.configuration.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, node := range snapshot.Nodes {
		if node.ID != location.ConfigurationID {
			continue
		}
		if access == jobs.SubmissionAccess && !node.Enabled {
			return nil, jobs.ErrUnavailable
		}
		origin := remoteresults.Origin{ConfigurationID: node.ID,
			NodeID: node.RunnerNodeID, InstallationID: node.InstallationID}
		paired := node.HostKey != nil && origin.Validate() == nil
		if !paired || !installationResolved(node.Installation) {
			return nil, jobs.ErrUnavailable
		}
		connect := backends.connect
		if connect == nil {
			connect = openProductionRunner
		}
		return connect(
			ctx,
			backends.configuration,
			remotesync.Node{Origin: origin, Revision: node.Revision},
			access,
		)
	}
	return nil, jobs.ErrUnavailable
}

type productionJobRepository interface {
	jobs.SubmissionRepository
	jobs.ControlRepository
	jobs.JobReader
}

type productionJobServices struct {
	coordinator   *jobs.Coordinator
	controller    *jobs.Controller
	confirmations *jobs.ConfirmationWatcher
	deliveries    *jobs.ControlDeliveryWatcher
	close         func() error
}

func newProductionJobs(
	ctx context.Context,
	repository productionJobRepository,
	backends productionJobBackends,
) (productionJobServices, error) {
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{
		Repository: repository, Open: backends.Open, Now: time.Now,
	})
	if err != nil {
		return productionJobServices{}, err
	}
	controller, err := jobs.NewController(jobs.ControllerDependencies{
		Repository: repository, Targets: repository, Open: backends.Open, Now: time.Now,
	})
	if err != nil {
		return productionJobServices{}, err
	}
	watcher, err := jobs.NewConfirmationWatcher(ctx, jobs.ConfirmationDependencies{
		Repository: repository, Reconcile: coordinator.Reconcile,
	})
	if err != nil {
		return productionJobServices{}, err
	}
	deliveries, err := jobs.NewControlDeliveryWatcher(ctx, jobs.ControlDeliveryDependencies{
		Repository: repository, Deliver: controller.Deliver,
	})
	if err != nil {
		_ = watcher.Close()
		return productionJobServices{}, err
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	controlsFinished := make(chan error, 1)
	go func() { controlsFinished <- deliveries.Run() }()
	var once sync.Once
	var closeError error
	closeServices := func() error {
		once.Do(func() {
			closeError = deliveries.Close()
			if err := <-controlsFinished; !errors.Is(err, context.Canceled) {
				closeError = errors.Join(closeError, err)
			}
			closeError = errors.Join(closeError, watcher.Close())
			if err := <-finished; !errors.Is(err, context.Canceled) {
				closeError = errors.Join(closeError, err)
			}
		})
		return closeError
	}
	return productionJobServices{coordinator: coordinator, controller: controller,
		confirmations: watcher, deliveries: deliveries, close: closeServices}, nil
}
