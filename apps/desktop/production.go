package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	casebundle "github.com/894x/llm-studio/cases"
	"github.com/894x/llm-studio/internal/application/caseimport"
	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/workspace"
	"github.com/894x/llm-studio/internal/persistence/sqlite"
)

var desktopApplicationVersion = "dev"

type productionOptions struct {
	userConfigDir       func() (string, error)
	appVersion          string
	caseBundle          fs.FS
	reportCaseConflicts func(int)
}

type productionClock struct{}

func (productionClock) Now() time.Time {
	return time.Now().UTC()
}

func defaultProductionOptions() productionOptions {
	return productionOptions{
		userConfigDir: os.UserConfigDir,
		appVersion:    desktopApplicationVersion,
		caseBundle:    casebundle.Bundle,
		reportCaseConflicts: func(count int) {
			log.Printf("llm-studio: %d built-in case update conflict(s) retained user revisions", count)
		},
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
		caseImporter, err := caseimport.New(caseimport.Dependencies{
			Store: repository,
			Clock: productionClock{},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create built-in case importer: %w", err)
		}
		bundle := options.caseBundle
		if isNilInterface(bundle) {
			bundle = casebundle.Bundle
		}
		importResult, err := caseImporter.Import(ctx, bundle)
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("import built-in cases: %w", err)
		}
		if len(importResult.Conflicts) != 0 && options.reportCaseConflicts != nil {
			options.reportCaseConflicts(len(importResult.Conflicts))
		}
		workspaceQuery := workspace.New(repository)
		catalogQuery, err := catalog.New(catalog.Dependencies{
			Repository: repository,
			Clock:      productionClock{},
			RepositoryErrors: catalog.RepositoryErrorSet{
				NotFound: sqlite.ErrNotFound,
				Conflict: sqlite.ErrConflict,
				Corrupt:  sqlite.ErrCorrupt,
			},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create desktop catalog service: %w", err)
		}
		reportingQuery := reporting.New(repository)
		return desktopDependencies{
			query:   workspaceQuery,
			catalog: catalogQuery,
			reports: reportingQuery,
			close:   repository.Close,
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
	directory = filepath.Join(root, "llm-studio")
	database = filepath.Join(directory, "llm-studio.db")
	relative, err := filepath.Rel(root, database)
	if err != nil {
		return "", "", fmt.Errorf("validate desktop database path: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("desktop database path escapes user configuration directory")
	}
	return directory, database, nil
}
