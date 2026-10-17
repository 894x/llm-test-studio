package cache

import "golang.org/x/sys/windows"

func publishFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

func confirmDirectory(string) error {
	// Windows publication uses the existing write-through atomic replacement.
	// Directory fsync is unsupported; complete files are flushed again on reuse.
	return nil
}
