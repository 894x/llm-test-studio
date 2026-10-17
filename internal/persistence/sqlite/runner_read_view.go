package sqlite

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// OpenReadOnlyView isolates history reads from execution's connection.
// Both roles use the same current repository implementation and validation.
// The owner must stop readers and close this view before closing writer.
func (repository *Repository) OpenReadOnlyView(ctx context.Context) (*Repository, error) {
	missingRepository := repository == nil || repository.db == nil
	if ctx == nil || missingRepository {
		return nil, errors.New("read view requires an open repository and context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	writer, err := repository.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire read view owner: %w", err)
	}
	defer writer.Close()
	var filename string
	pathQuery := `SELECT file FROM pragma_database_list WHERE name = 'main'`
	if err := writer.QueryRowContext(ctx, pathQuery).Scan(&filename); err != nil {
		return nil, fmt.Errorf("read view database path: %w", err)
	}
	if filename == "" || !filepath.IsAbs(filename) {
		return nil, errors.New("read view requires a file-backed database with an absolute path")
	}
	var busyMilliseconds int64
	if err := writer.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyMilliseconds); err != nil {
		return nil, fmt.Errorf("read view owner busy timeout: %w", err)
	}
	var journal string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journal); err != nil {
		return nil, fmt.Errorf("enable WAL for read view: %w", err)
	}
	if journal != "wal" {
		return nil, fmt.Errorf("read view requires WAL, got %q", journal)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("release read view owner: %w", err)
	}
	dsn, err := readOnlyViewDSN(filename, time.Duration(busyMilliseconds)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	view, err := openRepository(ctx, dsn, time.Duration(busyMilliseconds)*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("open read view: %w", err)
	}
	ready := false
	defer func() {
		if !ready {
			_ = view.Close()
		}
	}()
	connection, err := view.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire read view connection: %w", err)
	}
	defer connection.Close()
	var queryOnly, foreignKeys, configuredBusy int64
	for _, setting := range []struct {
		query string
		value *int64
	}{
		{query: "PRAGMA query_only", value: &queryOnly},
		{query: "PRAGMA foreign_keys", value: &foreignKeys},
		{query: "PRAGMA busy_timeout", value: &configuredBusy},
	} {
		if err := connection.QueryRowContext(ctx, setting.query).Scan(setting.value); err != nil {
			return nil, fmt.Errorf("verify read view setting: %w", err)
		}
	}
	if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return nil, fmt.Errorf("verify read view WAL: %w", err)
	}
	invalidPragmas := queryOnly != 1 || foreignKeys != 1 || configuredBusy != busyMilliseconds
	if invalidPragmas || journal != "wal" {
		return nil, errors.New("read view connection settings could not be established")
	}
	if err := connection.Close(); err != nil {
		return nil, fmt.Errorf("release read view connection: %w", err)
	}
	ready = true
	return view, nil
}

func readOnlyViewDSN(filename string, busyTimeout time.Duration) (string, error) {
	path := filepath.ToSlash(filename)
	if filepath.VolumeName(filename) != "" {
		path = "/" + path
	}
	location := url.URL{Scheme: "file", Path: path}
	dsn, err := repositoryPoolDSN(location.String(), busyTimeout)
	if err != nil {
		return "", err
	}
	base, rawQuery, _ := strings.Cut(dsn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("parse read view connection settings: %w", err)
	}
	query.Set("mode", "ro")
	query.Set("_txlock", "deferred")
	query["_pragma"] = append(query["_pragma"], "query_only(1)")
	return base + "?" + query.Encode(), nil
}
