package credentials

import "context"

// Store persists secrets outside application databases and serialized domain
// objects. Callers own every input byte slice and every returned Lease.
type Store interface {
	Set(context.Context, StoreRef, []byte) error
	Replace(context.Context, StoreRef, []byte) error
	Get(context.Context, StoreRef) (*Lease, error)
	Delete(context.Context, StoreRef) error
	Test(context.Context, StoreRef) error
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
