//go:build windows

package fileconfig

func publishPrivateDirectory(temporary, path string) error {
	// Publish the directory with the same write-through platform primitive as
	// configuration files. This runs before any keyring value can be written.
	return replaceFile(temporary, path)
}

func syncPrivateDirectory(string) error {
	// Windows does not support fsync on directory handles. Creation uses the
	// write-through rename above; later config replacements also write through.
	return nil
}
