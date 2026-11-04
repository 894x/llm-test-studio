package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/data/performanceplans"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
)

type benchmarkCatalogClock struct{}

func (benchmarkCatalogClock) Now() time.Time { return time.Unix(0, 0).UTC() }

func BenchmarkCatalogColdSnapshot(b *testing.B) {
	for _, unified := range []bool{false, true} {
		name := "previous"
		if unified {
			name = "unified"
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			for b.Loop() {
				repository, err := benchmarkCatalog(root, unified)
				if err != nil {
					b.Fatal(err)
				}
				service, err := catalog.New(catalog.Dependencies{
					Repository: repository, Clock: benchmarkCatalogClock{},
				})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := service.Snapshot(context.Background(), catalog.ScopeAll); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The comparison retains the previous desktop composition without default
// models or functional plans. Both sides discover the same current Case,
// Suite and performance-preset files; no provider requests or databases exist.
func benchmarkCatalog(root string, unified bool) (Catalog, error) {
	if unified {
		return OpenCatalog(root, CatalogOptions{})
	}
	models, err := modelcatalog.New(filepath.Join(root, "models.json"))
	if err != nil {
		return Catalog{}, err
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		return Catalog{}, err
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"), plancatalog.WithBuiltin(performanceplans.Bundle))
	if err != nil {
		return Catalog{}, err
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: casebundle.Bundle, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		return Catalog{}, err
	}
	suites, err := suitecatalog.New(suitecatalog.Options{
		Builtin: suitebundle.Bundle, UserRoot: filepath.Join(root, "suites"),
	})
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{Models: models, Channels: channels, Plans: plans, Cases: cases, Suites: suites}, nil
}
