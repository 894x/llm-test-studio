package main

import (
	"context"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remotenodes"
)

type remoteNodeUsageReader interface {
	RemoteNodeRemovalBlocked(context.Context, string, string, string) (bool, error)
}

func newRemoteNodeRemovalGuard(reader remoteNodeUsageReader) func(context.Context, remotenodes.Node) error {
	return func(ctx context.Context, node remotenodes.Node) error {
		if isNilInterface(reader) {
			return remotenodes.ErrUsageUnknown
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		nodeID, installationID := "", ""
		if node.Binding != nil {
			nodeID, installationID = node.Binding.Identity.NodeID, node.Binding.Identity.InstallationID
		}
		blocked, err := reader.RemoteNodeRemovalBlocked(ctx, node.ID, nodeID, installationID)
		if err != nil {
			return errors.Join(remotenodes.ErrUsageUnknown, err)
		}
		if blocked {
			return remotenodes.ErrReferenced
		}
		return nil
	}
}
