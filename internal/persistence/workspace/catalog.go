package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/diagnostics"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

// filesystemCatalogRepository is the complete authored-catalog boundary. It
// deliberately has no SQLite fallback: adding a new catalog operation must be
// implemented against files explicitly or it will fail to compile.
type Catalog struct {
	// lockPath is shared by every authored catalog rooted beside the same
	// executable. It serializes cross-file validation and the single-file
	// commit so concurrent repository instances preserve compare-and-swap semantics.
	LockPath string
	Models   interface {
		List(context.Context) ([]domain.Model, error)
		Get(context.Context, string) (domain.Model, error)
		Create(context.Context, domain.Model) error
		Update(context.Context, uint64, domain.Model) error
		Delete(context.Context, string, uint64) error
	}
	Channels interface {
		ListChannels(context.Context) ([]domain.Channel, error)
		ListMappings(context.Context) ([]domain.ChannelModel, error)
		GetChannel(context.Context, string) (domain.Channel, error)
		GetMapping(context.Context, string) (domain.ChannelModel, error)
		CreateChannel(context.Context, domain.Channel) error
		CreateChannelWithMappings(context.Context, domain.Channel, []domain.ChannelModel) error
		UpdateChannel(context.Context, uint64, domain.Channel) error
		DeleteChannel(context.Context, string, uint64) error
		CreateMapping(context.Context, domain.ChannelModel) error
		UpdateMapping(context.Context, uint64, domain.ChannelModel) error
		DeleteMapping(context.Context, string, uint64) error
	}
	Cases interface {
		Entries(context.Context) ([]casecatalog.Entry, error)
		Find(context.Context, string) (casecatalog.Entry, error)
		SaveCase(context.Context, string, string, domain.TestCase) error
		Delete(context.Context, string, uint64) error
	}
	Suites interface {
		Entries(context.Context) ([]suitecatalog.Entry, error)
		Find(context.Context, string) (suitecatalog.Entry, error)
		SaveSuite(context.Context, string, string, domain.Suite) error
		Delete(context.Context, string, uint64) error
	}
	Plans interface {
		List(context.Context) ([]domain.Plan, error)
		ListDocuments(context.Context) ([]plancatalog.Document, error)
		Get(context.Context, string) (domain.Plan, error)
		GetDocument(context.Context, string) (plancatalog.Document, error)
		Create(context.Context, domain.Plan) error
		CreateDocument(context.Context, plancatalog.Document) error
		Update(context.Context, uint64, domain.Plan) error
		UpdateDocument(context.Context, uint64, plancatalog.Document) error
		Delete(context.Context, string, uint64) error
	}
}

var _ catalog.Repository = Catalog{}

type filesystemCatalogMutationState struct {
	writeAttempted bool
}

type authoredCatalogLockContextKey struct{}

func (state *filesystemCatalogMutationState) write(action func() error) error {
	state.writeAttempted = true
	return action()
}

func (repository Catalog) withMutationLock(
	ctx context.Context,
	action func(*filesystemCatalogMutationState) error,
	verify func(context.Context) (bool, error),
) error {
	if ctx == nil || !filepath.IsAbs(repository.LockPath) {
		return catalog.ErrInvalid
	}
	state := &filesystemCatalogMutationState{}
	var actionErr error
	verifiedCommitted := false
	runLocked := func() error {
		actionErr = action(state)
		if actionErr == nil || !state.writeAttempted || isDefinitiveCatalogMutationError(actionErr) || verify == nil {
			return actionErr
		}
		committed, verifyErr := verify(context.WithoutCancel(ctx))
		if verifyErr == nil && committed {
			verifiedCommitted = true
			return nil
		}
		if verifyErr != nil {
			return errors.Join(actionErr, verifyErr)
		}
		return actionErr
	}
	var err error
	if repository.authoredCatalogLockHeld(ctx) {
		err = runLocked()
	} else {
		err = fileconfig.WithExclusiveLock(ctx, repository.LockPath, runLocked)
	}
	if verifiedCommitted || actionErr == nil && state.writeAttempted {
		// The authored document is committed. A later lock-release failure must
		// not be reported as a failed save or trigger keyring compensation.
		return nil
	}
	return err
}

