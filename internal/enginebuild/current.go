// Package enginebuild identifies the executable that owns provider execution.
package enginebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
)

func Current() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		// proc still opens the running inode after an installer replaces its
		// pathname. Windows keeps its running executable locked in place.
		path = "/proc/self/exe"
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
