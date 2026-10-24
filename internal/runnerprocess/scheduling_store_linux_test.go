package runnerprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

type schedulingStateFixture struct {
	document    []byte
	writes      int
	commitError bool
	syncError   bool
}

func (state *schedulingStateFixture) ReadPrivate(string, int64) ([]byte, error) {
	return bytes.Clone(state.document), nil
}

func (state *schedulingStateFixture) ReplacePrivate(_ string, document []byte) error {
	state.writes++
	state.document = bytes.Clone(document)
	if state.commitError {
		return errors.New("directory sync failed after rename")
	}
	return nil
}

func (state *schedulingStateFixture) SyncPrivate(string) error {
	if state.syncError {
		return errors.New("sync failed")
	}
	return nil
}

func TestTaskConcurrencyStoreConfirmsRenameAndRetriesOnlyDurability(t *testing.T) {
	config, err := runnerconfig.New([]byte(strings.Repeat("s", 64)))
	if err != nil {
		t.Fatal(err)
	}
	defer config.Clear()
	document, _ := json.Marshal(config)
	defer clear(document)
	state := &schedulingStateFixture{document: document, commitError: true, syncError: true}
	store := schedulingStore{state: state, config: config}
	if err := store.Save(t.Context(), 1, 4); err == nil {
		t.Fatal("unconfirmed durability was reported as success")
	}
	state.syncError = false
	if err := store.Save(t.Context(), 1, 4); err != nil || state.writes != 1 {
		t.Fatal("retry did not reconcile the already renamed configuration")
	}
	actual, err := runnerconfig.Decode(bytes.NewReader(state.document))
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Clear()
	if actual.Budget.MaxActiveJobs != 4 || actual.NodeID != config.NodeID {
		t.Fatal("configuration recovery changed the saved setting or identity")
	}
	before := bytes.Clone(state.document)
	defer clear(before)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Save(canceled, 4, 8); !errors.Is(err, context.Canceled) || !bytes.Equal(before, state.document) {
		t.Fatal("cancelled request mutated configuration")
	}
	state.document = []byte(`{"contract":999}`)
	if err := store.Save(t.Context(), 4, 8); err == nil || string(state.document) != `{"contract":999}` {
		t.Fatal("unsupported configuration was rewritten")
	}
}
