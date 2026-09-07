// Package suitecatalog owns the shareable filesystem suite catalog. Built-in
// suites and executable-relative user suites use the same group/suite layout.
package suitecatalog

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	"unicode"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const CurrentSchemaVersion = 1

type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

var (
	ErrInvalid   = errors.New("suite catalog: invalid input")
	ErrCollision = errors.New("suite catalog: duplicate suite identity")
)

type CaseSource interface {
	Entries(context.Context) ([]casecatalog.Entry, error)
}

type Options struct {
	Builtin  fs.FS
	UserRoot string
	Cases    CaseSource
}

type Service struct {
	builtin  fs.FS
	userRoot string
	cases    CaseSource
}

type Entry struct {
	Group     string
	Directory string
	Source    Source
	Suite     domain.Suite
	CaseKeys  []string
}

type document struct {
	SchemaVersion int                    `json:"schema_version"`
	Key           string                 `json:"key"`
	Name          string                 `json:"name"`
	Protocol      domain.Protocol        `json:"protocol"`
	ModelTarget   string                 `json:"model_target"`
	CaseKeys      []string               `json:"case_keys"`
	QuickTest     *domain.SuiteQuickTest `json:"quick_test,omitempty"`
}

type discovered struct {
	group, directory string
	source           Source
	document         document
}

func New(options Options) (*Service, error) {
	if options.Builtin == nil || options.Cases == nil || strings.TrimSpace(options.UserRoot) == "" || !filepath.IsAbs(options.UserRoot) {
		return nil, ErrInvalid
	}
	return &Service{builtin: options.Builtin, userRoot: filepath.Clean(options.UserRoot), cases: options.Cases}, nil
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
	return filepath.Join(directory, "data", "suites"), nil
}

