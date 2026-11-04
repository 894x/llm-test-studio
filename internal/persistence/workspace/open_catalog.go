package workspace

import (
	"fmt"
	"io/fs"
	"path/filepath"

	builtinbundle "github.com/894x/llm-test-studio/data"
	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/data/performanceplans"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
)

type CatalogOptions struct {
	Cases  fs.FS
	Suites fs.FS
}

// OpenCatalog is shared by the desktop and the build-time preview. Opening and
// reading catalogs never creates user files or imports definitions into SQLite.
func OpenCatalog(root string, options CatalogOptions) (Catalog, error) {
	modelFiles, err := fs.Sub(builtinbundle.Bundle, "definitions/models")
	if err != nil {
		return Catalog{}, err
	}
	planFiles, err := fs.Sub(builtinbundle.Bundle, "plans")
	if err != nil {
		return Catalog{}, err
	}
	models, err := modelcatalog.New(filepath.Join(root, "models.json"), modelcatalog.WithBuiltin(modelFiles))
	if err != nil {
		return Catalog{}, fmt.Errorf("create filesystem model catalog: %w", err)
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		return Catalog{}, fmt.Errorf("create filesystem channel catalog: %w", err)
	}
	plans, err := plancatalog.New(
		filepath.Join(root, "plans"),
		plancatalog.WithBuiltin(performanceplans.Bundle),
		plancatalog.WithDefaults(planFiles),
	)
	if err != nil {
		return Catalog{}, fmt.Errorf("create filesystem plan catalog: %w", err)
	}
	casesFS := options.Cases
	if casesFS == nil {
		casesFS = casebundle.Bundle
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: casesFS, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		return Catalog{}, fmt.Errorf("create filesystem case catalog: %w", err)
	}
	suitesFS := options.Suites
	if suitesFS == nil {
		suitesFS = suitebundle.Bundle
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: suitesFS, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		return Catalog{}, fmt.Errorf("create filesystem suite catalog: %w", err)
	}
	return Catalog{
		LockPath: filepath.Join(root, ".llm-test-studio-authored-catalog.lock"),
		Models:   models, Channels: channels, Cases: cases, Suites: suites, Plans: plans,
	}, nil
}
