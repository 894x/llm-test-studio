package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type controlPageRepository struct {
	ControlRepository
	list func(string, uint32) (ControlPage, error)
}

func (repository *controlPageRepository) ListPendingControls(
	_ context.Context,
	after string,
	limit uint32,
) (ControlPage, error) {
	return repository.list(after, limit)
}

func pendingControlFixture(index int) ControlRecord {
	at := time.Date(
		2026,
		10,
		16,
		1,
		0,
		0,
		0,
		time.UTC,
	)
	return ControlRecord{SchemaVersion: 1, Revision: 1, State: ControlPending, CreatedAt: at, UpdatedAt: at,
		Target: ControlTarget{JobID: "83000000-0000-4000-8000-000000000099",
			Location: Location{Kind: jobcontract.TargetLocal}, NodeID: "83000000-0000-4000-8000-000000000098",
			InstallationID: "83000000-0000-4000-8000-000000000097", SpecSHA256: strings.Repeat("a", 64)},
		Command: jobcontract.Command{Contract: jobcontract.CurrentContract, Kind: jobcontract.CommandCancel,
			JobID: "83000000-0000-4000-8000-000000000099", ExpectedRevision: 1,
			CommandID: fmt.Sprintf("83000000-0000-4000-8000-%012d", index)}}
}

func TestControlDeliveryWatcherBoundsConcurrencyAndJoinsCanceledDeliveries(t *testing.T) {
	records := []ControlRecord{}
	for index := 1; index <= controlsPerPass; index++ {
		records = append(records, pendingControlFixture(index))
	}
	entered := make(chan struct{}, controlsPerPass)
	var completed atomic.Int64
	watcher, err := NewControlDeliveryWatcher(t.Context(), ControlDeliveryDependencies{
		Repository: &controlPageRepository{list: func(string, uint32) (ControlPage, error) {
			return ControlPage{Records: records, NextCursor: records[len(records)-1].Command.CommandID}, nil
		}},
		Deliver: func(ctx context.Context, _ string) (ControlRecord, error) {
			entered <- struct{}{}
			<-ctx.Done()
			completed.Add(1)
			return ControlRecord{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	for range controlsPerPass {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("bounded delivery pass did not start")
		}
	}
	if err := watcher.Run(); !errors.Is(err, ErrConflict) {
		t.Fatal("delivery watcher acquired two polling owners")
	}
	closeError := watcher.Close()
	stopped := completed.Load() == controlsPerPass && !watcher.Snapshot().Running
	if closeError != nil || !stopped {
		t.Fatal("close returned before all command transports exited")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := watcher.Run(); !errors.Is(err, context.Canceled) {
		t.Fatal("closed command worker restarted")
	}
}

func TestControlDeliveryWatcherAdvancesFailedPagesAndWrapsOnlyAfterTail(t *testing.T) {
	records := []ControlRecord{}
	for index := 1; index <= 7; index++ {
		records = append(records, pendingControlFixture(index))
	}
	var mu sync.Mutex
	seen := []string{}
	watcher, err := NewControlDeliveryWatcher(t.Context(), ControlDeliveryDependencies{
		Repository: &controlPageRepository{list: func(after string, limit uint32) (ControlPage, error) {
			if limit != controlsPerPass {
				t.Error("delivery pass increased its work limit")
			}
			if after == "" {
				return ControlPage{Records: records[:4], NextCursor: records[3].Command.CommandID, HasMore: true}, nil
			}
			if after != records[3].Command.CommandID {
				t.Error("delivery did not resume its original page cursor")
			}
			return ControlPage{Records: records[4:], NextCursor: records[6].Command.CommandID}, nil
		}},
		Deliver: func(_ context.Context, id string) (ControlRecord, error) {
			mu.Lock()
			seen = append(seen, id)
			mu.Unlock()
			return ControlRecord{}, ErrPending
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	next, err := watcher.pass("")
	if !errors.Is(err, ErrPending) || next != records[3].Command.CommandID {
		t.Fatal("failed destinations prevented forward traversal")
	}
	next, err = watcher.pass(next)
	validTail := next == "" && len(seen) == len(records)
	if !errors.Is(err, ErrPending) || !validTail {
		t.Fatal("delivery did not reach the tail before wrapping")
	}
	slices.Sort(seen)
	for index, record := range records {
		if seen[index] != record.Command.CommandID {
			t.Fatal("pass sent an invented ID or skipped a persisted instruction")
		}
	}
}

func TestControlDeliveryWatcherRejectsInvalidPagesBeforeSending(t *testing.T) {
	for _, page := range []ControlPage{
		{Records: nil},
		{Records: []ControlRecord{}, HasMore: true},
		{Records: []ControlRecord{}, NextCursor: "unexpected"},
		{Records: make([]ControlRecord, controlsPerPass+1)},
		{Records: []ControlRecord{pendingControlFixture(2), pendingControlFixture(1)}},
	} {
		watcher, err := NewControlDeliveryWatcher(t.Context(), ControlDeliveryDependencies{
			Repository: &controlPageRepository{list: func(string, uint32) (ControlPage, error) { return page, nil }},
			Deliver: func(context.Context, string) (ControlRecord, error) {
				t.Error("invalid page sent a command")
				return ControlRecord{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := watcher.pass(""); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid command page was accepted")
		}
		_ = watcher.Close()
	}
}
