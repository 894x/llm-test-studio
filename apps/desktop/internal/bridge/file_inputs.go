package bridge

import (
	"github.com/894x/llm-test-studio/internal/application/jobfiles"
)

const (
	desktopCodeFileInputInvalid     = "file_input_invalid"
	desktopCodeFileInputTooLarge    = "file_input_too_large"
	desktopCodeFileInputLimit       = "file_input_limit"
	desktopCodeFileInputBusy        = "file_input_busy"
	desktopCodeFileInputNotFound    = "file_input_not_found"
	desktopCodeFileInputUnavailable = "file_input_unavailable"
)

// CreateLocalFileSelection receives a bounded current DTO, not a local path.
// The document boundary lets Core reject duplicate and unknown JSON fields.
func (app *DesktopApp) CreateLocalFileSelection(document string) (jobfiles.Selection, error) {
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return jobfiles.Selection{}, app.safeBindingError(err)
	}
	defer lease.release()
	app.mu.Lock()
	selections := app.fileSelections
	app.mu.Unlock()
	if selections == nil {
		return jobfiles.Selection{}, app.safeBindingError(jobfiles.ErrClosed)
	}
	selection, err := selections.Import(lease.ctx, document)
	if err != nil {
		return jobfiles.Selection{}, app.safeBindingError(err)
	}
	return selection, nil
}

func (app *DesktopApp) ReleaseLocalFileSelection(selectionID string) error {
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	app.mu.Lock()
	selections := app.fileSelections
	app.mu.Unlock()
	if selections == nil {
		return app.safeBindingError(jobfiles.ErrClosed)
	}
	if err := selections.Release(selectionID); err != nil {
		return app.safeBindingError(err)
	}
	return nil
}
