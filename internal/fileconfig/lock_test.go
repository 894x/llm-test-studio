package fileconfig

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWithExclusiveLockSerializesCallersAndHonorsContext(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "models.json.lock")
	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- WithExclusiveLock(context.Background(), lockPath, func() error {
			close(holderEntered)
			<-releaseHolder
			return nil
		})
	}()
	<-holderEntered

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	secondEntered := false
	err := WithExclusiveLock(ctx, lockPath, func() error {
		secondEntered = true
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WithExclusiveLock() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if secondEntered {
		t.Fatal("contending callback entered while the lock was held")
	}

	close(releaseHolder)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if err := WithExclusiveLock(context.Background(), lockPath, func() error { return nil }); err != nil {
		t.Fatalf("WithExclusiveLock() after release: %v", err)
	}
}

func TestWithExclusiveLockIsReleasedWhenHoldingProcessExits(t *testing.T) {
	if os.Getenv("LLM_TEST_FILECONFIG_LOCK_HELPER") == "1" {
		lockPath := os.Getenv("LLM_TEST_FILECONFIG_LOCK_PATH")
		err := WithExclusiveLock(context.Background(), lockPath, func() error {
			if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
				return err
			}
			<-time.After(time.Hour)
			return nil
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	lockPath := filepath.Join(t.TempDir(), "channels.json.lock")
	helperCtx, stopHelper := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopHelper()
	command := exec.CommandContext(helperCtx, os.Args[0], "-test.run=^TestWithExclusiveLockIsReleasedWhenHoldingProcessExits$")
	command.Env = append(
		os.Environ(),
		"LLM_TEST_FILECONFIG_LOCK_HELPER=1",
		"LLM_TEST_FILECONFIG_LOCK_PATH="+lockPath,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("lock helper startup = %q, %v; stderr = %q", line, err, stderr.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = WithExclusiveLock(ctx, lockPath, func() error { return nil })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cross-process lock attempt error = %v, want %v", err, context.DeadlineExceeded)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	reacquireCtx, cancelReacquire := context.WithTimeout(context.Background(), time.Second)
	defer cancelReacquire()
	if err := WithExclusiveLock(reacquireCtx, lockPath, func() error { return nil }); err != nil {
		t.Fatalf("reacquire after lock holder exit: %v", err)
	}
}

func TestIsAtomicTemporaryNameAcceptsOnlyCreateTempNames(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: ".config-0.tmp", want: true},
		{name: ".config-4294967295.tmp", want: true},
		{name: ".config-00.tmp"},
		{name: ".config-4294967296.tmp"},
		{name: ".config-.tmp"},
		{name: ".config-abcd.tmp"},
		{name: ".config-123.json"},
		{name: ".hidden"},
		{name: "plan.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsAtomicTemporaryName(test.name); got != test.want {
				t.Fatalf("IsAtomicTemporaryName(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}
