package bootstrap

import (
	"errors"
	"os"
	"syscall"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	sqlitedriver "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

func classifyStartupFailure(err error) error {
	code := "unknown"
	var sqliteError *sqlitedriver.Error
	if errors.As(err, &sqliteError) {
		// Extended SQLite codes retain the primary category in the low byte.
		switch sqliteError.Code() & 0xff {
		case sqlitelib.SQLITE_BUSY, sqlitelib.SQLITE_LOCKED:
			code = "workspace_busy"
		case sqlitelib.SQLITE_PERM, sqlitelib.SQLITE_READONLY, sqlitelib.SQLITE_AUTH:
			code = "permission_denied"
		case sqlitelib.SQLITE_FULL:
			code = "disk_full"
		case sqlitelib.SQLITE_CANTOPEN, sqlitelib.SQLITE_IOERR:
			code = "storage_unavailable"
		case sqlitelib.SQLITE_CORRUPT, sqlitelib.SQLITE_NOTADB:
			code = "database_corrupt"
		}
	}
	switch {
	case errors.Is(err, fileconfig.ErrLockBusy):
		code = "workspace_busy"
	case errors.Is(err, sqlite.ErrSchemaResetRequired):
		code = "database_unsupported"
	case errors.Is(err, os.ErrPermission):
		code = "permission_denied"
	case errors.Is(err, syscall.ENOSPC), platformDiskFull(err):
		code = "disk_full"
	case errors.Is(err, os.ErrNotExist):
		code = "storage_unavailable"
	}
	return &bridge.StartupFailure{Code: code, Cause: err}
}
