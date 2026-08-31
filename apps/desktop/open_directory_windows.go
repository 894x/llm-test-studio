//go:build windows

package main

func openDirectoryPlatform(path string) error {
	return runDirectoryOpener("explorer.exe", directoryOpenerTimeout, path)
}
