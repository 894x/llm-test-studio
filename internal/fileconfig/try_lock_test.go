package fileconfig

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestTryExclusiveLockDoesNotEnterAnotherOwnersOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.lock")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(finish)
	result := make(chan error, 1)
	go func() {
		result <- WithExclusiveLock(context.Background(), path, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	called := false
	err := TryWithExclusiveLock(context.Background(), path, func() error { called = true; return nil })
	if !errors.Is(err, ErrLockBusy) || called {
		t.Fatal("nonblocking contender entered the held operation")
	}
	finish()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := TryWithExclusiveLock(context.Background(), path, func() error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("released operation lock was not reusable")
	}
}
