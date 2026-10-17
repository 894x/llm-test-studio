package jobcontent

import (
	"context"
	"errors"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type resourceState uint8

const (
	resourcePreparing resourceState = iota
	resourcePrepared
	resourceBound
	resourceClosing
	resourceClosed
)

type anchor struct {
	claim Claim
	pin   PinnedContent
}

type preparedResources struct{ resource *resources }

func (prepared *preparedResources) Claims() []Claim { return prepared.resource.Claims() }
func (prepared *preparedResources) Permit() *Permit { return prepared.resource.Permit() }
func (prepared *preparedResources) BindJob(jobID string) (JobResources, error) {
	return prepared.resource.BindJob(jobID)
}

func (prepared *preparedResources) Close() error {
	resource := prepared.resource
	resource.manager.mu.Lock()
	bound := resource.state == resourceBound
	if !bound && resource.state != resourceClosed {
		resource.state = resourceClosing
	}
	resource.manager.mu.Unlock()
	if bound {
		return nil
	}
	return resource.Close()
}

// resources has a single owner. BindJob transfers that owner to execution;
// neither the original submission context nor a copied Claim owns its files.
type resources struct {
	manager    *Manager
	ctx        context.Context
	submission context.Context
	cancel     context.CancelFunc
	jobID      string
	digest     string
	state      resourceState // guarded by manager.mu
	anchors    []anchor      // immutable after ready closes
	ready      chan struct{}
	permit     *Permit
	stopSubmit func() bool
	submitDone chan struct{}
	submitOnce sync.Once
	borrowers  map[*assetStream]struct{} // guarded by manager.mu
	opening    sync.WaitGroup
	borrowed   sync.WaitGroup
	callbacks  sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
}

func (manager *Manager) Prepare(
	ctx context.Context,
	spec jobcontract.JobSpec,
) (result PreparedResources, prepareErr error) {
	operation, end, err := manager.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	if err := manager.physical.Ready(operation); err != nil {
		return nil, err
	}
	frozen, err := spec.Freeze()
	if err != nil {
		return nil, err
	}
	if frozen.NodeInstallationID != manager.store.InstallationID {
		return nil, ErrConflict
	}
	digest, err := frozen.Digest()
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if manager.closed || len(manager.resources) >= manager.limits.Resources {
		closed := manager.closed
		manager.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		return nil, ErrLimit
	}
	lifetime, cancel := context.WithCancel(manager.ctx)
	resource := &resources{
		manager: manager, ctx: lifetime, submission: ctx, cancel: cancel,
		jobID: frozen.JobID, digest: digest,
		ready: make(chan struct{}), submitDone: make(chan struct{}),
		anchors: make([]anchor, 0, len(frozen.Assets)), borrowers: make(map[*assetStream]struct{}),
	}
	resource.permit = &Permit{owner: manager, resource: resource}
	// Publish the cancellation callback while holding the state mutex. A callback
	// that starts immediately cannot observe a half-initialized resource.
	resource.stopSubmit = context.AfterFunc(ctx, func() {
		defer close(resource.submitDone)
		manager.mu.Lock()
		bound := resource.state == resourceBound
		if !bound {
			resource.state = resourceClosing
		}
		manager.mu.Unlock()
		if !bound {
			_ = resource.closeInternal()
		}
	})
	manager.resources[resource] = struct{}{}
	manager.mu.Unlock()
	prepared := false
	defer func() {
		close(resource.ready)
		if !prepared {
			prepareErr = errors.Join(prepareErr, resource.Close())
		}
	}()
	for _, asset := range frozen.Assets {
		identity := jobcontract.ContentIdentity{SHA256: asset.SHA256, Size: asset.Size}
		if err := manager.prepareAsset(resource, identity, asset); err != nil {
			return nil, err
		}
	}
	manager.mu.Lock()
	if manager.closed || resource.state != resourcePreparing {
		manager.mu.Unlock()
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	if err := operation.Err(); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	resource.state = resourcePrepared
	prepared = true
	manager.mu.Unlock()
	return &preparedResources{resource: resource}, nil
}

func (manager *Manager) prepareAsset(
	resource *resources,
	identity jobcontract.ContentIdentity,
	asset jobcontract.Asset,
) error {
	if err := resource.ctx.Err(); err != nil {
		return err
	}
	if err := manager.retain(identity, false); err != nil {
		return err
	}
	retained := false
	defer func() {
		if !retained {
			manager.release(identity, false)
		}
	}()
	unlock, err := manager.work(resource.ctx, identity)
	if err != nil {
		return err
	}
	defer unlock()
	pin, err := manager.physical.OpenPinned(resource.ctx, identity)
	if err != nil {
		return err
	}
	if _, err := manager.publish(resource.ctx, pin, identity); err != nil {
		return errors.Join(err, pin.Close())
	}
	resource.anchors = append(resource.anchors, anchor{
		claim: Claim{Ref: asset.Ref, Object: pin.Object(), MediaType: asset.MediaType}, pin: pin,
	})
	retained = true
	return nil
}

func (resource *resources) Claims() []Claim {
	<-resource.ready
	claims := make([]Claim, len(resource.anchors))
	for index, anchor := range resource.anchors {
		claims[index] = anchor.claim
	}
	return claims
}

func (resource *resources) Permit() *Permit { return resource.permit }

// ValidatePermit is deliberately lightweight. Prepare performed the physical
// verification; this validates its still-live private ownership without IO.
// File reads and new borrows additionally verify the current physical identity.
func (manager *Manager) ValidatePermit(permit *Permit, expectedSpecSHA256 string) ([]Claim, error) {
	if permit == nil || !jobcontract.IsSHA256(expectedSpecSHA256) {
		return nil, ErrInvalid
	}
	if permit.owner != manager || permit.resource == nil {
		return nil, ErrInvalid
	}
	resource := permit.resource
	manager.mu.Lock()
	_, exists := manager.resources[resource]
	liveState := resource.state == resourcePrepared || resource.state == resourceBound
	validSubmission := resource.state == resourceBound || resource.submission.Err() == nil
	validLifetime := !manager.closed && exists && liveState && validSubmission
	validBinding := permit == resource.permit && resource.ctx.Err() == nil && resource.digest == expectedSpecSHA256
	manager.mu.Unlock()
	if !validLifetime || !validBinding {
		return nil, ErrInvalid
	}
	return resource.Claims(), nil
}

func (resource *resources) BindJob(jobID string) (JobResources, error) {
	if !domain.IsUUID(jobID) || jobID != resource.jobID {
		return nil, ErrInvalid
	}
	manager := resource.manager
	manager.mu.Lock()
	validState := resource.state == resourcePrepared && resource.ctx.Err() == nil
	if manager.closed || !validState {
		manager.mu.Unlock()
		return nil, ErrClosed
	}
	if err := resource.submission.Err(); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	// The submission callback uses this same mutex; once bound, cancellation can
	// no longer close execution resources. A callback already closing wins first.
	resource.state = resourceBound
	manager.mu.Unlock()
	resource.joinSubmission()
	return resource, nil
}

func (resource *resources) Resolver() AssetResolver { return resource }

func (resource *resources) joinSubmission() {
	resource.submitOnce.Do(func() {
		if resource.stopSubmit() {
			close(resource.submitDone)
		} else {
			<-resource.submitDone
		}
	})
}

func (resource *resources) Close() error {
	resource.joinSubmission()
	return resource.closeInternal()
}

func (resource *resources) closeInternal() error {
	resource.closeOnce.Do(func() {
		manager := resource.manager
		manager.mu.Lock()
		resource.state = resourceClosing
		manager.mu.Unlock()
		resource.cancel()
		<-resource.ready
		// Closing the gate precedes Wait: Open cannot add another in-flight open.
		resource.opening.Wait()
		manager.mu.Lock()
		borrowers := make([]*assetStream, 0, len(resource.borrowers))
		for borrower := range resource.borrowers {
			borrowers = append(borrowers, borrower)
		}
		manager.mu.Unlock()
		for _, borrower := range borrowers {
			resource.closeErr = errors.Join(resource.closeErr, borrower.Close())
		}
		resource.borrowed.Wait()
		resource.callbacks.Wait()
		for _, anchor := range resource.anchors {
			resource.closeErr = errors.Join(resource.closeErr, anchor.pin.Close())
			manager.release(anchor.claim.Object.Content, false)
		}
		manager.mu.Lock()
		resource.state = resourceClosed
		delete(manager.resources, resource)
		manager.mu.Unlock()
	})
	return resource.closeErr
}

var _ AdmissionResources = (*Manager)(nil)
var _ PreparedResources = (*preparedResources)(nil)
var _ JobResources = (*resources)(nil)
