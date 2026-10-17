//go:build windows

package native

func openDirectoryPlatform(path string) error {
	return runDirectoryOpener("explorer.exe", directoryOpenerTimeout, path)
}
