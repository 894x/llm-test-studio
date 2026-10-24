package bridge

import (
	"context"
	"testing"
)

func TestDesktopRemoteNodeBindingLifecycle(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) { return Dependencies{}, nil })
	if _, err := app.GetRemoteNodes(); err == nil || err.Error() != desktopCodeNotStarted {
		t.Fatal("remote node query bypassed startup")
	}
	if err := app.SetRemoteNodeTaskConcurrency(RemoteNodeTaskConcurrencyCommand{}); err == nil ||
		err.Error() != desktopCodeNotStarted {
		t.Fatal("remote task settings bypassed startup")
	}
	app.onStartup(t.Context())
	defer app.shutdown()
	if _, err := app.GetRemoteNodes(); err == nil || err.Error() != desktopCodeRemoteNodesMissing {
		t.Fatal("missing remote node manager was not explicit")
	}
	if err := app.SetRemoteNodeTaskConcurrency(RemoteNodeTaskConcurrencyCommand{}); err == nil ||
		err.Error() != desktopCodeRemoteNodesMissing {
		t.Fatal("task settings accepted an absent node manager")
	}
	if err := app.SelectDefaultExecution(DefaultExecutionCommand{}); err == nil ||
		err.Error() != DesktopCodeRemoteNodeInvalid {
		t.Fatal("missing location was silently treated as local")
	}
}
