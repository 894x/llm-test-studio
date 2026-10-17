//go:build !darwin && !windows

package clipboard

import (
	"context"
	"errors"
)

func WritePNG(context.Context, []byte) error {
	return errors.New("native PNG clipboard is not supported on this platform")
}
