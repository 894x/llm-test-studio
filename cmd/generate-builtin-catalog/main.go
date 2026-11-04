// Command generate-builtin-catalog emits the same catalog projection as the
// desktop using only embedded definitions and an isolated empty user root.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/persistence/workspace"
)

type clock struct{}

func (clock) Now() time.Time { return time.Unix(0, 0).UTC() }

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	root, err := os.MkdirTemp("", "llm-test-builtin-catalog-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	repository, err := workspace.OpenCatalog(root, workspace.CatalogOptions{})
	if err != nil {
		return err
	}
	service, err := catalog.New(catalog.Dependencies{Repository: repository, Clock: clock{}})
	if err != nil {
		return err
	}
	snapshot, err := service.Snapshot(context.Background(), catalog.ScopeAll)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(snapshot)
}
