package runs

import (
	"errors"
	"sync"
	"time"
)

const (
	diagnosticQueueSize       = 256
	diagnosticShutdownTimeout = 2 * time.Second
)

var errDiagnosticShutdownTimeout = errors.New("runs: timed out draining diagnostics")

type diagnosticDispatcher struct {
	report          func(Diagnostic)
	queue           chan Diagnostic
	done            chan struct{}
	shutdownTimeout time.Duration

	mu     sync.Mutex
	closed bool
}

func newDiagnosticDispatcher(report func(Diagnostic)) *diagnosticDispatcher {
	if report == nil {
		return nil
	}
	dispatcher := &diagnosticDispatcher{
		report:          report,
		queue:           make(chan Diagnostic, diagnosticQueueSize),
		done:            make(chan struct{}),
		shutdownTimeout: diagnosticShutdownTimeout,
	}
	go dispatcher.run()
	return dispatcher
}

func (dispatcher *diagnosticDispatcher) submit(diagnostic Diagnostic) bool {
	if dispatcher == nil {
		return false
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.closed {
		return false
	}
	select {
	case dispatcher.queue <- diagnostic:
		return true
	default:
		return false
	}
}

func (dispatcher *diagnosticDispatcher) close() error {
	if dispatcher == nil {
		return nil
	}
	dispatcher.mu.Lock()
	if !dispatcher.closed {
		dispatcher.closed = true
		close(dispatcher.queue)
	}
	dispatcher.mu.Unlock()

	timer := time.NewTimer(dispatcher.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-dispatcher.done:
		return nil
	case <-timer.C:
		return errDiagnosticShutdownTimeout
	}
}

func (dispatcher *diagnosticDispatcher) run() {
	defer close(dispatcher.done)
	for diagnostic := range dispatcher.queue {
		func() {
			defer func() { _ = recover() }()
			dispatcher.report(diagnostic)
		}()
	}
}
