//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

func TestReportPNGToDIBV5PreservesPixelsAndAlpha(t *testing.T) {
	bitmap := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	bitmap.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 128})
	bitmap.SetNRGBA(1, 0, color.NRGBA{R: 40, G: 50, B: 60, A: 255})
	bitmap.SetNRGBA(0, 1, color.NRGBA{R: 70, G: 80, B: 90, A: 255})
	bitmap.SetNRGBA(1, 1, color.NRGBA{R: 100, G: 110, B: 120, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	dib, err := reportPNGToDIBV5(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct {
		offset int
		want   uint32
	}{
		{offset: 0, want: 124},
		{offset: 4, want: 2},
		{offset: 8, want: 2},
		{offset: 16, want: 3},
		{offset: 20, want: 16},
		{offset: 40, want: 0x00ff0000},
		{offset: 44, want: 0x0000ff00},
		{offset: 48, want: 0x000000ff},
		{offset: 52, want: 0xff000000},
		{offset: 56, want: 0x73524742},
	} {
		if got := binary.LittleEndian.Uint32(dib[field.offset:]); got != field.want {
			t.Errorf("DIB field at %d = %#x, want %#x", field.offset, got, field.want)
		}
	}
	if binary.LittleEndian.Uint16(dib[12:]) != 1 || binary.LittleEndian.Uint16(dib[14:]) != 32 {
		t.Fatal("DIB must have one plane with 32 bits per pixel")
	}
	// Packed clipboard DIBs use bottom-up rows in BGRA byte order.
	want := []byte{90, 80, 70, 255, 120, 110, 100, 255, 30, 20, 10, 128, 60, 50, 40, 255}
	if !bytes.Equal(dib[124:], want) {
		t.Fatalf("DIB pixels = %v, want %v", dib[124:], want)
	}
}

func TestReportPNGToDIBV5RejectsInvalidImages(t *testing.T) {
	valid, err := base64.StdEncoding.DecodeString(validPNGBase64())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("not a PNG"), valid[:len(valid)-12]} {
		if _, err := reportPNGToDIBV5(data); err == nil {
			t.Fatalf("invalid PNG of size %d was accepted", len(data))
		}
	}
	// Reject a compressed image's oversized dimensions before decoding pixels.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 8192, 4096))); err != nil {
		t.Fatal(err)
	}
	if _, err := reportPNGToDIBV5(encoded.Bytes()); err == nil || !strings.Contains(err.Error(), "bitmap limit") {
		t.Fatalf("oversized PNG error = %v", err)
	}
}

func TestReportDIBV5SizeBoundsBeforeAllocation(t *testing.T) {
	maxPixels := (maxReportExportBytes - clipboardDIBV5Header) / 4
	if size, err := reportDIBV5Size(maxPixels, 1); err != nil || size > maxReportExportBytes {
		t.Fatalf("maximum bitmap size = %d, %v", size, err)
	}
	for _, dimensions := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {maxPixels + 1, 1}, {math.MaxInt, math.MaxInt}} {
		if _, err := reportDIBV5Size(dimensions[0], dimensions[1]); err == nil {
			t.Fatalf("invalid bitmap dimensions %v were accepted", dimensions)
		}
	}
}

func TestWindowsPNGClipboardRejectsInvalidOrCancelledInputBeforeOpening(t *testing.T) {
	if err := writePNGToClipboard(context.Background(), []byte("invalid")); err == nil {
		t.Fatal("invalid PNG was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writePNGToClipboard(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled clipboard error = %v", err)
	}
}
