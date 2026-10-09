// Package casecatalog owns the shareable filesystem case catalog. Built-in
// cases and executable-relative user cases use the same group/case layout.
package casecatalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

var (
	ErrInvalid   = errors.New("case catalog: invalid input")
	ErrCollision = errors.New("case catalog: duplicate case identity")
)

type Options struct {
	Builtin  fs.FS
	UserRoot string
	Now      func() time.Time
}

type Service struct {
	builtin  fs.FS
	userRoot string
}

type Snapshot struct {
	Groups []Group `json:"groups"`
}

type Group struct {
	Name  string `json:"name"`
	Cases []Case `json:"cases"`
}

type Case struct {
	ID            string                     `json:"id"`
	Revision      uint64                     `json:"revision"`
	Group         string                     `json:"group"`
	Directory     string                     `json:"directory"`
	Key           string                     `json:"key"`
	Name          string                     `json:"name"`
	Dimension     string                     `json:"dimension"`
	Enabled       bool                       `json:"enabled"`
	Default       bool                       `json:"default"`
	Severity      domain.CaseSeverity        `json:"severity"`
	ExecutionMode domain.CaseExecutionMode   `json:"execution_mode"`
	Source        Source                     `json:"source"`
	Definitions   domain.ProtocolDefinitions `json:"definitions"`
}

type discovered struct {
	group, directory string
	source           Source
	testCase         domain.TestCase
}

type Entry struct {
	Group     string
	Directory string
	Source    Source
	TestCase  domain.TestCase
}

func New(options Options) (*Service, error) {
	if options.Builtin == nil || strings.TrimSpace(options.UserRoot) == "" || !filepath.IsAbs(options.UserRoot) {
		return nil, ErrInvalid
	}
	return &Service{builtin: options.Builtin, userRoot: filepath.Clean(options.UserRoot)}, nil
}

func UserRootForExecutable(executablePath string) (string, error) {
	if strings.TrimSpace(executablePath) == "" || !filepath.IsAbs(executablePath) {
		return "", ErrInvalid
	}
	executable := filepath.Clean(executablePath)
	directory := filepath.Dir(executable)
	if directory == "." || directory == string(filepath.Separator) {
		return "", ErrInvalid
	}
	return filepath.Join(directory, "data", "cases"), nil
}

