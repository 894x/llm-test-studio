package runnerstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const contentStoreTestInstallation = "11111111-1111-4111-8111-111111111111"

func contentStoreTestTime() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }

func TestContentStoreMarkerCreatesAndReopensStableIdentity(t *testing.T) {
	state := contentTestState(t)
	marker, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime())
	if err != nil {
		t.Fatal(err)
	}
	original := marker.Identity()
	if original.Contract != jobcontract.CurrentContract || original.InstallationID != contentStoreTestInstallation {
		t.Fatal("incorrect store owner")
	}
	path := state.Path()
	before, err := os.ReadFile(filepath.Join(path, ContentStoreName))
	if err != nil {
		t.Fatal(err)
	}
	if err := marker.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := marker.Check(t.Context()); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed state still accepted marker", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime().Add(time.Hour))
	if err != nil || again.Identity() != original {
		t.Fatal("reopen replaced immutable store identity", err)
	}
	after, _ := os.ReadFile(filepath.Join(path, ContentStoreName))
	if !bytes.Equal(before, after) {
		t.Fatal("reopen changed marker bytes")
	}
	if _, err := reopened.EnsureContentStore(t.Context(), "22222222-2222-4222-8222-222222222222", contentStoreTestTime()); err == nil {
		t.Fatal("different installation silently took over store")
	}
	after, _ = os.ReadFile(filepath.Join(path, ContentStoreName))
	if !bytes.Equal(before, after) {
		t.Fatal("rejected installation changed marker")
	}
}

func TestContentStoreMarkerStrictCurrentShape(t *testing.T) {
	identity := ContentStoreIdentity{
		Contract: jobcontract.CurrentContract, StoreID: "22222222-2222-4222-8222-222222222222",
		InstallationID: contentStoreTestInstallation, CreatedAt: contentStoreTestTime(),
	}
	raw, _ := json.Marshal(identity)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for field := range fields {
		for _, mode := range []string{"missing", "null", "wrong_type"} {
			changed := make(map[string]json.RawMessage, len(fields))
			for key, value := range fields {
				changed[key] = value
			}
			switch mode {
			case "missing":
				delete(changed, field)
			case "null":
				changed[field] = json.RawMessage("null")
			case "wrong_type":
				changed[field] = json.RawMessage("{}")
			}
			bad, _ := json.Marshal(changed)
			if _, err := decodeContentStore(bad); err == nil {
				t.Fatalf("accepted %s/%s", field, mode)
			}
		}
	}
	for _, bad := range []string{
		"null", "[]", string(raw) + "{}",
		strings.Replace(string(raw), `"contract":1`, `"contract":1,"contract":1`, 1),
		strings.Replace(string(raw), `"contract":1`, `"contract":1,"contr\u0061ct":1`, 1),
		strings.Replace(string(raw), `"contract":1`, `"Contract":1`, 1),
		strings.Replace(string(raw), `"contract":1`, `"contract":1,"version":1`, 1),
	} {
		if _, err := decodeContentStore([]byte(bad)); err == nil {
			t.Fatal("accepted unsupported marker shape")
		}
	}
	padded := append(bytes.Clone(raw), bytes.Repeat([]byte(" "), MaxContentStoreBytes-len(raw))...)
	if _, err := decodeContentStore(padded); err != nil {
		t.Fatal("valid exact byte limit rejected", err)
	}
	if _, err := decodeContentStore(append(padded, ' ')); !errors.Is(err, jobcontract.ErrTooLarge) {
		t.Fatal("oversized marker accepted", err)
	}
}

