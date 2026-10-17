package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobfiles"
)

func localFileInputDocument(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rectangle{Max: image.Point{X: 1, Y: 1}})); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(map[string]string{
		"data_base64": base64.StdEncoding.EncodeToString(buffer.Bytes()), "media_type": "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func localFileInputApp() *DesktopApp {
	return New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, nil
	})
}

func TestLocalFileSelectionDesktopLifecycle(t *testing.T) {
	app := localFileInputApp()
	document := localFileInputDocument(t)
	if _, err := app.CreateLocalFileSelection(document); err == nil || err.Error() != desktopCodeNotStarted {
		t.Fatal("selection created before startup")
	}
	app.onStartup(context.Background())
	selection, err := app.CreateLocalFileSelection(document)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(serialized, &fields) != nil || len(fields) != 4 {
		t.Fatal("selection DTO has unexpected fields")
	}
	for _, name := range []string{"selection_id", "sha256", "size", "media_type"} {
		if _, exists := fields[name]; !exists {
			t.Fatal("selection DTO omits a current field")
		}
	}
	registry := app.fileSelections
	reader, err := registry.Borrow(context.Background(), selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ReleaseLocalFileSelection(selection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Borrow(context.Background(), selection.ID); !errors.Is(err, jobfiles.ErrNotFound) {
		t.Fatal("desktop release allowed a new borrow")
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal("release interrupted an existing owner")
	}
	if err := app.shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, jobfiles.ErrClosed) {
		t.Fatal("reader survived desktop shutdown")
	}
	_ = reader.Close()
	if _, err := app.CreateLocalFileSelection(document); err == nil || err.Error() != DesktopCodeStopped {
		t.Fatal("selection created after shutdown")
	}
}

func TestLocalFileSelectionRejectsPrivateInputWithoutEcho(t *testing.T) {
	app := localFileInputApp()
	app.onStartup(context.Background())
	t.Cleanup(func() { _ = app.shutdown() })
	reported := []error{}
	app.setErrorReporter(func(err error) { reported = append(reported, err) })
	privateText := "C:/private-secret-marker/image.png"
	_, err := app.CreateLocalFileSelection(`{"path":"` + privateText + `"}`)
	if err == nil || err.Error() != desktopCodeFileInputInvalid {
		t.Fatal("unsupported path DTO was accepted")
	}
	for _, report := range append(reported, err) {
		if strings.Contains(report.Error(), privateText) || strings.Contains(report.Error(), "data_base64") {
			t.Fatal("file content or path escaped the error boundary")
		}
	}
	if err := app.ReleaseLocalFileSelection("not-an-id"); err == nil || err.Error() != desktopCodeFileInputInvalid {
		t.Fatal("malformed selection ID was accepted")
	}
}

func TestLocalFileSelectionErrorCodesAreCurrent(t *testing.T) {
	app := localFileInputApp()
	t.Cleanup(func() { _ = app.shutdown() })
	tests := []struct {
		err  error
		code string
	}{
		{err: jobfiles.ErrInvalid, code: desktopCodeFileInputInvalid},
		{err: jobfiles.ErrTooLarge, code: desktopCodeFileInputTooLarge},
		{err: jobfiles.ErrLimit, code: desktopCodeFileInputLimit},
		{err: jobfiles.ErrBusy, code: desktopCodeFileInputBusy},
		{err: jobfiles.ErrNotFound, code: desktopCodeFileInputNotFound},
		{err: jobfiles.ErrClosed, code: desktopCodeFileInputUnavailable},
	}
	for _, test := range tests {
		if got := app.safeBindingError(test.err); got.Error() != test.code || !isDesktopBindingCode(test.code) {
			t.Fatalf("file input classification = %v, want %s", got, test.code)
		}
	}
}

func TestLocalFileSelectionShutdownRacesImports(t *testing.T) {
	app := localFileInputApp()
	app.onStartup(context.Background())
	document := localFileInputDocument(t)
	start := make(chan struct{})
	var operations sync.WaitGroup
	for range 16 {
		operations.Go(func() {
			<-start
			_, err := app.CreateLocalFileSelection(document)
			if err == nil {
				return
			}
			switch err.Error() {
			case DesktopCodeStopped, desktopCodeFileInputBusy, desktopCodeFileInputLimit,
				desktopCodeOperationCancelled, desktopCodeFileInputUnavailable:
			default:
				t.Errorf("unexpected import outcome: %v", err)
			}
		})
	}
	close(start)
	if err := app.shutdown(); err != nil {
		t.Fatal(err)
	}
	operations.Wait()
	if app.fileSelections != nil {
		t.Fatal("desktop retained the registry after shutdown")
	}
}
