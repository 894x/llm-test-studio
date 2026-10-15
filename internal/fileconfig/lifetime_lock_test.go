package fileconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLifetimeLockSurvivesAcquisitionCancellationUntilOwnerCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "execution-owner.lock")
	ctx, cancel := context.WithCancel(t.Context())
	lock, err := TryAcquireExclusiveLock(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	cancel()
	entered := false
	err = TryWithExclusiveLock(t.Context(), path, func() error { entered = true; return nil })
	if !errors.Is(err, ErrLockBusy) || entered {
		t.Fatal("canceling acquisition released a still-live execution owner")
	}
	for range 2 {
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("closing ownership removed the shared lock inode")
	}
	if err := TryWithExclusiveLock(t.Context(), path, func() error { entered = true; return nil }); err != nil || !entered {
		t.Fatalf("next owner could not acquire the retained lock: %v", err)
	}
}

func TestLifetimeLockRejectsCanceledOrInvalidAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if lock, err := TryAcquireExclusiveLock(ctx, filepath.Join(t.TempDir(), "owner.lock")); !errors.Is(err, context.Canceled) || lock != nil {
		t.Fatal("canceled acquisition returned ownership")
	}
	if lock, err := TryAcquireExclusiveLock(t.Context(), "relative.lock"); err == nil || lock != nil {
		t.Fatal("relative execution lock was accepted")
	}
	var lock *ExclusiveLock
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&ExclusiveLock{}).Close(); err != nil {
		t.Fatal(err)
	}
}
