// Package benchmarkruntime is the process adapter for the shipped Inspect worker.
package benchmarkruntime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/benchmarks"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/protocol"
)

//go:embed pyproject.toml uv.lock worker.py datasets.json
var assets embed.FS

var assetNames = []string{"pyproject.toml", "uv.lock", "worker.py", "datasets.json"}

func CatalogJSON() []byte {
	payload, _ := assets.ReadFile("datasets.json")
	return payload
}

type Adapter struct {
	mu        sync.Mutex
	directory string
	identity  string
}

var _ benchmarks.DatasetRuntime = (*Adapter)(nil)

func New(directory string) (*Adapter, error) {
	if !filepath.IsAbs(directory) {
		return nil, benchmarks.ErrInvalid
	}
	hash := sha256.New()
	for _, name := range assetNames {
		payload, err := assets.ReadFile(name)
		if err != nil {
			return nil, err
		}
		_, _ = hash.Write(payload)
	}
	identity := hex.EncodeToString(hash.Sum(nil))
	return &Adapter{directory: filepath.Join(directory, "inspect-"+identity[:16]), identity: identity}, nil
}

func (a *Adapter) python() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(a.directory, ".venv", "Scripts", "python.exe")
	}
	return filepath.Join(a.directory, ".venv", "bin", "python")
}

func (a *Adapter) Ready() bool {
	payload, err := os.ReadFile(filepath.Join(a.directory, ".ready"))
	if err != nil || string(payload) != a.identity {
		return false
	}
	info, err := os.Stat(a.python())
	return err == nil && info.Mode().IsRegular()
}

func (a *Adapter) Prepare(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	uv, err := exec.LookPath("uv")
	if err != nil {
		return benchmarks.ErrRuntime
	}
	for _, name := range assetNames {
		payload, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		if err := fileconfig.WriteAtomically(ctx, filepath.Join(a.directory, name), payload); err != nil {
			return err
		}
	}
	command := exec.CommandContext(ctx, uv, "sync", "--locked", "--no-dev", "--python", "3.12", "--no-progress")
	command.Dir = a.directory
	command.Env = append(os.Environ(), "UV_PROJECT_ENVIRONMENT="+filepath.Join(a.directory, ".venv"), "UV_LINK_MODE=copy")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.WaitDelay = 5 * time.Second
	hideWindow(command)
	if err := command.Run(); err != nil {
		return benchmarks.ErrRuntime
	}
	check := exec.CommandContext(ctx, a.python(), "-X", "utf8", "-I", filepath.Join(a.directory, "worker.py"), "check")
	check.Dir = a.directory
	check.Stdout, check.Stderr = io.Discard, io.Discard
	hideWindow(check)
	if err := check.Run(); err != nil {
		return benchmarks.ErrRuntime
	}
	return fileconfig.WriteAtomically(ctx, filepath.Join(a.directory, ".ready"), []byte(a.identity))
}

func (a *Adapter) Execute(
	ctx context.Context, request benchmarks.WorkerRequest, receive func(benchmarks.Event) error,
) error {
	if !a.Ready() || receive == nil {
		return benchmarks.ErrRuntime
	}
	if err := fileconfig.EnsurePrivateDirectory(ctx, request.LogDir); err != nil {
		return err
	}
	base, err := inspectBaseURL(request.BaseURL, request.Protocol)
	if err != nil {
		return err
	}
	request.BaseURL = base
	payload, err := json.Marshal(request)
	if err != nil {
		return benchmarks.ErrInvalid
	}
	defer clear(payload)
	return a.runWorker(ctx, payload, func(line []byte) error {
		var event benchmarks.Event
		if err := decodeWorkerEvent(line, &event); err != nil {
			return err
		}
		return receive(event)
	})
}

func (a *Adapter) DatasetKey(id string) string {
	var manifest struct {
		Datasets []json.RawMessage `json:"datasets"`
	}
	if json.Unmarshal(CatalogJSON(), &manifest) != nil {
		return ""
	}
	for _, entry := range manifest.Datasets {
		var dataset struct {
			ID      string `json:"id"`
			Module  string `json:"module"`
			Factory string `json:"factory"`
		}
		if json.Unmarshal(entry, &dataset) == nil && dataset.ID == id {
			packages, _ := assets.ReadFile("pyproject.toml")
			definition := dataset.ID + "\x00" + dataset.Module + "\x00" + dataset.Factory
			// Bump this cache format when the worker's data layout or loader changes.
			hash := sha256.Sum256(append(append([]byte("dataset-cache-1"), packages...), []byte(definition)...))
			return hex.EncodeToString(hash[:])
		}
	}
	return ""
}

func (a *Adapter) DownloadDataset(
	ctx context.Context, id, cacheDir string, receive func(benchmarks.DatasetTransfer) error,
) error {
	validRequest := receive != nil && filepath.IsAbs(cacheDir)
	if !validRequest || !a.Ready() || a.DatasetKey(id) == "" {
		return benchmarks.ErrRuntime
	}
	payload, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Action        string `json:"action"`
		DatasetID     string `json:"dataset_id"`
		CacheDir      string `json:"cache_dir"`
	}{SchemaVersion: 1, Action: "download", DatasetID: id, CacheDir: cacheDir})
	if err != nil {
		return err
	}
	finished := false
	err = a.runWorker(ctx, payload, func(line []byte) error {
		var event benchmarks.DatasetTransfer
		if err := decodeWorkerEvent(line, &event); err != nil {
			return err
		}
		if finished {
			return benchmarks.ErrFormat
		}
		finished = event.Kind == "ready"
		return receive(event)
	})
	if err == nil && !finished {
		return benchmarks.ErrFormat
	}
	return err
}

func decodeWorkerEvent(line []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return benchmarks.ErrFormat
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return benchmarks.ErrFormat
	}
	return nil
}

func (a *Adapter) runWorker(ctx context.Context, payload []byte, receive func([]byte) error) error {
	command := exec.CommandContext(ctx, a.python(), "-X", "utf8", "-I", filepath.Join(a.directory, "worker.py"))
	command.Dir = a.directory
	command.WaitDelay = 5 * time.Second
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	command.Stderr = io.Discard
	hideWindow(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	if err := command.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var protocolError error
	for scanner.Scan() {
		if err := receive(scanner.Bytes()); err != nil {
			protocolError = err
			break
		}
	}
	if scanner.Err() != nil {
		protocolError = benchmarks.ErrFormat
	}
	if protocolError != nil {
		_ = command.Process.Kill()
	}
	err = command.Wait()
	return errors.Join(protocolError, err)
}

func inspectBaseURL(address string, selected domain.Protocol) (string, error) {
	path, suffix := "/v1/chat/completions", "/chat/completions"
	if selected == domain.ProtocolOpenAIResponses {
		path, suffix = "/v1/responses", "/responses"
	}
	if selected == domain.ProtocolAnthropicMessages {
		path, suffix = "/v1/messages", "/v1/messages"
	}
	known := []protocol.RequestPath{{Path: "/v1/chat/completions"}, {Path: "/v1/responses"}, {Path: "/v1/messages"}}
	resolved, err := protocol.ResolveAddress(address, path, known)
	if err != nil {
		return "", benchmarks.ErrInvalid
	}
	return strings.TrimSuffix(resolved.Endpoint, suffix), nil
}
