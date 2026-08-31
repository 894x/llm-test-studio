//go:build darwin

package main

func openDirectoryPlatform(path string) error {
	return runDirectoryOpener("open", directoryOpenerTimeout, path)
}
