//go:build darwin

package main

/*
#cgo LDFLAGS: -framework AppKit
int llm_test_studio_copy_png_to_clipboard(const void *bytes, long length);
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"
)

func writePNGToClipboard(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("PNG clipboard payload is empty")
	}
	if C.llm_test_studio_copy_png_to_clipboard(unsafe.Pointer(&data[0]), C.long(len(data))) == 0 {
		return errors.New("macOS rejected the PNG clipboard payload")
	}
	return nil
}
