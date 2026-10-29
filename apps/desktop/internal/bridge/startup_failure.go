package bridge

import "errors"

// StartupFailure carries only a bounded recovery category across the UI boundary.
// Cause remains available to the existing startup diagnostic reporter.
type StartupFailure struct {
	Code  string
	Cause error
}

func (failure *StartupFailure) Error() string { return failure.Cause.Error() }
func (failure *StartupFailure) Unwrap() error { return failure.Cause }

func startupFailureCode(err error, progress StartupProgress) string {
	var failure *StartupFailure
	if errors.As(err, &failure) {
		switch failure.Code {
		case "workspace_busy", "permission_denied", "disk_full", "storage_unavailable",
			"database_unsupported", "database_corrupt":
			return failure.Code
		}
	}
	upgrading := progress.FromVersion > 0 && progress.FromVersion < progress.ToVersion
	switch {
	case progress.Stage == "database_backing_up":
		return "backup_failed"
	case progress.Stage == "database_migrating":
		return "migration_failed"
	case progress.Stage == "database_validating" && upgrading:
		return "migration_failed"
	default:
		return "unknown"
	}
}
