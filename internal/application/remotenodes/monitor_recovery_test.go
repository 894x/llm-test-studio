package remotenodes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/fileconfig"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func TestManagerMonitorResumesAfterCurrentConfigurationReadFailure(t *testing.T) {
	fixture := newNodeFixture(t)
	_ = fixture.save(t, passwordCommand(t))
	var connections atomic.Int64
	manager, err := NewManager(t.Context(), ManagerDependencies{Configuration: fixture.service,
		Connect: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
			connections.Add(1)
			return nil, remotessh.ErrConnection
		}})
	if err != nil {
		t.Fatal(err)
	}
	manager.monitorEvery = 5 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- manager.Run() }()
	t.Cleanup(func() { _ = manager.Close() })
	waitMonitor(t, func() bool { return connections.Load() > 0 })
	var original []byte
	rejected := []byte(`{"format":999}`)
	if err := fileconfig.WithExclusiveLock(t.Context(), fixture.store.path+".lock", func() error {
		original, err = os.ReadFile(fixture.store.path)
		if err != nil {
			return err
		}
		return fileconfig.WriteAtomically(t.Context(), fixture.store.path, rejected)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Snapshot(t.Context()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unsupported configuration stopped being explicit")
	}
	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-done:
		t.Fatalf("configuration read failure terminated the monitor: %v", err)
	case <-timer.C:
	}
	retained, err := os.ReadFile(fixture.store.path)
	if err != nil || !bytes.Equal(retained, rejected) {
		t.Fatal("monitor modified rejected configuration")
	}
	before := connections.Load()
	if err := fileconfig.WithExclusiveLock(t.Context(), fixture.store.path+".lock", func() error {
		return fileconfig.WriteAtomically(t.Context(), fixture.store.path, original)
	}); err != nil {
		t.Fatal(err)
	}
	waitMonitor(t, func() bool { return connections.Load() > before })
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func waitMonitor(t *testing.T, predicate func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for !predicate() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("monitor did not resume")
		case <-timer.C:
		}
	}
}
