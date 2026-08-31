//go:build !windows && !darwin

package main

func openDirectoryPlatform(path string) error {
	return startDirectoryOpener("xdg-open", directoryOpenerInitialWait, path)
}
