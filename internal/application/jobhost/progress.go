package jobhost

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
)

// Progress is a coalesced display projection. Database latency must not block
// the load scheduler or become part of the measured sending window. Complete
// execution observations use the separate synchronous checkpoint boundary.
func (host *Host) startProgressObserver(control *jobControl) (func(quicktest.PerformanceProgress), func()) {
	updates := make(chan quicktest.PerformanceProgress, 1)
	stop, done := make(chan struct{}), make(chan struct{})
	var stopped atomic.Bool
	var stopOnce sync.Once
	go func() {
		defer close(done)
		defer stopped.Store(true)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var latest quicktest.PerformanceProgress
		pending := false
		flush := func() {
			if pending {
				host.observe(control, latest)
				pending = false
			}
		}
		for {
			if host.unhealthy.Load() {
				return
			}
			select {
			case progress := <-updates:
				latest, pending = progress, true
			case <-ticker.C:
				flush()
			case <-stop:
				select {
				case progress := <-updates:
					latest, pending = progress, true
				default:
				}
				flush()
				return
			}
		}
	}()
	observe := func(progress quicktest.PerformanceProgress) {
		if stopped.Load() {
			return
		}
		select {
		case updates <- progress:
			return
		default:
		}
		// Replace at most one pending display value. Concurrent publishers may
		// win this slot; every durable execution fact is kept by checkpoints.
		select {
		case <-updates:
		default:
		}
		select {
		case updates <- progress:
		default:
		}
	}
	finish := func() {
		stopOnce.Do(func() {
			stopped.Store(true)
			close(stop)
		})
		<-done
	}
	return observe, finish
}
