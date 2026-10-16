package runnerinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func (paths layout) uninstallManifest(state *runnerstate.State, request UninstallRequest) (UninstallManifest, error) {
	if err := paths.checkProgramRoot(state); err != nil {
		return UninstallManifest{}, err
	}
	selected, err := paths.currentBuild()
	if err != nil || selected != request.ExpectedBuildID {
		return UninstallManifest{}, ErrConflict
	}
	unit, err := paths.checkUnit()
	if err != nil || !unit {
		return UninstallManifest{}, ErrUnownedPath
	}
	// A managed header alone is insufficient authority to delete a modified unit.
	digest := sha256.Sum256(paths.unitBytes())
	manifest := UninstallManifest{BuildIDs: []string{}, UnitSHA256: hex.EncodeToString(digest[:])}
	manifest.OwnerSHA256, err = digestOwned(filepath.Join(paths.program, ".owner.json"), os.Geteuid(), false)
	if err != nil {
		return UninstallManifest{}, err
	}
	entries, err := boundedEntries(filepath.Join(paths.program, "releases"))
	if err != nil || len(entries) == 0 || len(entries) > 128 {
		return UninstallManifest{}, ErrUnownedPath
	}
	for _, entry := range entries {
		manifest.BuildIDs = append(manifest.BuildIDs, entry.Name())
	}
	slices.Sort(manifest.BuildIDs)
	if err := paths.verifyUninstallFiles(manifest, request.ExpectedBuildID, false); err != nil {
		return UninstallManifest{}, err
	}
	return manifest, nil
}

func removalDirectory(name string) error {
	if err := trustedDirectory(name, os.Geteuid(), false); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if err != nil || info.Mode().Perm() != 0755 {
		return ErrUnownedPath
	}
	return nil
}

func removalFile(name, digest string, mode os.FileMode, partial bool) error {
	actual, err := digestOwned(name, os.Geteuid(), false)
	if errors.Is(err, os.ErrNotExist) && partial {
		return nil
	}
	if err != nil || actual != digest {
		return ErrUnownedPath
	}
	info, err := os.Lstat(name)
	if err != nil || info.Mode().Perm() != mode {
		return ErrUnownedPath
	}
	return nil
}

func (paths layout) verifyUninstallFiles(manifest UninstallManifest, selected string, partial bool) error {
	if err := paths.verifyBases(); err != nil {
		return err
	}
	if err := removalFile(
		paths.unit,
		manifest.UnitSHA256,
		0644,
		partial,
	); err != nil {
		return err
	}
	if _, err := os.Lstat(paths.program); errors.Is(err, os.ErrNotExist) && partial {
		return nil
	}
	if err := removalDirectory(paths.program); err != nil {
		return err
	}
	entries, err := boundedEntries(paths.program)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !slices.Contains([]string{".owner.json", "current", "releases"}, entry.Name()) {
			return ErrUnownedPath
		}
	}
	owner := filepath.Join(paths.program, ".owner.json")
	if _, err := os.Lstat(owner); errors.Is(err, os.ErrNotExist) && partial && len(entries) == 0 {
		return nil
	}
	if err := removalFile(
		owner,
		manifest.OwnerSHA256,
		0644,
		false,
	); err != nil {
		return err
	}
	current := filepath.Join(paths.program, "current")
	info, err := os.Lstat(current)
	if err == nil {
		metadata, ok := info.Sys().(*syscall.Stat_t)
		if !ok || metadata.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink == 0 {
			return ErrUnownedPath
		}
		target, err := os.Readlink(current)
		if err != nil || target != "releases/"+selected || verifyProgram(paths.programPath(selected), selected) != nil {
			return ErrUnownedPath
		}
	} else if !errors.Is(err, os.ErrNotExist) || !partial {
		return ErrUnownedPath
	}
	releases := filepath.Join(paths.program, "releases")
	if _, err := os.Lstat(releases); errors.Is(err, os.ErrNotExist) && partial {
		return nil
	}
	if err := removalDirectory(releases); err != nil {
		return err
	}
	builds, err := boundedEntries(releases)
	if err != nil || !partial && len(builds) != len(manifest.BuildIDs) {
		return ErrUnownedPath
	}
	for _, build := range builds {
		if !slices.Contains(manifest.BuildIDs, build.Name()) {
			return ErrUnownedPath
		}
		directory := filepath.Join(releases, build.Name())
		if err := removalDirectory(directory); err != nil {
			return err
		}
		files, err := boundedEntries(directory)
		if err != nil || len(files) > 1 || !partial && len(files) != 1 {
			return ErrUnownedPath
		}
		for _, file := range files {
			if file.Name() != "llm-test-runner" {
				return ErrUnownedPath
			}
			if err := removalFile(
				filepath.Join(directory, file.Name()),
				build.Name(),
				0755,
				false,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (installer uninstaller) removePrograms(ctx context.Context, operation UninstallOperation) error {
	paths := installer.layout
	if err := paths.verifyUninstallFiles(operation.Manifest, operation.Request.ExpectedBuildID, true); err != nil {
		return err
	}
	// Enumerate only the frozen manifest. Remove never descends into a directory;
	// an unexpected child causes ENOTEMPTY and remains untouched.
	names := []string{paths.unit, filepath.Join(paths.program, "current")}
	for _, build := range operation.Manifest.BuildIDs {
		names = append(names, paths.programPath(build), filepath.Dir(paths.programPath(build)))
	}
	names = append(
		names,
		filepath.Join(paths.program, "releases"),
		filepath.Join(paths.program, ".owner.json"),
		paths.program,
	)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := installer.remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(filepath.Dir(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
