package bridge

import (
	"context"
	"errors"
	"fmt"

	native "github.com/894x/llm-test-studio/apps/desktop/internal/native"
)

type reportExportSaver func(context.Context, native.ExportFile) (bool, error)

type reportPNGClipboardWriter func(context.Context, []byte) error

func (app *DesktopApp) SaveReportExport(filename, mediaType, dataBase64, locale string) (bool, error) {
	exported, err := native.ParseReportExport(filename, mediaType, dataBase64, locale)
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
	exported, err := native.ParseReportExport("llm-test-studio-report.png", "image/png", dataBase64, "zh-CN")
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
	if err := copyPNG(lease.ctx, exported.Data); err != nil {
		return app.safeBindingError(fmt.Errorf("copy desktop report PNG: %w", err))
	}
	return nil
}
