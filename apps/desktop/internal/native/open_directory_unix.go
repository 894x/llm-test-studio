//go:build !windows && !darwin

package native

func openDirectoryPlatform(path string) error {
	return startDirectoryOpener("xdg-open", directoryOpenerInitialWait, path)
}
