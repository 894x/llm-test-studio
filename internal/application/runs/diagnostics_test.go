package runs

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDiagnosticDispatcherPreservesOrderAndDrains(t *testing.T) {
	var mu sync.Mutex
	operations := make([]string, 0, 3)
	dispatcher := newDiagnosticDispatcher(func(diagnostic Diagnostic) {
		mu.Lock()
		operations = append(operations, diagnostic.Operation)
		mu.Unlock()
	})
	for _, operation := range []string{"execute", "persist", "transition"} {
		if !dispatcher.submit(Diagnostic{Operation: operation}) {
			t.Fatalf("submit(%q) rejected", operation)
		}
	}
	if err := dispatcher.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(operations, []string{"execute", "persist", "transition"}) {
		t.Fatalf("operations = %#v, want submission order", operations)
	}
}

func TestDiagnosticDispatcherBoundsShutdownWhenCallbackBlocks(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	dispatcher := newDiagnosticDispatcher(func(Diagnostic) {
		close(entered)
		<-release
	})
	dispatcher.shutdownTimeout = 20 * time.Millisecond
	if !dispatcher.submit(Diagnostic{Operation: "execute"}) {
		t.Fatal("submit() rejected")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback was not invoked")
	}
	started := time.Now()
	if err := dispatcher.close(); !errors.Is(err, errDiagnosticShutdownTimeout) {
		t.Fatalf("close() error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("close() took %v, want bounded shutdown", elapsed)
	}
	close(release)
	select {
	case <-dispatcher.done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not exit after blocked callback was released")
	}
}
