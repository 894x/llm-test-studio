// Package casecatalog owns the shareable filesystem case catalog. Built-in
// cases and executable-relative user cases use the same group/case layout.
package casecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	ID            string                    `json:"id"`
	Revision      uint64                    `json:"revision"`
	Group         string                    `json:"group"`
	Directory     string                    `json:"directory"`
	Key           string                    `json:"key"`
	Name          string                    `json:"name"`
	Dimension     string                    `json:"dimension"`
	Protocol      domain.Protocol           `json:"protocol"`
	ModelTargets  []string                  `json:"model_targets"`
	Enabled       bool                      `json:"enabled"`
	Default       bool                      `json:"default"`
	Severity      domain.CaseSeverity       `json:"severity"`
	ExecutionMode domain.CaseExecutionMode  `json:"execution_mode"`
	Source        Source                    `json:"source"`
	Definition    domain.TestCaseDefinition `json:"definition"`
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
			Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension, Protocol: testCase.Protocol,
			ModelTargets: append([]string{}, testCase.ModelTargets...),
			Enabled:      testCase.Enabled, Default: testCase.Default, Severity: testCase.Severity,
			ExecutionMode: testCase.ExecutionMode, Source: entry.Source, Definition: testCase.Definition,
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
		builtin[key] = value
	}
	discoveredEntries := make([]discovered, 0, len(builtin))
	identities := make(map[string]string, len(builtin))
	for path, entry := range builtin {
		identity := string(entry.testCase.Protocol) + "/" + entry.testCase.Key
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

// FindRevision resolves the immutable Case revision pinned by a Plan. The
// active case.json remains the authored document; prior semantic revisions are
// retained as sidecars so updating a Case cannot invalidate an existing Plan.
func (service *Service) FindRevision(ctx context.Context, id string, revision uint64) (Entry, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) || revision == 0 {
		return Entry{}, ErrInvalid
	}
	entry, err := service.Find(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.TestCase.Revision == revision {
		return entry, nil
	}
	target := service.revisionPath(entry.Group, entry.Directory, revision)
	if !withinRoot(service.userRoot, target) {
		return Entry{}, ErrInvalid
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return Entry{}, err
	}
	testCase, err := decodeStoredCaseRevision(raw)
	if err != nil || testCase.ID != id || testCase.Revision != revision ||
		testCase.Protocol != entry.TestCase.Protocol || testCase.Key != entry.TestCase.Key {
		return Entry{}, ErrInvalid
	}
	return Entry{Group: entry.Group, Directory: entry.Directory, Source: SourceUser, TestCase: testCase}, nil
}

// StoreRevision materializes an exact Case revision without changing the
// active case.json. Persisting the current revision before a Plan commit keeps
// that pin resolvable even when a user later edits case.json directly.
func (service *Service) StoreRevision(ctx context.Context, testCase domain.TestCase) error {
	if service == nil || ctx == nil || testCase.Validate() != nil {
		return ErrInvalid
	}
	entry, err := service.Find(ctx, testCase.ID)
	if err != nil {
		return err
	}
	if entry.TestCase.Protocol != testCase.Protocol || entry.TestCase.Key != testCase.Key {
		return ErrInvalid
	}
	if entry.TestCase.Revision == testCase.Revision {
		if !equalStoredCaseRevision(entry.TestCase, testCase) {
			return ErrCollision
		}
		raw, encodeErr := encodeStoredCaseRevision(testCase)
		if encodeErr != nil {
			return ErrInvalid
		}
		return service.storeRevision(ctx, entry.Group, entry.Directory, testCase, raw)
	}
	raw, err := encodeStoredCaseRevision(testCase)
	if err != nil {
		return ErrInvalid
	}
	return service.storeRevision(ctx, entry.Group, entry.Directory, testCase, raw)
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
	protocol := domain.Protocol(group)
	if err := protocol.Validate(); err != nil {
		return ErrInvalid
	}
	sourcePath := group + "/" + directory + "/case.json"
	testCase, err := casecodec.DecodeFilesystemCase(sourcePath, raw)
	if err != nil || testCase.Protocol != protocol {
		return ErrInvalid
	}
	current, findErr := service.Find(ctx, testCase.ID)
	if findErr == nil {
		if current.Group != group || current.Directory != directory {
			return ErrCollision
		}
		if current.TestCase.Revision != testCase.Revision {
			previous, encodeErr := encodeStoredCaseRevision(current.TestCase)
			if encodeErr != nil {
				return ErrInvalid
			}
			if err := service.storeRevision(ctx, group, directory, current.TestCase, previous); err != nil {
				return err
			}
		}
	} else if !errors.Is(findErr, fs.ErrNotExist) {
		return findErr
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

func (service *Service) storeRevision(ctx context.Context, group, directory string, testCase domain.TestCase, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := service.revisionPath(group, directory, testCase.Revision)
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	if existing, err := os.ReadFile(target); err == nil {
		stored, decodeErr := decodeStoredCaseRevision(existing)
		if decodeErr != nil || !equalStoredCaseRevision(stored, testCase) {
			return ErrCollision
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect case revision: %w", err)
	}
	if err := fileconfig.WriteAtomically(ctx, target, raw); err != nil {
		return fmt.Errorf("write case revision: %w", err)
	}
	return nil
}

func (service *Service) revisionPath(group, directory string, revision uint64) string {
	return filepath.Join(service.userRoot, group, directory, "revisions", strconv.FormatUint(revision, 10)+".json")
}

func encodeStoredCaseRevision(testCase domain.TestCase) ([]byte, error) {
	if err := testCase.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(testCase, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func decodeStoredCaseRevision(raw []byte) (domain.TestCase, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var testCase domain.TestCase
	if err := decoder.Decode(&testCase); err != nil {
		return domain.TestCase{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return domain.TestCase{}, errors.New("case revision must contain one object")
		}
		return domain.TestCase{}, err
	}
	if err := testCase.Validate(); err != nil {
		return domain.TestCase{}, err
	}
	return testCase, nil
}

func equalStoredCaseRevision(left, right domain.TestCase) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
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
		if err != nil || string(testCase.Protocol) != parts[0] {
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