func TestContentStoreMarkerRejectsUnsafeFileAndRootWithoutReplacement(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *State, string)
	}{
		{name: "old shape", change: func(t *testing.T, _ *State, path string) {
			if err := os.WriteFile(path, []byte(`{"version":1,"id":"old"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file permissions", change: func(t *testing.T, _ *State, path string) {
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "hard link", change: func(t *testing.T, state *State, path string) {
			if err := os.Link(path, filepath.Join(state.Path(), "marker-alias")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file owner", change: func(t *testing.T, _ *State, path string) {
			if os.Geteuid() != 0 {
				t.Skip("owner-change check requires root")
			}
			if err := os.Chown(path, 65534, -1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symbolic link", change: func(t *testing.T, state *State, path string) {
			other := filepath.Join(state.Path(), "retained-marker")
			if err := os.Rename(path, other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "same document new inode", change: func(t *testing.T, state *State, path string) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, filepath.Join(state.Path(), "retained-marker")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "root permissions", change: func(t *testing.T, state *State, _ string) {
			if err := os.Chmod(state.Path(), 0500); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := contentTestState(t)
			marker, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state.Path(), ContentStoreName)
			test.change(t, state, path)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := marker.Check(t.Context()); err == nil {
				t.Fatal("changed marker accepted")
			}
			if test.name != "same document new inode" {
				if _, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime()); err == nil {
					t.Fatal("unsafe marker was silently replaced")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected marker bytes changed")
			}
		})
	}
}

func TestContentStoreMarkerRejectsInvalidCurrentIdentity(t *testing.T) {
	for _, change := range []func(*ContentStoreIdentity){
		func(identity *ContentStoreIdentity) { identity.StoreID = "00000000-0000-0000-0000-000000000000" },
		func(identity *ContentStoreIdentity) { identity.InstallationID = "" },
		func(identity *ContentStoreIdentity) { identity.CreatedAt = time.Time{} },
		func(identity *ContentStoreIdentity) {
			identity.CreatedAt = identity.CreatedAt.In(time.FixedZone("offset", 3600))
		},
	} {
		identity := ContentStoreIdentity{
			Contract: jobcontract.CurrentContract, StoreID: "22222222-2222-4222-8222-222222222222",
			InstallationID: contentStoreTestInstallation, CreatedAt: contentStoreTestTime(),
		}
		change(&identity)
		raw, _ := json.Marshal(identity)
		if _, err := decodeContentStore(raw); err == nil {
			t.Fatal("invalid current marker identity accepted")
		}
	}
}

func TestContentStoreConcurrentInitializationKeepsOneConfirmedIdentity(t *testing.T) {
	state := contentTestState(t)
	const count = 8
	identities := make(chan ContentStoreIdentity, count)
	failures := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			marker, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime())
			if err != nil {
				failures <- err
				return
			}
			identities <- marker.Identity()
		}()
	}
	group.Wait()
	close(failures)
	close(identities)
	for err := range failures {
		t.Fatal("concurrent initialization failed", err)
	}
	var first ContentStoreIdentity
	for identity := range identities {
		if first.StoreID == "" {
			first = identity
		}
		if identity != first {
			t.Fatal("concurrent initialization acknowledged different store identities")
		}
	}
	if first.StoreID == "" {
		t.Fatal("no store identity confirmed")
	}
}

func TestContentStoreMarkerConfirmationFailureRetainsOwnedData(t *testing.T) {
	for _, directoryFailure := range []bool{false, true} {
		state := contentTestState(t)
		state.contentSync = func(file *os.File) error {
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if info.IsDir() == directoryFailure {
				return errors.New("forced content marker confirmation failure")
			}
			return file.Sync()
		}
		if _, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime()); err == nil {
			t.Fatal("unconfirmed store identity acknowledged")
		}
		entries, err := os.ReadDir(state.Path())
		if err != nil {
			t.Fatal(err)
		}
		var retained bool
		for _, entry := range entries {
			retained = retained || strings.HasPrefix(entry.Name(), ".content-store-") || entry.Name() == ContentStoreName
		}
		if !retained {
			t.Fatal("failed marker publication removed its owned evidence")
		}
		state.contentSync = nil
		if _, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime()); err != nil {
			t.Fatal("explicit retry could not confirm a preserved current marker", err)
		}
	}
}

func TestContentStoreMarkerCloseBeforePublicationPreservesPartial(t *testing.T) {
	state := contentTestState(t)
	synced := make(chan struct{})
	resume := make(chan struct{})
	state.contentSync = func(file *os.File) error {
		if err := file.Sync(); err != nil {
			return err
		}
		close(synced)
		<-resume
		return nil
	}
	finished := make(chan error, 1)
	go func() {
		_, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime())
		finished <- err
	}()
	<-synced
	closeErr := state.Close()
	close(resume)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := <-finished; !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed state acknowledged marker publication", err)
	}
	if _, err := os.Lstat(filepath.Join(state.Path(), ContentStoreName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("closed state published marker", err)
	}
	partials, err := filepath.Glob(filepath.Join(state.Path(), ".content-store-*.partial"))
	if err != nil || len(partials) != 1 {
		t.Fatal("closed publication discarded partial evidence", err)
	}
}

func TestContentStoreMarkerPublicationHoldsCloseUntilDirectoryConfirmation(t *testing.T) {
	state := contentTestState(t)
	confirming := make(chan struct{})
	resume := make(chan struct{})
	var first sync.Once
	state.contentSync = func(file *os.File) error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.IsDir() {
			first.Do(func() {
				close(confirming)
				<-resume
			})
		}
		return file.Sync()
	}
	finished := make(chan error, 1)
	go func() {
		_, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime())
		finished <- err
	}()
	<-confirming
	closing := make(chan struct{})
	closed := make(chan error, 1)
	go func() {
		close(closing)
		closed <- state.Close()
	}()
	<-closing
	closedEarly := false
	var closeErr error
	select {
	case closeErr = <-closed:
		closedEarly = true
	case <-time.After(25 * time.Millisecond):
	}
	close(resume)
	if !closedEarly {
		closeErr = <-closed
	}
	<-finished
	if closedEarly {
		t.Fatal("State.Close completed while marker publication still held a directory descriptor", closeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, err := state.EnsureContentStore(t.Context(), contentStoreTestInstallation, contentStoreTestTime()); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed state accepted another publication", err)
	}
}
