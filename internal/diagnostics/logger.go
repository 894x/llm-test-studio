// Package diagnostics writes redacted, structured operator diagnostics.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxBytes int64 = 10 << 20
	DefaultBackups        = 5
)

type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type Options struct {
	Directory  string
	Filename   string
	AppVersion string
	MaxBytes   int64
	Backups    int
}

type Event struct {
	Level        Level
	Message      string
	Component    string
	Operation    string
	ErrorCode    string
	RunID        string
	RequestID    string
	ReportID     string
	Duration     time.Duration
	DroppedCount uint64
	FailureCount uint64
	Err          error
}

type Logger struct {
	handler slog.Handler
	sink    *rotatingFile
	owner   *processLock
	path    string
}

type fileOperations struct {
	open   func(string, int, os.FileMode) (*os.File, error)
	remove func(string) error
	rename func(string, string) error
	close  func(*os.File) error
}

var defaultFileOperations = fileOperations{
	open: os.OpenFile, remove: os.Remove, rename: os.Rename,
	close: func(file *os.File) error { return file.Close() },
}

type rotatingFile struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	ops      fileOperations
	size     int64
	maxBytes int64
	backups  int
	closed   bool
}

var secretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(https?://)([^/@\s:]+):([^/@\s]+)@`), `${1}[REDACTED]:[REDACTED]@`},
	{regexp.MustCompile(`(?i)("(?:authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password)"\s*:\s*")([^"]*)(")`), `${1}[REDACTED]${3}`},
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:\[[^\]]*\]|bearer\s+[a-z0-9._~+/=-]+|"[^"]*"|'[^']*'|[^,;\s\]\}]+)`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password)\s*[:=]\s*)"[^"]*"`), `${1}"[REDACTED]"`},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password)\s*[:=]\s*)'[^']*'`), `${1}'[REDACTED]'`},
	{regexp.MustCompile(`(?i)(\bbearer\s+)[a-z0-9._~+/=-]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password)\s*[:=]\s*)[^,;\s\]\}]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)\bsk-[a-z0-9._-]{6,}\b`), `[REDACTED]`},
}

func Open(options Options) (*Logger, error) {
	directory := strings.TrimSpace(options.Directory)
	if directory == "" {
		return nil, fmt.Errorf("diagnostic log directory is required")
	}
	filename := strings.TrimSpace(options.Filename)
	if filename == "" {
		filename = "llm-studio.log"
	}
	if filepath.Base(filename) != filename {
		return nil, fmt.Errorf("diagnostic log filename must not contain a directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create diagnostic log directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure diagnostic log directory: %w", err)
	}
	path := filepath.Join(directory, filename)
	owner, err := acquireProcessLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	backups := options.Backups
	if backups <= 0 {
		backups = DefaultBackups
	}
	sink, err := openRotatingFile(path, maxBytes, backups)
	if err != nil {
		_ = owner.Close()
		return nil, err
	}
	var handler slog.Handler = slog.NewJSONHandler(sink, nil)
	if version := strings.TrimSpace(options.AppVersion); version != "" {
		handler = handler.WithAttrs([]slog.Attr{slog.String("app_version", version)})
	}
	return &Logger{handler: handler, sink: sink, owner: owner, path: path}, nil
}

func (logger *Logger) Record(ctx context.Context, event Event) error {
	if logger == nil || logger.handler == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := strings.TrimSpace(event.Message)
	if message == "" {
		message = "diagnostic event"
	}
	attributes := make([]any, 0, 16)
	attributes = appendString(attributes, "component", event.Component)
	attributes = appendString(attributes, "operation", event.Operation)
	attributes = appendString(attributes, "error_code", event.ErrorCode)
	attributes = appendString(attributes, "run_id", event.RunID)
	attributes = appendString(attributes, "request_id", event.RequestID)
	attributes = appendString(attributes, "report_id", event.ReportID)
	if event.Duration > 0 {
		attributes = append(attributes, "duration_ms", event.Duration.Milliseconds())
	}
	if event.DroppedCount > 0 {
		attributes = append(attributes, "dropped_count", event.DroppedCount)
	}
	if event.FailureCount > 0 {
		attributes = append(attributes, "failure_count", event.FailureCount)
	}
	if event.Err != nil {
		attributes = append(attributes, "error", RedactText(event.Err.Error()))
	}
	record := slog.NewRecord(time.Now(), slogLevel(event.Level), message, 0)
	record.Add(attributes...)
	if err := logger.handler.Handle(ctx, record); err != nil {
		return fmt.Errorf("write diagnostic log: %w", err)
	}
	return nil
}

