package integration

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

type unusedRemoteArtifactSource struct{ calls atomic.Uint32 }

func (source *unusedRemoteArtifactSource) Fetch(context.Context, string, string) (io.ReadCloser, error) {
	source.calls.Add(1)
	return nil, runnerartifacts.ErrUnavailable
}
