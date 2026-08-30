package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/894x/llm-test/internal/application/workspace"
	"github.com/894x/llm-test/internal/persistence/sqlite"
)

var desktopApplicationVersion = "dev"

type productionOptions struct {
	userConfigDir func() (string, error)
	appVersion    string
}

func defaultProductionOptions() productionOptions {
	return productionOptions{
		userConfigDir: os.UserConfigDir,
		appVersion:    desktopApplicationVersion,
	}
}

func newProductionInitializer(options productionOptions) desktopInitializer {
	return func(ctx context.Context) (desktopDependencies, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if options.userConfigDir == nil {
			return desktopDependencies{}, errors.New("user configuration directory provider is unavailable")
		}
		if strings.TrimSpace(options.appVersion) == "" {
			return desktopDependencies{}, errors.New("desktop application version is required")
		}
		configurationRoot, err := options.userConfigDir()
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate user configuration directory: %w", err)
		}
		directory, database, err := productionStoragePaths(configurationRoot)
		if err != nil {
			return desktopDependencies{}, err
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return desktopDependencies{}, fmt.Errorf("create desktop data directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return desktopDependencies{}, fmt.Errorf("secure desktop data directory: %w", err)
		}
		if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{AppVersion: options.appVersion}); err != nil {
			return desktopDependencies{}, fmt.Errorf("migrate desktop database: %w", err)
		}
		repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("open desktop repository: %w", err)
		}
		query := workspace.New(repository)
		return desktopDependencies{
			query: query,
			close: repository.Close,
		}, nil
	}
}

func productionStoragePaths(configurationRoot string) (directory string, database string, err error) {
	if configurationRoot == "" || configurationRoot != strings.TrimSpace(configurationRoot) {
		return "", "", errors.New("user configuration directory must be a non-blank absolute path")
	}
	root := filepath.Clean(configurationRoot)
	if !filepath.IsAbs(root) {
		return "", "", errors.New("user configuration directory must be an absolute path")
	}
	directory = filepath.Join(root, "llm-test")
	database = filepath.Join(directory, "llm-test.db")
	relative, err := filepath.Rel(root, database)
	if err != nil {
		return "", "", fmt.Errorf("validate desktop database path: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("desktop database path escapes user configuration directory")
	}
	return directory, database, nil
}