func (logger *Logger) Path() string {
	if logger == nil {
		return ""
	}
	return logger.path
}

func (logger *Logger) Close() error {
	if logger == nil {
		return nil
	}
	return errors.Join(logger.sink.Close(), logger.owner.Close())
}

func openRotatingFile(path string, maxBytes int64, backups int) (*rotatingFile, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open diagnostic log: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure diagnostic log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect diagnostic log: %w", err)
	}
	return &rotatingFile{path: path, file: file, size: info.Size(), maxBytes: maxBytes, backups: backups, ops: defaultFileOperations}, nil
}

func (file *rotatingFile) Write(contents []byte) (int, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed || file.file == nil {
		if file.closed {
			return 0, os.ErrClosed
		}
		if err := file.reopenActive(); err != nil {
			return 0, fmt.Errorf("recover diagnostic log before write: %w", err)
		}
	}
	var rotationErr error
	if file.size > 0 && file.size+int64(len(contents)) > file.maxBytes {
		rotationErr = file.rotate()
		if file.file == nil {
			return 0, rotationErr
		}
	}
	written, err := file.file.Write(contents)
	file.size += int64(written)
	return written, errors.Join(rotationErr, err)
}

func (file *rotatingFile) Close() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil
	}
	file.closed = true
	if file.file == nil {
		return nil
	}
	return file.file.Close()
}

func (file *rotatingFile) rotate() (resultErr error) {
	active := file.file
	file.file = nil
	activeMoved := false
	defer func() {
		if resultErr == nil {
			return
		}
		if activeMoved {
			if err := file.ops.rename(file.path+".1", file.path); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("restore diagnostic log after rotation failure: %w", err))
			}
		}
		if err := file.reopenActive(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("reopen diagnostic log after rotation failure: %w", err))
		}
	}()
	if err := file.ops.close(active); err != nil {
		_ = active.Close()
		return fmt.Errorf("close diagnostic log for rotation: %w", err)
	}
	oldest := fmt.Sprintf("%s.%d", file.path, file.backups)
	if err := file.ops.remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oldest diagnostic log backup: %w", err)
	}
	for index := file.backups - 1; index >= 1; index-- {
		from := fmt.Sprintf("%s.%d", file.path, index)
		to := fmt.Sprintf("%s.%d", file.path, index+1)
		if err := file.ops.rename(from, to); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate diagnostic log backup %d: %w", index, err)
		}
	}
	if err := file.ops.rename(file.path, file.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate diagnostic log: %w", err)
	}
	activeMoved = true
	active, err := file.ops.open(file.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open rotated diagnostic log: %w", err)
	}
	file.file = active
	file.size = 0
	activeMoved = false
	return nil
}

func (file *rotatingFile) reopenActive() error {
	active, err := file.ops.open(file.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := active.Stat()
	if err != nil {
		_ = active.Close()
		return err
	}
	file.file = active
	file.size = info.Size()
	return nil
}

func appendString(attributes []any, name, value string) []any {
	if value = strings.TrimSpace(value); value != "" {
		return append(attributes, name, value)
	}
	return attributes
}

func slogLevel(level Level) slog.Level {
	switch level {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RedactText removes common credential representations before text reaches any
// diagnostic sink. It deliberately returns a useful stable marker instead of
// dropping the surrounding operator context.
func RedactText(value string) string {
	for _, item := range secretPatterns {
		value = item.pattern.ReplaceAllString(value, item.replacement)
	}
	return value
}
