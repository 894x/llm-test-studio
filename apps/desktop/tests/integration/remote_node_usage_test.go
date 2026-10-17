package integration

import (
	"context"
)

type unusedRemoteNodeUsage struct{ err error }

func (reader unusedRemoteNodeUsage) RemoteNodeRemovalBlocked(context.Context, string, string, string) (bool, error) {
	return false, reader.err
}
