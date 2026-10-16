package runnerprocess

import (
	"context"
	"net/http"
	"sync"
)

// drainingHandler joins handlers as well as Server.Serve. Shutdown may time out
// while a handler finishes CPU work after its request context was cancelled.
type drainingHandler struct {
	next    http.Handler
	mu      sync.Mutex
	stopped bool
	active  sync.WaitGroup
}

func (handler *drainingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mu.Lock()
	if handler.stopped {
		handler.mu.Unlock()
		http.Error(writer, "runner is stopping", http.StatusServiceUnavailable)
		return
	}
	handler.active.Add(1)
	handler.mu.Unlock()
	defer handler.active.Done()
	handler.next.ServeHTTP(writer, request)
}

func (handler *drainingHandler) shutdown(
	ctx context.Context,
	server *http.Server,
	cancelAPI context.CancelFunc,
	serveDone <-chan struct{},
) {
	// Closing admission and Add share the same lock. No new Add can race Wait.
	handler.mu.Lock()
	handler.stopped = true
	handler.mu.Unlock()
	cancelAPI()
	_ = server.Shutdown(ctx)
	_ = server.Close()
	<-serveDone
	handler.active.Wait()
}
