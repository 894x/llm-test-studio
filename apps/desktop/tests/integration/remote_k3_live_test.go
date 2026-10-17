package integration

import (
	"context"
	"testing"
	"time"

	bootstrap "github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
)

func startLiveK3App(t *testing.T, ctx context.Context, options bootstrap.Options) *bridge.DesktopApp {
	t.Helper()
	app := bridge.New(bootstrap.NewInitializer(options))
	bridge.Startup(app, ctx)
	if _, err := app.GetRemoteNodes(); err != nil {
		_ = bridge.Shutdown(app)
		t.Fatal("cannot start the isolated production desktop")
	}
	return app
}

func liveK3Pause(t *testing.T, ctx context.Context, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("live acceptance deadline reached; accepted jobs were neither cancelled nor resubmitted")
	case <-timer.C:
	}
}
