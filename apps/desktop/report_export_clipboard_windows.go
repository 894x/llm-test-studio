//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clipboardDIBV5Format = 17
	clipboardDIBV5Header = 124
)

var (
	clipboardUser32         = windows.NewLazySystemDLL("user32.dll")
	clipboardKernel32       = windows.NewLazySystemDLL("kernel32.dll")
	clipboardCreateWindow   = clipboardUser32.NewProc("CreateWindowExW")
	clipboardDestroyWindow  = clipboardUser32.NewProc("DestroyWindow")
	clipboardRegisterFormat = clipboardUser32.NewProc("RegisterClipboardFormatW")
	clipboardOpen           = clipboardUser32.NewProc("OpenClipboard")
	clipboardClose          = clipboardUser32.NewProc("CloseClipboard")
	clipboardEmpty          = clipboardUser32.NewProc("EmptyClipboard")
	clipboardSetData        = clipboardUser32.NewProc("SetClipboardData")
	clipboardGlobalAlloc    = clipboardKernel32.NewProc("GlobalAlloc")
	clipboardGlobalFree     = clipboardKernel32.NewProc("GlobalFree")
	clipboardGlobalLock     = clipboardKernel32.NewProc("GlobalLock")
	clipboardGlobalUnlock   = clipboardKernel32.NewProc("GlobalUnlock")
	clipboardMoveMemory     = clipboardKernel32.NewProc("RtlMoveMemory")
	clipboardSetLastError   = clipboardKernel32.NewProc("SetLastError")
)

func writePNGToClipboard(ctx context.Context, data []byte) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dib, err := reportPNGToDIBV5(data)
	if err != nil {
		return err
	}

	// Windows requires clipboard access and window cleanup on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		return err
	}
	// A message-only window gives this process a valid clipboard owner even if
	// focus moved to another app while the report image was rendering.
	owner, _, err := clipboardCreateWindow.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		0,
		0,
		0,
		0,
		0,
		^uintptr(2), // HWND_MESSAGE
		0,
		0,
		0,
	)
	if owner == 0 {
		return fmt.Errorf("create PNG clipboard owner: %w", err)
	}
	defer clipboardDestroyWindow.Call(owner)
	formatName, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		return err
	}
	pngFormat, _, err := clipboardRegisterFormat.Call(uintptr(unsafe.Pointer(formatName)))
	if pngFormat == 0 {
		return fmt.Errorf("register PNG clipboard format: %w", err)
	}
	pngMemory, err := allocateClipboardMemory(data)
	if err != nil {
		return err
	}
	defer func() {
		if pngMemory != 0 {
			clipboardGlobalFree.Call(pngMemory)
		}
	}()
	dibMemory, err := allocateClipboardMemory(dib)
	if err != nil {
		return err
	}
	defer func() {
		if dibMemory != 0 {
			clipboardGlobalFree.Call(dibMemory)
		}
	}()
	if err := openPNGClipboard(ctx, owner); err != nil {
		return err
	}
	defer func() {
		if closed, _, err := clipboardClose.Call(); closed == 0 {
			resultErr = errors.Join(resultErr, fmt.Errorf("close PNG clipboard: %w", err))
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if emptied, _, err := clipboardEmpty.Call(); emptied == 0 {
		return fmt.Errorf("empty PNG clipboard: %w", err)
	}
	if copied, _, err := clipboardSetData.Call(pngFormat, pngMemory); copied == 0 {
		return fmt.Errorf("write PNG clipboard data: %w", err)
	}
	pngMemory = 0 // Windows owns successfully published memory.
	if copied, _, err := clipboardSetData.Call(clipboardDIBV5Format, dibMemory); copied == 0 {
		return fmt.Errorf("write bitmap clipboard data: %w", err)
	}
	dibMemory = 0
	return nil
}

func openPNGClipboard(ctx context.Context, owner uintptr) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		opened, _, err := clipboardOpen.Call(owner)
		if opened != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("open PNG clipboard: %w", err)
		case <-ticker.C:
		}
	}
}

func allocateClipboardMemory(data []byte) (uintptr, error) {
	const gmemMoveable = 0x0002
	memory, _, err := clipboardGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if memory == 0 {
		return 0, fmt.Errorf("allocate PNG clipboard memory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			clipboardGlobalFree.Call(memory)
		}
	}()
	pointer, _, err := clipboardGlobalLock.Call(memory)
	if pointer == 0 {
		return 0, fmt.Errorf("lock PNG clipboard memory: %w", err)
	}
	clipboardMoveMemory.Call(pointer, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(data)
	clipboardSetLastError.Call(0)
	if unlocked, _, err := clipboardGlobalUnlock.Call(memory); unlocked == 0 && err != windows.ERROR_SUCCESS {
		return 0, fmt.Errorf("unlock PNG clipboard memory: %w", err)
	}
	keep = true
	return memory, nil
}

func reportPNGToDIBV5(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxReportExportBytes {
		return nil, errors.New("invalid PNG clipboard payload size")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode PNG clipboard dimensions: %w", err)
	}
	size, err := reportDIBV5Size(config.Width, config.Height)
	if err != nil {
		return nil, err
	}
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode PNG clipboard image: %w", err)
	}
	dib := make([]byte, size)
	binary.LittleEndian.PutUint32(dib[0:4], clipboardDIBV5Header)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(config.Width))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(config.Height))
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[16:20], 3) // BI_BITFIELDS
	binary.LittleEndian.PutUint32(dib[20:24], uint32(size-clipboardDIBV5Header))
	binary.LittleEndian.PutUint32(dib[40:44], 0x00ff0000)
	binary.LittleEndian.PutUint32(dib[44:48], 0x0000ff00)
	binary.LittleEndian.PutUint32(dib[48:52], 0x000000ff)
	binary.LittleEndian.PutUint32(dib[52:56], 0xff000000)
	binary.LittleEndian.PutUint32(dib[56:60], 0x73524742) // LCS_sRGB
	binary.LittleEndian.PutUint32(dib[108:112], 4)        // LCS_GM_IMAGES
	for y := range config.Height {
		row := clipboardDIBV5Header + (config.Height-1-y)*config.Width*4
		for x := range config.Width {
			pixel := color.NRGBAModel.Convert(image.At(x, y)).(color.NRGBA)
			offset := row + x*4
			dib[offset], dib[offset+1], dib[offset+2], dib[offset+3] = pixel.B, pixel.G, pixel.R, pixel.A
		}
	}
	return dib, nil
}

func reportDIBV5Size(width, height int) (int, error) {
	const maxPixels = (maxReportExportBytes - clipboardDIBV5Header) / 4
	invalidDimensions := width <= 0 || height <= 0
	if invalidDimensions || width > maxPixels/height {
		return 0, errors.New("PNG clipboard image exceeds the 128 MiB bitmap limit")
	}
	return clipboardDIBV5Header + width*height*4, nil
}
