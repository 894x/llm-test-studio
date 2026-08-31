package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (repository *Repository) BuiltinCatalogSeedCompleted(ctx context.Context, seedKey string) (bool, error) {
	if ctx == nil {
		return false, errors.New("inspect built-in catalog seed: context is required")
	}
	seedKey = strings.TrimSpace(seedKey)
	if seedKey == "" {
		return false, errors.New("inspect built-in catalog seed: key is required")
	}
	var completed int
	if err := repository.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM builtin_catalog_seeds WHERE seed_key = ?)`, seedKey).Scan(&completed); err != nil {
		return false, fmt.Errorf("inspect built-in catalog seed: %w", err)
	}
	return completed != 0, nil
}

func (repository *Repository) CompleteBuiltinCatalogSeed(ctx context.Context, seedKey string, completedAt time.Time) error {
	if ctx == nil {
		return errors.New("complete built-in catalog seed: context is required")
	}
	seedKey = strings.TrimSpace(seedKey)
	if seedKey == "" || completedAt.IsZero() {
		return errors.New("complete built-in catalog seed: key and timestamp are required")
	}
	if _, err := repository.conn.ExecContext(ctx, `INSERT OR IGNORE INTO builtin_catalog_seeds(seed_key, completed_at) VALUES(?, ?)`, seedKey, formatTime(completedAt.UTC())); err != nil {
		return classifyWriteError("complete built-in catalog seed", err)
	}
	return nil
}
