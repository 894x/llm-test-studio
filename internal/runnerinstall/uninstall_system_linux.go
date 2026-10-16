package runnerinstall

import (
	"context"
	"strings"
)

type localUninstallSystem struct{ localUpgradeSystem }

func (system localUninstallSystem) Disable(ctx context.Context) error {
	if _, err := command(
		ctx,
		"systemctl",
		"disable",
		ServiceName,
	); err != nil {
		return ErrService
	}
	return nil
}

func (system localUninstallSystem) Reload(ctx context.Context) error {
	if _, err := command(ctx, "systemctl", "daemon-reload"); err != nil {
		return ErrService
	}
	return nil
}

func (system localUninstallSystem) VerifyStopped(ctx context.Context) error {
	if err := system.verifyPolicy(ctx, false); err != nil {
		return err
	}
	document, err := command(
		ctx,
		"systemctl",
		"show",
		ServiceName,
		"--property=MainPID,ActiveState,LoadState,FragmentPath,UnitFileState",
	)
	if err != nil {
		return ErrService
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(document)), "\n") {
		key, value, present := strings.Cut(line, "=")
		if !present || fields[key] != "" {
			return ErrService
		}
		fields[key] = value
	}
	return verifyStoppedUninstallUnit(fields, system.layout)
}

func verifyStoppedUninstallUnit(fields map[string]string, paths layout) error {
	inactive := fields["MainPID"] == "0" && fields["ActiveState"] == "inactive"
	managed := fields["LoadState"] == "loaded" && fields["FragmentPath"] == paths.unit
	disposition := fields["UnitFileState"] == "enabled" || fields["UnitFileState"] == "disabled"
	if !inactive || !managed || !disposition {
		return ErrService
	}
	return nil
}

func (system localUninstallSystem) VerifyRemoved(ctx context.Context) error {
	document, err := command(
		ctx,
		"systemctl",
		"show",
		ServiceName,
		"--property=MainPID,ActiveState,LoadState",
	)
	if err != nil {
		return ErrService
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(document), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	if fields["MainPID"] != "0" || fields["ActiveState"] != "inactive" || fields["LoadState"] != "not-found" {
		return ErrService
	}
	return nil
}
