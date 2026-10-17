package native

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const MaxReportExportBytes = 128 << 20

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

type ExportFile struct {
	Filename  string
	MediaType string
	Data      []byte
	Locale    string
	spec      reportExportSpec
}

type reportExportSpec struct {
	extension    string
	displayNames map[string]string
	pattern      string
}

var reportExportSpecs = map[string]reportExportSpec{
	"application/json":         {extension: ".json", displayNames: map[string]string{"zh-CN": "JSON 报告 (*.json)", "en-US": "JSON report (*.json)"}, pattern: "*.json"},
	"text/html; charset=utf-8": {extension: ".html", displayNames: map[string]string{"zh-CN": "HTML 报告 (*.html)", "en-US": "HTML report (*.html)"}, pattern: "*.html"},
	"image/png":                {extension: ".png", displayNames: map[string]string{"zh-CN": "PNG 图片 (*.png)", "en-US": "PNG image (*.png)"}, pattern: "*.png"},
	"application/pdf":          {extension: ".pdf", displayNames: map[string]string{"zh-CN": "PDF 报告 (*.pdf)", "en-US": "PDF report (*.pdf)"}, pattern: "*.pdf"},
}

func ParseReportExport(filename, mediaType, dataBase64, locale string) (ExportFile, error) {
	if locale != "zh-CN" && locale != "en-US" {
		return ExportFile{}, errors.New("unsupported report export locale")
	}
	spec, ok := reportExportSpecs[mediaType]
	if !ok {
		return ExportFile{}, errors.New("unsupported report export media type")
	}
	if filename == "" || len(filename) > 255 || filepath.Base(filename) != filename ||
		!strings.EqualFold(filepath.Ext(filename), spec.extension) {
		return ExportFile{}, errors.New("invalid report export filename")
	}
	if dataBase64 == "" || base64.StdEncoding.DecodedLen(len(dataBase64)) > MaxReportExportBytes {
		return ExportFile{}, errors.New("invalid report export payload size")
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil || len(data) == 0 || len(data) > MaxReportExportBytes {
		return ExportFile{}, errors.New("invalid report export payload")
	}
	if mediaType == "image/png" && !HasPNGSignature(data) {
		return ExportFile{}, errors.New("invalid PNG report export payload")
	}
	return ExportFile{Filename: filename, MediaType: mediaType, Data: data, Locale: locale, spec: spec}, nil
}

func SaveReportExport(ctx context.Context, exported ExportFile) (bool, error) {
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
	if err := os.WriteFile(path, exported.Data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func reportExportDialogOptions(exported ExportFile) wailsruntime.SaveDialogOptions {
	title := "保存测试报告"
	if exported.Locale == "en-US" {
		title = "Save test report"
	}
	return wailsruntime.SaveDialogOptions{
		Title:                title,
		DefaultFilename:      exported.Filename,
		CanCreateDirectories: true,
		Filters: []wailsruntime.FileFilter{{
			DisplayName: exported.spec.displayNames[exported.Locale],
			Pattern:     exported.spec.pattern,
		}},
	}
}

func HasPNGSignature(data []byte) bool {
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
