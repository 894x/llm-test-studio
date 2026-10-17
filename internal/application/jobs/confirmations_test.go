package jobs

import (
	"context"
	"errors"
	"testing"
)

type confirmationPageRepository struct {
	SubmissionRepository
	page SubmissionPage
}

func (repository *confirmationPageRepository) ListPendingSubmissions(
	context.Context,
	string,
	uint32,
) (SubmissionPage, error) {
	return repository.page, nil
}

func TestConfirmationWatcherRejectsInvalidPagesBeforeAnyQuery(t *testing.T) {
	for _, page := range []SubmissionPage{
		{Records: []SubmissionRecord{}, HasMore: true},
		{Records: []SubmissionRecord{}, NextCursor: "unexpected"},
		{Records: make([]SubmissionRecord, confirmationsPerPass+1)},
		{Records: []SubmissionRecord{{State: SubmissionPrepared}}},
	} {
		watcher, err := NewConfirmationWatcher(t.Context(), ConfirmationDependencies{
			Repository: &confirmationPageRepository{page: page},
			Reconcile: func(context.Context, string) (SubmissionRecord, error) {
				t.Error("invalid pending page caused a network query")
				return SubmissionRecord{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := watcher.pass(""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid page accepted: %v", err)
		}
		_ = watcher.Close()
	}
}

func TestConfirmationWatcherRejectsMissingDependenciesAndCloseBeforeRun(t *testing.T) {
	if _, err := NewConfirmationWatcher(t.Context(), ConfirmationDependencies{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing confirmation ports accepted")
	}
	var typedNil *confirmationPageRepository
	dependencies := ConfirmationDependencies{Repository: typedNil,
		Reconcile: func(context.Context, string) (SubmissionRecord, error) { return SubmissionRecord{}, nil }}
	if _, err := NewConfirmationWatcher(t.Context(), dependencies); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil repository accepted")
	}
	dependencies.Repository = &confirmationPageRepository{page: SubmissionPage{Records: []SubmissionRecord{}}}
	watcher, err := NewConfirmationWatcher(t.Context(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	_ = watcher.Close()
	if err := watcher.Run(); !errors.Is(err, context.Canceled) {
		t.Fatal("closed confirmation worker restarted")
	}
}
