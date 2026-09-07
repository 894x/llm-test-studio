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
	locale    string
	spec      reportExportSpec
}

type reportExportSpec struct {
	extension    string
	displayNames map[string]string
	pattern      string
}

type reportExportSaver func(context.Context, reportExportFile) (bool, error)
type reportPNGClipboardWriter func([]byte) error

var reportExportSpecs = map[string]reportExportSpec{
	"application/json":         {extension: ".json", displayNames: map[string]string{"zh-CN": "JSON 报告 (*.json)", "en-US": "JSON report (*.json)"}, pattern: "*.json"},
	"text/html; charset=utf-8": {extension: ".html", displayNames: map[string]string{"zh-CN": "HTML 报告 (*.html)", "en-US": "HTML report (*.html)"}, pattern: "*.html"},
	"image/png":                {extension: ".png", displayNames: map[string]string{"zh-CN": "PNG 图片 (*.png)", "en-US": "PNG image (*.png)"}, pattern: "*.png"},
	"application/pdf":          {extension: ".pdf", displayNames: map[string]string{"zh-CN": "PDF 报告 (*.pdf)", "en-US": "PDF report (*.pdf)"}, pattern: "*.pdf"},
}

func (app *DesktopApp) SaveReportExport(filename, mediaType, dataBase64, locale string) (bool, error) {
	exported, err := parseReportExport(filename, mediaType, dataBase64, locale)
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
	exported, err := parseReportExport("llm-test-studio-report.png", "image/png", dataBase64, "zh-CN")
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

func parseReportExport(filename, mediaType, dataBase64, locale string) (reportExportFile, error) {
	if locale != "zh-CN" && locale != "en-US" {
		return reportExportFile{}, errors.New("unsupported report export locale")
	}
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
	return reportExportFile{filename: filename, mediaType: mediaType, data: data, locale: locale, spec: spec}, nil
}

func saveReportExportToFile(ctx context.Context, exported reportExportFile) (bool, error) {
	path, err := wailsruntime.SaveFileDialog(ctx, reportExportDialogOptions(exported))
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

func reportExportDialogOptions(exported reportExportFile) wailsruntime.SaveDialogOptions {
	title := "保存测试报告"
	if exported.locale == "en-US" {
		title = "Save test report"
	}
	return wailsruntime.SaveDialogOptions{
		Title:                title,
		DefaultFilename:      exported.filename,
		CanCreateDirectories: true,
		Filters: []wailsruntime.FileFilter{{
			DisplayName: exported.spec.displayNames[exported.locale],
			Pattern:     exported.spec.pattern,
		}},
	}
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
