//go:build !windows

package bootstrap

func platformDiskFull(error) bool { return false }
