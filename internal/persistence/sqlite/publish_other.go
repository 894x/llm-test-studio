//go:build !windows && !linux && !darwin

package sqlite

func atomicPublish(sourcePath, destinationPath string) error {
	return publishWithHardLink(sourcePath, destinationPath)
}
