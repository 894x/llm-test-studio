package runnerprocess

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestHTTPDrainJoinsHandlersBeforeClosingRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drain.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "drain-test"}); err != nil {
		t.Fatal(err)
	}
	writer, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	reader, err := writer.OpenReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	assertOpen := func() error {
		_, err := reader.GetJob(context.Background(), "30000000-0000-4000-8000-000000000006")
		if !errors.Is(err, jobhost.ErrNotFound) {
			return fmt.Errorf("expected open reader with missing job: %v", err)
		}
		return writer.CheckWritable(context.Background())
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	finished := make(chan error, 1)
	requests := &drainingHandler{next: http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		close(cancelled)
		// Model validation CPU work that must finish after cancellation. The
		// actual repositories must stay open until the handler returns.
		<-release
		finished <- assertOpen()
	})}
	apiCtx, cancelAPI := context.WithCancel(context.Background())
	defer cancelAPI()
	server := &http.Server{Handler: requests, BaseContext: func(net.Listener) context.Context { return apiCtx }}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = server.Serve(listener) }()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-entered
	closed := make(chan error, 1)
	go func() {
		// An expired graceful shutdown still has to join actual handlers before
		// the owner's next defers can close either repository.
		expired, cancel := context.WithCancel(context.Background())
		cancel()
		requests.shutdown(expired, server, cancelAPI, serveDone)
		closed <- errors.Join(reader.Close(), writer.Close())
	}()
	<-cancelled
	<-serveDone
	if err := assertOpen(); err != nil {
		t.Fatalf("repository closed while its HTTP handler was active: %v", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("shutdown passed the handler before it returned: %v", err)
	default:
	}
	rejected := httptest.NewRecorder()
	requests.ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "/", nil))
	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("new request entered after shutdown: %d", rejected.Code)
	}
	unblock()
	if err := <-finished; err != nil {
		t.Fatalf("handler lost its repositories before returning: %v", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the handler returned")
	}
	<-clientDone
	if err := writer.CheckWritable(t.Context()); err == nil {
		t.Fatal("execution repository was not closed after handler drain")
	}
	if _, err := reader.GetJob(t.Context(), "30000000-0000-4000-8000-000000000006"); err == nil {
		t.Fatal("HTTP repository was not closed after handler drain")
	}
}