func (service *Service) Entries(ctx context.Context) ([]Entry, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	caseEntries, err := service.cases.Entries(ctx)
	if err != nil {
		return nil, err
	}
	casesByIdentity := make(map[string]domain.TestCase, len(caseEntries))
	for _, entry := range caseEntries {
		identity := string(entry.TestCase.Protocol) + "/" + entry.TestCase.Key
		if _, duplicate := casesByIdentity[identity]; duplicate {
			return nil, fmt.Errorf("%w: duplicate case %s", ErrCollision, identity)
		}
		casesByIdentity[identity] = entry.TestCase
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
		return nil, fmt.Errorf("inspect user suite root: %w", statErr)
	}
	for path, value := range user {
		builtin[path] = value
	}

	discoveredEntries := make([]discovered, 0, len(builtin))
	identities := make(map[string]string, len(builtin))
	for path, entry := range builtin {
		identity := string(entry.document.Protocol) + "/" + entry.document.Key
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
	for index, candidate := range discoveredEntries {
		suite, materializeErr := materialize(candidate.document, casesByIdentity)
		if materializeErr != nil {
			return nil, fmt.Errorf("resolve suite %s/%s: %w", candidate.group, candidate.directory, materializeErr)
		}
		entries[index] = Entry{
			Group: candidate.group, Directory: candidate.directory, Source: candidate.source,
			Suite: suite, CaseKeys: append([]string(nil), candidate.document.CaseKeys...),
		}
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
		if entry.Suite.ID == id {
			return entry, nil
		}
	}
	return Entry{}, fs.ErrNotExist
}

// FindRevision resolves an exact immutable Suite revision. The active
// suite.json remains the authored document; historical revisions live in
// executable-relative sidecars and are never included by Entries.
func (service *Service) FindRevision(ctx context.Context, id string, revision uint64) (Entry, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) || revision == 0 {
		return Entry{}, ErrInvalid
	}
	entry, err := service.Find(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if entry.Suite.Revision == revision {
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
	suite, err := decodeStoredSuiteRevision(raw)
	if err != nil || suite.ID != id || suite.Revision != revision ||
		suite.Protocol != entry.Suite.Protocol || suite.Key != entry.Suite.Key {
		return Entry{}, ErrInvalid
	}
	return Entry{
		Group: entry.Group, Directory: entry.Directory, Source: SourceUser,
		Suite: suite,
	}, nil
}

// StoreRevision persists a complete exact Suite without changing the active
// suite.json. Revision values are content hashes, so only exact equality is
// meaningful; their numeric ordering is deliberately ignored.
func (service *Service) StoreRevision(ctx context.Context, suite domain.Suite) error {
	if service == nil || ctx == nil || suite.Validate() != nil {
		return ErrInvalid
	}
	entry, err := service.Find(ctx, suite.ID)
	if err != nil {
		return err
	}
	if entry.Suite.Protocol != suite.Protocol || entry.Suite.Key != suite.Key {
		return ErrInvalid
	}
	if entry.Suite.Revision == suite.Revision {
		if !equalStoredSuiteRevision(entry.Suite, suite) {
			return ErrCollision
		}
		raw, encodeErr := encodeStoredSuiteRevision(suite)
		if encodeErr != nil {
			return ErrInvalid
		}
		return service.storeRevision(ctx, entry.Group, entry.Directory, suite, raw)
	}
	raw, err := encodeStoredSuiteRevision(suite)
	if err != nil {
		return ErrInvalid
	}
	return service.storeRevision(ctx, entry.Group, entry.Directory, suite, raw)
}

func (service *Service) SaveSuite(ctx context.Context, group, directory string, suite domain.Suite) error {
	if err := suite.Validate(); err != nil || string(suite.Protocol) != group {
		return ErrInvalid
	}
	caseEntries, err := service.cases.Entries(ctx)
	if err != nil {
		return err
	}
	caseKeys := make([]string, len(suite.Cases))
	for index, ref := range suite.Cases {
		found := false
		for _, entry := range caseEntries {
			testCase := entry.TestCase
			if testCase.ID == ref.CaseID && testCase.Revision == ref.Revision && suite.AcceptsCase(testCase) {
				caseKeys[index] = testCase.Key
				found = true
				break
			}
		}
		if !found {
			return ErrInvalid
		}
	}
	raw, err := json.MarshalIndent(document{
		SchemaVersion: CurrentSchemaVersion, Key: suite.Key, Name: suite.Name, Protocol: suite.Protocol,
		ModelTarget: suite.ModelTarget, CaseKeys: caseKeys, QuickTest: suite.QuickTest.Clone(),
	}, "", "  ")
	if err != nil {
		return ErrInvalid
	}
	return service.Save(ctx, group, directory, raw)
}

func (service *Service) Save(ctx context.Context, group, directory string, raw []byte) error {
	if service == nil || ctx == nil || !validSegment(group) || !validSegment(directory) || len(raw) == 0 {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	doc, err := decodeDocument(raw)
	if err != nil || string(doc.Protocol) != group {
		return ErrInvalid
	}
	caseEntries, err := service.cases.Entries(ctx)
	if err != nil {
		return err
	}
	casesByIdentity := make(map[string]domain.TestCase, len(caseEntries))
	for _, entry := range caseEntries {
		casesByIdentity[string(entry.TestCase.Protocol)+"/"+entry.TestCase.Key] = entry.TestCase
	}
	suite, err := materialize(doc, casesByIdentity)
	if err != nil {
		return ErrInvalid
	}
	current, findErr := service.Find(ctx, suite.ID)
	if findErr == nil {
		if current.Group != group || current.Directory != directory {
			return ErrCollision
		}
		if current.Suite.Revision != suite.Revision {
			previous, encodeErr := encodeStoredSuiteRevision(current.Suite)
			if encodeErr != nil {
				return ErrInvalid
			}
			if err := service.storeRevision(ctx, group, directory, current.Suite, previous); err != nil {
				return err
			}
		}
	} else if !errors.Is(findErr, fs.ErrNotExist) {
		return findErr
	}

	targetDirectory := filepath.Join(service.userRoot, group, directory)
	target := filepath.Join(targetDirectory, "suite.json")
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	if err := fileconfig.WriteAtomically(ctx, target, raw); err != nil {
		return fmt.Errorf("install suite file: %w", err)
	}
	return nil
}

func (service *Service) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	entry, err := service.Find(ctx, id)
	if err != nil || entry.Source != SourceUser || entry.Suite.Revision != expectedRevision {
		return ErrInvalid
	}
	targetDirectory := filepath.Join(service.userRoot, entry.Group, entry.Directory)
	target := filepath.Join(targetDirectory, "suite.json")
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return ErrInvalid
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("delete user suite: %w", err)
	}
	_ = os.Remove(targetDirectory)
	_ = os.Remove(filepath.Dir(targetDirectory))
	return nil
}

func (service *Service) storeRevision(ctx context.Context, group, directory string, suite domain.Suite, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := service.revisionPath(group, directory, suite.Revision)
	if !withinRoot(service.userRoot, target) {
		return ErrInvalid
	}
	if existing, err := os.ReadFile(target); err == nil {
		stored, decodeErr := decodeStoredSuiteRevision(existing)
		if decodeErr != nil || !equalStoredSuiteRevision(stored, suite) {
			return ErrCollision
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect suite revision: %w", err)
	}
	if err := fileconfig.WriteAtomically(ctx, target, raw); err != nil {
		return fmt.Errorf("write suite revision: %w", err)
	}
	return nil
}

func (service *Service) revisionPath(group, directory string, revision uint64) string {
	return filepath.Join(service.userRoot, group, directory, "revisions", strconv.FormatUint(revision, 10)+".json")
}

func encodeStoredSuiteRevision(suite domain.Suite) ([]byte, error) {
	if err := suite.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func decodeStoredSuiteRevision(raw []byte) (domain.Suite, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var suite domain.Suite
	if err := decoder.Decode(&suite); err != nil {
		return domain.Suite{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return domain.Suite{}, errors.New("suite revision must contain one object")
		}
		return domain.Suite{}, err
	}
	if err := suite.Validate(); err != nil {
		return domain.Suite{}, err
	}
	return suite, nil
}

func equalStoredSuiteRevision(left, right domain.Suite) bool {
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
			return errors.New("suite catalog must not contain symbolic links")
		}
		if entry.IsDir() || entry.Name() != "suite.json" {
			return nil
		}
		parts := strings.Split(path, "/")
		if len(parts) != 3 || !validSegment(parts[0]) || !validSegment(parts[1]) {
			return fmt.Errorf("invalid suite catalog path %q", path)
		}
		raw, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			return err
		}
		doc, err := decodeDocument(raw)
		if err != nil || string(doc.Protocol) != parts[0] {
			return fmt.Errorf("invalid suite file %q", path)
		}
		key := parts[0] + "/" + parts[1]
		result[key] = discovered{group: parts[0], directory: parts[1], source: source, document: doc}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func decodeDocument(raw []byte) (document, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var doc document
	if err := decoder.Decode(&doc); err != nil {
		return document{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return document{}, errors.New("suite JSON must contain exactly one value")
		}
		return document{}, err
	}
	if doc.SchemaVersion != CurrentSchemaVersion || !validKey(doc.Key) || strings.TrimSpace(doc.Name) == "" || doc.Protocol.Validate() != nil || (doc.ModelTarget != "" && !validModelTarget(doc.ModelTarget)) || (doc.ModelTarget == "" && doc.QuickTest == nil) || len(doc.CaseKeys) == 0 {
		return document{}, ErrInvalid
	}
	seen := make(map[string]struct{}, len(doc.CaseKeys))
	for _, key := range doc.CaseKeys {
		if !validKey(key) {
			return document{}, ErrInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return document{}, ErrInvalid
		}
		seen[key] = struct{}{}
	}
	return doc, nil
}

func materialize(doc document, cases map[string]domain.TestCase) (domain.Suite, error) {
	refs := make([]domain.CaseRevisionRef, len(doc.CaseKeys))
	definitions := make([]domain.TestCase, len(doc.CaseKeys))
	selector := domain.Suite{Protocol: doc.Protocol, ModelTarget: doc.ModelTarget, QuickTest: doc.QuickTest}
	for index, key := range doc.CaseKeys {
		testCase, found := cases[string(doc.Protocol)+"/"+key]
		if !found || !selector.AcceptsCase(testCase) {
			return domain.Suite{}, ErrInvalid
		}
		refs[index] = domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision}
		definitions[index] = testCase
	}
	encoded, err := json.Marshal(struct {
		Document document                 `json:"document"`
		Cases    []domain.CaseRevisionRef `json:"cases"`
	}{Document: doc, Cases: refs})
	if err != nil {
		return domain.Suite{}, err
	}
	digest := sha256.Sum256(encoded)
	revision := binary.BigEndian.Uint64(digest[:8]) & ((1 << 53) - 1)
	if revision == 0 {
		revision = 1
	}
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	suite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: stableSuiteID(string(doc.Protocol) + "/" + doc.Key), SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: revision, CreatedAt: stamp, UpdatedAt: stamp,
		},
		Key: doc.Key, Name: doc.Name, Protocol: doc.Protocol, ModelTarget: doc.ModelTarget, Cases: refs, QuickTest: doc.QuickTest.Clone(),
	}
	if err := suite.ValidateCases(definitions); err != nil {
		return domain.Suite{}, err
	}
	return suite, nil
}

func stableSuiteID(identity string) string {
	namespace := [16]byte{0x67, 0x9d, 0x9f, 0x3b, 0x18, 0x9e, 0x5b, 0x81, 0x95, 0x28, 0x68, 0xf4, 0xef, 0x6d, 0xd4, 0x52}
	hash := sha1.New()
	_, _ = hash.Write(namespace[:])
	_, _ = hash.Write([]byte("filesystem.suites/v1/" + identity))
	value := hash.Sum(nil)[:16]
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func validKey(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || index > 0 && strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func validModelTarget(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value && len(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validSegment(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && value != "." && value != ".." && !strings.ContainsAny(value, `/\:`) && fs.ValidPath(value)
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