// WithCredentialMutation holds the authored-catalog lock across the queue
// journal, keyring operation, and Channel file commit. The derived context lets
// repository methods reuse the already-held lock without self-deadlocking.
func (repository Catalog) WithCredentialMutation(
	ctx context.Context,
	action func(context.Context) error,
) error {
	if ctx == nil || action == nil || !filepath.IsAbs(repository.LockPath) {
		return catalog.ErrInvalid
	}
	var actionErr error
	actionStarted := false
	err := fileconfig.WithExclusiveLock(ctx, repository.LockPath, func() error {
		actionStarted = true
		lockedCtx := context.WithValue(ctx, authoredCatalogLockContextKey{}, filepath.Clean(repository.LockPath))
		actionErr = action(lockedCtx)
		return actionErr
	})
	if actionStarted && actionErr == nil {
		// The whole credential mutation completed. A later lock release error
		// cannot safely be reported as an uncommitted Channel write.
		return nil
	}
	return err
}

func (repository Catalog) authoredCatalogLockHeld(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	lockPath, ok := ctx.Value(authoredCatalogLockContextKey{}).(string)
	return ok && lockPath == filepath.Clean(repository.LockPath)
}

func isDefinitiveCatalogMutationError(err error) bool {
	return errors.Is(err, catalog.ErrInvalid) || errors.Is(err, catalog.ErrNotFound) ||
		errors.Is(err, catalog.ErrConflict) || errors.Is(err, catalog.ErrCorrupt) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func containsCatalogID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (repository Catalog) IsCredentialReferenced(ctx context.Context, credentialID string) (bool, error) {
	if ctx == nil || !domain.IsUUID(credentialID) {
		return false, catalog.ErrInvalid
	}
	return repository.isCredentialReferencedUnlocked(ctx, credentialID)
}

func (repository Catalog) WithCredentialUnreferenced(
	ctx context.Context,
	credentialID string,
	action func() error,
) (bool, error) {
	if ctx == nil || !domain.IsUUID(credentialID) || action == nil || !filepath.IsAbs(repository.LockPath) {
		return false, catalog.ErrInvalid
	}
	executed := false
	actionWhileLocked := func() error {
		referenced, err := repository.isCredentialReferencedUnlocked(ctx, credentialID)
		if err != nil || referenced {
			return err
		}
		executed = true
		return action()
	}
	var err error
	if repository.authoredCatalogLockHeld(ctx) {
		err = actionWhileLocked()
	} else {
		err = fileconfig.WithExclusiveLock(ctx, repository.LockPath, actionWhileLocked)
	}
	return executed, err
}

func (repository Catalog) isCredentialReferencedUnlocked(ctx context.Context, credentialID string) (bool, error) {
	channels, err := repository.ListChannels(ctx)
	if err != nil {
		return false, err
	}
	for _, channel := range channels {
		if channel.CredentialID == credentialID {
			return true, nil
		}
	}
	return false, nil
}

func canonicalCatalogValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func catalogDesiredStateEqual(current, desired any, err error) (bool, error) {
	if errors.Is(err, catalog.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(current, desired), nil
}

func catalogRecordIsMissing(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if errors.Is(err, catalog.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func mapFileCatalogError(err error) error {
	if err == nil {
		return nil
	}
	var diagnostic catalog.SafeDiagnosticCause
	if errors.As(err, &diagnostic) {
		return err
	}
	switch {
	case errors.Is(err, casecatalog.ErrInvalid), errors.Is(err, suitecatalog.ErrInvalid),
		errors.Is(err, modelcatalog.ErrInvalid), errors.Is(err, channelcatalog.ErrInvalid), errors.Is(err, plancatalog.ErrInvalid):
		return catalog.ErrInvalid
	case errors.Is(err, casecatalog.ErrCollision), errors.Is(err, suitecatalog.ErrCollision):
		return catalog.ErrConflict
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, modelcatalog.ErrNotFound), errors.Is(err, channelcatalog.ErrNotFound), errors.Is(err, plancatalog.ErrNotFound):
		return catalog.ErrNotFound
	case errors.Is(err, modelcatalog.ErrConflict), errors.Is(err, channelcatalog.ErrConflict), errors.Is(err, plancatalog.ErrConflict):
		return catalog.ErrConflict
	case errors.Is(err, modelcatalog.ErrCorrupt), errors.Is(err, channelcatalog.ErrCorrupt), errors.Is(err, plancatalog.ErrCorrupt):
		return catalog.ErrCorrupt
	default:
		detail := strings.TrimSpace(diagnostics.RedactText(err.Error()))
		if detail == "" {
			detail = "filesystem catalog operation failed"
		}
		return filesystemCatalogDiagnosticError{cause: err, detail: detail}
	}
}

type filesystemCatalogDiagnosticError struct {
	cause  error
	detail string
}

func (err filesystemCatalogDiagnosticError) Error() string {
	return err.detail
}

func (err filesystemCatalogDiagnosticError) Unwrap() error {
	return err.cause
}

func (err filesystemCatalogDiagnosticError) SafeDiagnosticCause() string {
	return err.detail
}
