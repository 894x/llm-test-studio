package runnerprocess

import (
	"context"
	"errors"
	"io"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type contentWriter interface {
	PutContent(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error)
}

type contentFaultOwner interface{ MarkUnavailable() }

// contentStore owns the full scratch reservation through final file close.
// Retained scratch growth is also counted by UsedBytes, intentionally favoring
// safe admission over exact headroom while uploads are active.
type contentStore struct {
	state contentWriter
	guard *diskGuard
	host  contentFaultOwner
}

var _ runnerhttp.ContentStore = (*contentStore)(nil)

func (store *contentStore) PutContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.Reader,
) (created bool, err error) {
	invalid := ctx == nil || source == nil || identity.Validate() != nil
	if invalid {
		return false, jobhost.ErrInvalid
	}
	defer func() {
		if err == nil {
			return
		}
		storageFault := errors.Is(err, jobcontract.ErrAssetStorage)
		clientFailure := errors.Is(err, jobcontract.ErrAssetRead) || errors.Is(err, jobcontract.ErrAssetIntegrity)
		cancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
		capacity := errors.Is(err, jobhost.ErrCapacity)
		if storageFault || (!clientFailure && !cancelled && !capacity) {
			err = errors.Join(jobcontract.ErrAssetStorage, err)
			store.host.MarkUnavailable()
		}
	}()
	reservation, err := store.guard.reserveContent(ctx, identity)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, reservation.Close()) }()
	return store.state.PutContent(ctx, identity, source)
}
