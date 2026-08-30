package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/894x/llm-test/internal/application/workspace"
)

func TestProductionStoragePathsStayWithinInjectedConfigurationRoot(t *testing.T) {
	configurationRoot := t.TempDir()
	directory, database, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatalf("productionStoragePaths() error = %v", err)
	}
	wantDirectory := filepath.Join(configurationRoot, "llm-test")
	wantDatabase := filepath.Join(wantDirectory, "llm-test.db")
	if directory != wantDirectory || database != wantDatabase {
		t.Fatalf("storage paths = (%q, %q), want (%q, %q)", directory, database, wantDirectory, wantDatabase)
	}
	relative, err := filepath.Rel(configurationRoot, database)
	if err != nil {
		t.Fatalf("relative database path: %v", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		t.Fatalf("database path escaped injected configuration root: %q", relative)
	}
}

func TestProductionStoragePathsRejectUnsafeRoots(t *testing.T) {
	for _, root := range []string{"", "   ", "relative-config"} {
		t.Run(root, func(t *testing.T) {
			if _, _, err := productionStoragePaths(root); err == nil {
				t.Fatalf("productionStoragePaths(%q) error = nil", root)
			}
		})
	}
}

func TestProductionInitializerMigratesAndOpensWorkspaceOnlyUnderInjectedRoot(t *testing.T) {
	configurationRoot := t.TempDir()
	configurationCalls := 0
	initialize := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) {
			configurationCalls++
			return configurationRoot, nil
		},
		appVersion: "desktop-test",
	})

	dependencies, err := initialize(context.Background())
	if err != nil {
		t.Fatalf("production initializer error = %v", err)
	}
	if dependencies.close == nil {
		t.Fatal("production initializer did not return repository closer")
	}
	defer func() {
		if err := dependencies.close(); err != nil {
			t.Errorf("close production dependencies: %v", err)
		}
	}()
	if configurationCalls != 1 {
		t.Fatalf("configuration directory calls = %d, want 1", configurationCalls)
	}
	if isNilInterface(dependencies.query) {
		t.Fatal("production initializer did not return workspace query")
	}
	if !isNilInterface(dependencies.commands) {
		t.Fatal("production initializer invented run commands")
	}

	directory := filepath.Join(configurationRoot, "llm-test")
	database := filepath.Join(directory, "llm-test.db")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("stat production database: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat production directory: %v", err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o700 {
			t.Fatalf("production directory permissions = %#o, want 0700", permissions)
		}
	}
	snapshot, err := dependencies.query.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("query initialized workspace: %v", err)
	}
	if snapshot.SchemaVersion != workspace.CurrentSchemaVersion || len(snapshot.Plans) != 0 || len(snapshot.Runs) != 0 {
		t.Fatalf("initialized workspace = %+v, want empty schema v%d", snapshot, workspace.CurrentSchemaVersion)
	}
}

func TestProductionInitializationFailureRemainsVisibleThroughBinding(t *testing.T) {
	configurationFailure := errors.New("configuration directory unavailable")
	app := newDesktopApp(newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return "", configurationFailure },
		appVersion:    "desktop-test",
	}))
	var reported error
	app.setErrorReporter(func(err error) { reported = err })

	app.onStartup(context.Background())
	_, err := app.GetWorkspace()

	assertBindingErrorCode(t, err, "desktop_startup_failed")
	if !errors.Is(reported, configurationFailure) {
		t.Fatalf("locally reported error = %v, want configuration failure", reported)
	}
}
