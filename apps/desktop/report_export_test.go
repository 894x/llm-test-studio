package main

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

func TestDesktopAppSavesAndCopiesRenderedReportExports(t *testing.T) {
	type contextKey struct{}
	lifecycleContext := context.WithValue(context.Background(), contextKey{}, "desktop")
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	app.onStartup(lifecycleContext)

	var savedFile reportExportFile
	app.saveReportExport = func(ctx context.Context, exported reportExportFile) (bool, error) {
		if ctx.Value(contextKey{}) != "desktop" {
			t.Fatal("save did not receive the desktop lifecycle context")
		}
		savedFile = exported
		return true, nil
	}
	var copiedPNG []byte
	app.copyReportPNG = func(data []byte) error {
		copiedPNG = append([]byte(nil), data...)
		return nil
	}

	saved, err := app.SaveReportExport("llm-studio-report-1.png", "image/png", validPNGBase64())
	if err != nil || !saved {
		t.Fatalf("SaveReportExport() = %v, %v; want true, nil", saved, err)
	}
	if savedFile.filename != "llm-studio-report-1.png" || savedFile.mediaType != "image/png" || !hasPNGSignature(savedFile.data) {
		t.Fatalf("saved export = %#v", savedFile)
	}

	if err := app.CopyReportPNG(validPNGBase64()); err != nil {
		t.Fatalf("CopyReportPNG() error = %v", err)
	}
	if !hasPNGSignature(copiedPNG) {
		t.Fatalf("copied PNG = %x", copiedPNG)
	}
}

func TestDesktopAppTreatsCancelledReportSaveAsSuccess(t *testing.T) {
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	app.onStartup(context.Background())
	app.saveReportExport = func(context.Context, reportExportFile) (bool, error) {
		return false, nil
	}

	saved, err := app.SaveReportExport("report.json", "application/json", base64.StdEncoding.EncodeToString([]byte("{}")))
	if err != nil || saved {
		t.Fatalf("SaveReportExport() = %v, %v; want false, nil", saved, err)
	}
}

func TestDesktopAppRejectsInvalidReportExportPayloads(t *testing.T) {
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	app.onStartup(context.Background())

	tests := []struct {
		name      string
		filename  string
		mediaType string
		data      string
	}{
		{name: "directory", filename: "../report.png", mediaType: "image/png", data: validPNGBase64()},
		{name: "extension", filename: "report.json", mediaType: "image/png", data: validPNGBase64()},
		{name: "media type", filename: "report.txt", mediaType: "text/plain", data: "eA=="},
		{name: "base64", filename: "report.png", mediaType: "image/png", data: "%%%"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := app.SaveReportExport(test.filename, test.mediaType, test.data); !errors.As(err, new(DesktopBindingError)) {
				t.Fatalf("SaveReportExport() error = %v; want DesktopBindingError", err)
			}
		})
	}

	invalidPNG := base64.StdEncoding.EncodeToString([]byte("not a png"))
	if err := app.CopyReportPNG(invalidPNG); !errors.As(err, new(DesktopBindingError)) {
		t.Fatalf("CopyReportPNG() error = %v; want DesktopBindingError", err)
	}
}

func TestDesktopAppReportsNativeExportFailures(t *testing.T) {
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	app.onStartup(context.Background())
	app.saveReportExport = func(context.Context, reportExportFile) (bool, error) {
		return false, errors.New("save failed")
	}
	app.copyReportPNG = func([]byte) error { return errors.New("copy failed") }

	if _, err := app.SaveReportExport("report.png", "image/png", validPNGBase64()); !errors.As(err, new(DesktopBindingError)) {
		t.Fatalf("SaveReportExport() error = %v; want DesktopBindingError", err)
	}
	if err := app.CopyReportPNG(validPNGBase64()); !errors.As(err, new(DesktopBindingError)) {
		t.Fatalf("CopyReportPNG() error = %v; want DesktopBindingError", err)
	}
}

func validPNGBase64() string {
	return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
}
