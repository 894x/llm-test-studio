package bootstrap

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const databaseSelectionName = "database-selection.json"

var databaseDirectoryName = regexp.MustCompile(`^database-workspace-[0-9]+(-verified)?$`)
var backupDirectoryName = regexp.MustCompile(`^database-recovery-[0-9]+(-verified)?$`)

type databaseSelection struct {
	SchemaVersion     int    `json:"schema_version"`
	DatabaseDirectory string `json:"database_directory"`
	BackupDirectory   string `json:"backup_directory"`
}

// A missing selector is the published default workspace. An invalid selector
// never falls back to that database or creates a replacement silently.
func readDatabaseSelection(directory string) (databaseSelection, bool, error) {
	path := filepath.Join(directory, databaseSelectionName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return databaseSelection{}, false, nil
	}
	if err != nil {
		return databaseSelection{}, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return databaseSelection{}, false, errors.New("invalid desktop database selection file")
	}
	file, err := os.Open(path)
	if err != nil {
		return databaseSelection{}, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return databaseSelection{}, false, errors.New("desktop database selection identity changed")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	var selection databaseSelection
	seen := map[string]bool{}
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return selection, false, errors.New("invalid desktop database selection")
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		invalidToken := err != nil || !ok
		if invalidToken || seen[key] {
			return selection, false, errors.New("duplicate or invalid database selection field")
		}
		seen[key] = true
		switch key {
		case "schema_version":
			err = decoder.Decode(&selection.SchemaVersion)
		case "database_directory":
			err = decoder.Decode(&selection.DatabaseDirectory)
		case "backup_directory":
			err = decoder.Decode(&selection.BackupDirectory)
		default:
			return selection, false, errors.New("unknown database selection field")
		}
		if err != nil {
			return selection, false, errors.New("invalid database selection field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return selection, false, errors.New("incomplete database selection")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return selection, false, errors.New("trailing database selection data")
	}
	validNames := databaseDirectoryName.MatchString(selection.DatabaseDirectory) &&
		backupDirectoryName.MatchString(selection.BackupDirectory)
	validVersionFields := selection.SchemaVersion == 1 && len(seen) == 3
	if !validVersionFields || !validNames {
		return selection, false, errors.New("unsupported or invalid database selection")
	}
	if err := validateRecoveryDirectory(directory, selection.DatabaseDirectory); err != nil {
		return selection, false, err
	}
	return selection, true, nil
}

func validateRecoveryDirectory(root, name string) error {
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("database recovery directory is not a regular directory")
	}
	return nil
}

func resolveDesktopDatabase(directory, defaultPath string) (string, error) {
	selection, exists, err := readDatabaseSelection(directory)
	if err != nil || !exists {
		return defaultPath, err
	}
	path := filepath.Join(directory, selection.DatabaseDirectory, "llm-test-studio.db")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("selected desktop database is not a regular file")
	}
	return path, nil
}