func (service *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if service == nil || ctx == nil {
		return Snapshot{}, ErrInvalid
	}
	entries, err := service.Entries(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	groups := make([]Group, 0)
	for _, entry := range entries {
		if len(groups) == 0 || groups[len(groups)-1].Name != entry.Group {
			groups = append(groups, Group{Name: entry.Group, Cases: []Case{}})
		}
		testCase := entry.TestCase
		groups[len(groups)-1].Cases = append(groups[len(groups)-1].Cases, Case{
			ID: testCase.ID, Revision: testCase.Revision, Group: entry.Group, Directory: entry.Directory,
			Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
			Enabled: testCase.Enabled, Default: testCase.Default, Severity: testCase.Severity,
			ExecutionMode: testCase.ExecutionMode, Source: entry.Source, Definitions: testCase.Definitions.Clone(),
		})
	}
	return Snapshot{Groups: groups}, nil
}

func (service *Service) Entries(ctx context.Context) ([]Entry, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	builtin, err := discoverFS(ctx, service.builtin, SourceBuiltin)
	if err != nil {
		return nil, err
	}
	user := map[string]discovered{}
	if _, statErr := os.Stat(service.userRoot); statErr == nil {
		user, err = discoverFS(ctx, os.DirFS(service.userRoot), SourceUser)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect user case root: %w", statErr)
	}
	for key, value := range user {
		if original, exists := builtin[key]; exists && original.testCase.ID != value.testCase.ID {
			return nil, fmt.Errorf("%w: user override %s must preserve the built-in id", ErrCollision, key)
		}
		builtin[key] = value
	}
	discoveredEntries := make([]discovered, 0, len(builtin))
	identities := make(map[string]string, len(builtin))
	for path, entry := range builtin {
		identity := entry.testCase.ID
		if previous, duplicate := identities[identity]; duplicate {
			return nil, fmt.Errorf("%w: %s and %s", ErrCollision, previous, path)
		}
		identities[identity] = path
		discoveredEntries = append(discoveredEntries, entry)
	}
	sort.Slice(discoveredEntries, func(left, right int) bool {
		if discoveredEntries[left].group == discoveredEntries[right].group {
			return discoveredEntries[left].directory < discoveredEntries[right].directory
		}
		return discoveredEntries[left].group < discoveredEntries[right].group
	})
	entries := make([]Entry, len(discoveredEntries))
	for index, entry := range discoveredEntries {
		entries[index] = Entry{Group: entry.group, Directory: entry.directory, Source: entry.source, TestCase: entry.testCase}
	}
	return entries, nil
}

func (service *Service) Find(ctx context.Context, id string) (Entry, error) {
	if !domain.IsUUID(id) {
		return Entry{}, ErrInvalid
	}
	entries, err := service.Entries(ctx)
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range entries {
		if entry.TestCase.ID == id {
			return entry, nil
		}
	}
	return Entry{}, fs.ErrNotExist

}

func (service *Service) SaveCase(ctx context.Context, group, directory string, testCase domain.TestCase) error {
	raw, err := casecodec.EncodeFilesystemCase(testCase)
	if err != nil {
		return ErrInvalid
	}
	return service.Save(ctx, group, directory, raw)

}

func (service *Service) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	entry, err := service.Find(ctx, id)
	if err != nil || entry.Source != SourceUser || entry.TestCase.Revision != expectedRevision {
		return ErrInvalid
	}
	targetDirectory := filepath.Join(service.userRoot, entry.Group, entry.Directory)
	target := filepath.Join(targetDirectory, "case.json")
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return ErrInvalid
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("delete user case: %w", err)
	}
	_ = os.Remove(targetDirectory)
	_ = os.Remove(filepath.Dir(targetDirectory))
	return nil
}

func (service *Service) Save(ctx context.Context, group, directory string, raw []byte) error {
	if service == nil || ctx == nil || !validSegment(group) || !validSegment(directory) || len(raw) == 0 {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sourcePath := group + "/" + directory + "/case.json"
	testCase, err := casecodec.DecodeFilesystemCase(sourcePath, raw)
	if err != nil {
		return ErrInvalid
	}
	entries, err := service.Entries(ctx)
	if err != nil {
		return err
	}
	for _, current := range entries {
		samePath := current.Group == group && current.Directory == directory
		sameID := current.TestCase.ID == testCase.ID
		if samePath != sameID {
			return ErrCollision
		}
	}
	targetDirectory := filepath.Join(service.userRoot, group, directory)
	target := filepath.Join(targetDirectory, "case.json")
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	if err := fileconfig.WriteAtomically(ctx, target, raw); err != nil {
		return fmt.Errorf("install case file: %w", err)
	}
	return nil
}

func discoverFS(ctx context.Context, sourceFS fs.FS, source Source) (map[string]discovered, error) {
	result := make(map[string]discovered)
	err := fs.WalkDir(sourceFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("case catalog must not contain symbolic links")
		}
		if entry.IsDir() || entry.Name() != "case.json" {
			return nil
		}
		parts := strings.Split(path, "/")
		if len(parts) != 3 || !validSegment(parts[0]) || !validSegment(parts[1]) {
			return fmt.Errorf("invalid case catalog path %q", path)
		}
		raw, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			return err
		}
		testCase, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			return fmt.Errorf("invalid case file %q", path)
		}
		key := parts[0] + "/" + parts[1]
		result[key] = discovered{group: parts[0], directory: parts[1], source: source, testCase: testCase}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validSegment(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\:`) && fs.ValidPath(value)
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
