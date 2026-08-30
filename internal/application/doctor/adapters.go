package doctor

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/894x/llm-test/internal/application/compatibility"
)

type OSFileSystem struct{}

func (OSFileSystem) DirectoryHasEntries(ctx context.Context, path string) (bool, error) {
	ctx = nonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	directory, err := os.Open(path)
	if err != nil {
		return false, err
	}
	names, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return len(names) > 0, nil
}

type CompatibilityLister interface {
	List(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error)
}

type CompatibilityCatalog struct {
	Lister CompatibilityLister
}

func (catalog CompatibilityCatalog) CountCases(ctx context.Context, root, suite string) (int, error) {
	if catalog.Lister == nil {
		return 0, fmt.Errorf("compatibility catalog is unavailable")
	}
	cases, err := catalog.Lister.List(nonNilContext(ctx), compatibility.ListRequest{CasesRoot: root, Suite: suite})
	if err != nil {
		return 0, err
	}
	return len(cases), nil
}
