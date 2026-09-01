//go:build !darwin

package main

import "errors"

func writePNGToClipboard([]byte) error {
	return errors.New("native PNG clipboard is not supported on this platform")
}
