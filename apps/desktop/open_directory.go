package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const directoryOpenerTimeout = 5 * time.Second
const directoryOpenerInitialWait = 250 * time.Millisecond

func openDirectory(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("inspect directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("diagnostic path is not a directory")
	}
	if err := openDirectoryPlatform(absolute); err != nil {
		return fmt.Errorf("open directory with system shell: %w", err)
	}
	return nil
}

func runDirectoryOpener(command string, timeout time.Duration, arguments ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := exec.CommandContext(ctx, command, arguments...).Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("directory opener timed out: %w", ctx.Err())
		}
		return err
	}
	return nil
}

func startDirectoryOpener(command string, initialWait time.Duration, arguments ...string) error {
	process := exec.Command(command, arguments...)
	if err := process.Start(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	timer := time.NewTimer(initialWait)
	defer timer.Stop()
	select {
	case err := <-finished:
		return err
	case <-timer.C:
		return nil
	}
}
