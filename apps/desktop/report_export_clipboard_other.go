//go:build !darwin && !windows

package main

import (
	"context"
	"errors"
)

func writePNGToClipboard(context.Context, []byte) error {
	return errors.New("native PNG clipboard is not supported on this platform")
}
