package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxReportExportBytes = 128 << 20

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

type reportExportFile struct {
	filename  string
	mediaType string
	data      []byte
	spec      reportExportSpec
}

type reportExportSpec struct {
	extension   string
	displayName string
	pattern     string
}

type reportExportSaver func(context.Context, reportExportFile) (bool, error)
type reportPNGClipboardWriter func([]byte) error

var reportExportSpecs = map[string]reportExportSpec{
	"application/json":         {extension: ".json", displayName: "JSON 报告 (*.json)", pattern: "*.json"},
	"text/html; charset=utf-8": {extension: ".html", displayName: "HTML 报告 (*.html)", pattern: "*.html"},
	"image/png":                {extension: ".png", displayName: "PNG 图片 (*.png)", pattern: "*.png"},
	"application/pdf":          {extension: ".pdf", displayName: "PDF 报告 (*.pdf)", pattern: "*.pdf"},
}

func (app *DesktopApp) SaveReportExport(filename, mediaType, dataBase64 string) (bool, error) {
	exported, err := parseReportExport(filename, mediaType, dataBase64)
	if err != nil {
		return false, app.safeBindingError(fmt.Errorf("validate report export: %w", err))
	}
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return false, app.safeBindingError(err)
	}
	defer lease.release()

	app.mu.Lock()
	save := app.saveReportExport
	app.mu.Unlock()
	if save == nil {
		return false, app.safeBindingError(errors.New("report export saver is unavailable"))
	}
	saved, err := save(lease.ctx, exported)
	if err != nil {
		return false, app.safeBindingError(fmt.Errorf("save desktop report export: %w", err))
	}
	return saved, nil
}

func (app *DesktopApp) CopyReportPNG(dataBase64 string) error {
	exported, err := parseReportExport("llm-studio-report.png", "image/png", dataBase64)
	if err != nil {
		return app.safeBindingError(errors.New("invalid PNG report export"))
	}
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()

	app.mu.Lock()
	copyPNG := app.copyReportPNG
	app.mu.Unlock()
	if copyPNG == nil {
		return app.safeBindingError(errors.New("PNG clipboard writer is unavailable"))
	}
	if err := copyPNG(exported.data); err != nil {
		return app.safeBindingError(fmt.Errorf("copy desktop report PNG: %w", err))
	}
	return nil
}

func parseReportExport(filename, mediaType, dataBase64 string) (reportExportFile, error) {
	spec, ok := reportExportSpecs[mediaType]
	if !ok {
		return reportExportFile{}, errors.New("unsupported report export media type")
	}
	if filename == "" || len(filename) > 255 || filepath.Base(filename) != filename ||
		!strings.EqualFold(filepath.Ext(filename), spec.extension) {
		return reportExportFile{}, errors.New("invalid report export filename")
	}
	if dataBase64 == "" || base64.StdEncoding.DecodedLen(len(dataBase64)) > maxReportExportBytes {
		return reportExportFile{}, errors.New("invalid report export payload size")
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil || len(data) == 0 || len(data) > maxReportExportBytes {
		return reportExportFile{}, errors.New("invalid report export payload")
	}
	if mediaType == "image/png" && !hasPNGSignature(data) {
		return reportExportFile{}, errors.New("invalid PNG report export payload")
	}
	return reportExportFile{filename: filename, mediaType: mediaType, data: data, spec: spec}, nil
}

func saveReportExportToFile(ctx context.Context, exported reportExportFile) (bool, error) {
	path, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:                "保存测试报告",
		DefaultFilename:      exported.filename,
		CanCreateDirectories: true,
		Filters: []wailsruntime.FileFilter{{
			DisplayName: exported.spec.displayName,
			Pattern:     exported.spec.pattern,
		}},
	})
	if err != nil {
		return false, err
	}
	if path == "" {
		return false, nil
	}
	if filepath.Ext(path) == "" {
		path += exported.spec.extension
	}
	if err := os.WriteFile(path, exported.data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func hasPNGSignature(data []byte) bool {
	if len(data) < len(pngSignature) {
		return false
	}
	for index, expected := range pngSignature {
		if data[index] != expected {
			return false
		}
	}
	return true
}
