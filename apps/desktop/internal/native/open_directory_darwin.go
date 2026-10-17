//go:build darwin

package native

func openDirectoryPlatform(path string) error {
	return runDirectoryOpener("open", directoryOpenerTimeout, path)
}
