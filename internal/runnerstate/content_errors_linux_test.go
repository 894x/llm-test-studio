package runnerstate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestContentInputFailuresAreDistinctFromStorageFailures(t *testing.T) {
	state := contentTestState(t)
	document := []byte("good")
	identity := contentTestIdentity(document)
	for _, body := range [][]byte{[]byte("g"), []byte("bad!"), []byte("good-extra")} {
		_, err := state.PutContent(t.Context(), identity, bytes.NewReader(body))
		if !errors.Is(err, jobcontract.ErrAssetIntegrity) || errors.Is(err, jobcontract.ErrAssetStorage) {
			t.Fatalf("invalid incoming bytes were not classified separately: %v", err)
		}
	}
	sourceErr := errors.New("test_source_failure")
	source := contentReaderFunc(func([]byte) (int, error) { return 0, sourceErr })
	_, err := state.PutContent(t.Context(), identity, source)
	validRead := errors.Is(err, sourceErr) && errors.Is(err, jobcontract.ErrAssetRead)
	if !validRead || errors.Is(err, jobcontract.ErrAssetStorage) {
		t.Fatalf("incoming reader failure was classified as storage: %v", err)
	}
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.Path(), contentName(identity)), []byte("bad!"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = state.PutContent(t.Context(), identity, bytes.NewReader(document))
	if !errors.Is(err, jobcontract.ErrAssetStorage) || errors.Is(err, jobcontract.ErrAssetIntegrity) {
		t.Fatalf("corrupt stored content was classified as incoming bytes: %v", err)
	}
}

func TestContentFileQuotaCountsRetainedNamesAcrossDirectoryPages(t *testing.T) {
	state := contentTestState(t)
	document := []byte("good")
	identity := contentTestIdentity(document)
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader([]byte("bad!"))); err == nil {
		t.Fatal("bad content was accepted")
	}
	for range 2 {
		if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 129 {
		name := filepath.Join(state.Path(), "content-unknown-"+strconv.Itoa(index))
		if err := os.WriteFile(name, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count, err := state.ContentFiles(t.Context())
	if err != nil || count != 131 {
		t.Fatalf("quota omitted retained partial/unknown names: count=%d err=%v", count, err)
	}
	if got := contentTestRead(t, state, identity); !bytes.Equal(got, document) {
		t.Fatal("quota inspection changed complete content")
	}
}

func TestContentDurabilityFaultIsStorageFailure(t *testing.T) {
	state := contentTestState(t)
	document := []byte("durable")
	storageErr := errors.New("test_sync_failure")
	state.contentSync = func(*os.File) error { return storageErr }
	created, err := state.PutContent(t.Context(), contentTestIdentity(document), bytes.NewReader(document))
	validStorage := errors.Is(err, storageErr) && errors.Is(err, jobcontract.ErrAssetStorage)
	if created || !validStorage {
		t.Fatalf("durability fault was acknowledged or misclassified: created=%v err=%v", created, err)
	}
}
