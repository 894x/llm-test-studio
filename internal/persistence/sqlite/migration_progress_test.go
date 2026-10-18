package sqlite

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestMigrationReportsRealStagesForFreshUpgradeAndRepeatOpen(t *testing.T) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, historical := range []bool{false, true} {
		name := "fresh"
		path := filepath.Join(t.TempDir(), "workspace.db")
		if historical {
			name = "upgrade"
			path = historicalMigrationFixture(t, catalog.Baselines[0])
		}
		t.Run(name, func(t *testing.T) {
			var stages []string
			options := MigrateOptions{AppVersion: "progress-test", Progress: func(progress MigrationProgress) {
				stages = append(stages, progress.Stage)
				if progress.ToVersion != CurrentSchemaVersion {
					t.Error("invalid target version")
				}
			}}
			if err := Migrate(t.Context(), path, options); err != nil {
				t.Fatal(err)
			}
			want := []string{"checking", "initializing", "validating", "completed"}
			if historical {
				want = []string{"checking", "checking", "backing_up", "migrating", "validating", "migrating", "validating", "completed"}
			}
			if !slices.Equal(stages, want) {
				t.Fatalf("stages = %v, want %v", stages, want)
			}
			stages = nil
			if err := Migrate(t.Context(), path, options); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(stages, []string{"checking", "validating", "completed"}) {
				t.Fatalf("repeat open stages = %v", stages)
			}
		})
	}
}
