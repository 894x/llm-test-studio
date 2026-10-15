package runnerinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const serviceUser = "llm-test-runner"

type localSystem struct{ layout layout }

func (system localSystem) Preflight(ctx context.Context) error {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return ErrUnsupported
	}
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		return ErrUnsupported
	}
	version, err := command(ctx, "systemctl", "--version")
	if err != nil {
		return ErrUnsupported
	}
	fields := strings.Fields(string(version))
	if len(fields) < 2 || fields[0] != "systemd" {
		return ErrUnsupported
	}
	number, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil || number < 249 {
		return ErrUnsupported
	}
	if _, err := exec.LookPath("runuser"); err != nil {
		return ErrUnsupported
	}
	watchdogs, err := command(ctx, "systemctl", "service-watchdogs")
	if err != nil || strings.TrimSpace(string(watchdogs)) != "yes" {
		return ErrUnsupported
	}
	return nil
}

func (system localSystem) Account(ctx context.Context) (account, error) {
	entry, err := user.Lookup(serviceUser)
	var missing user.UnknownUserError
	if errors.As(err, &missing) {
		_, createErr := command(
			ctx,
			"useradd",
			"--system",
			"--user-group",
			"--no-create-home",
			"--home-dir",
			system.layout.state,
			"--shell",
			"/usr/sbin/nologin",
			serviceUser,
		)
		if createErr != nil {
			return account{}, ErrUnownedPath
		}
		entry, err = user.Lookup(serviceUser)
	}
	if err != nil {
		return account{}, ErrUnownedPath
	}
	uid, uidErr := strconv.ParseUint(entry.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(entry.Gid, 10, 32)
	valid := uidErr == nil && gidErr == nil && uid != 0 && gid != 0 &&
		entry.Username == serviceUser && entry.HomeDir == system.layout.state
	if !valid {
		return account{}, ErrUnownedPath
	}
	group, err := user.LookupGroup(serviceUser)
	if err != nil || group.Gid != entry.Gid {
		return account{}, ErrUnownedPath
	}
	groups, err := entry.GroupIds()
	validGroups := err == nil && len(groups) == 1
	if validGroups {
		validGroups = groups[0] == entry.Gid
	}
	if !validGroups {
		return account{}, ErrUnownedPath
	}
	document, err := os.ReadFile("/etc/passwd")
	if err != nil || len(document) > 1<<20 {
		return account{}, ErrUnownedPath
	}
	for _, line := range strings.Split(string(document), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != serviceUser {
			continue
		}
		if fields[6] != "/usr/sbin/nologin" {
			return account{}, ErrUnownedPath
		}
		return account{uid: int(uid), gid: int(gid)}, nil
	}
	return account{}, ErrUnownedPath
}

func (system localSystem) Initialize(ctx context.Context, input initializeInput) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	process := exec.CommandContext(
		ctx,
		"runuser",
		"-u",
		serviceUser,
		"--",
		input.program,
		"init",
		"--state",
		system.layout.state,
		"--port",
		strconv.Itoa(int(input.port)),
	)
	process.Stdin = bytes.NewReader(input.token)
	process.Stdout, process.Stderr = io.Discard, io.Discard
	process.WaitDelay = 2 * time.Second
	if err := process.Run(); err != nil {
		return ErrService
	}
	return nil
}

func (system localSystem) Start(ctx context.Context) error {
	_, active := command(
		ctx,
		"systemctl",
		"is-active",
		"--quiet",
		ServiceName,
	)
	_, enabled := command(
		ctx,
		"systemctl",
		"is-enabled",
		"--quiet",
		ServiceName,
	)
	if active == nil && enabled == nil {
		return system.verifyPolicy(ctx, true)
	}
	if _, err := command(ctx, "systemctl", "daemon-reload"); err != nil {
		return ErrService
	}
	if err := system.verifyPolicy(ctx, active == nil); err != nil {
		return err
	}
	if _, err := command(
		ctx,
		"systemctl",
		"enable",
		"--now",
		ServiceName,
	); err != nil {
		return ErrService
	}
	return nil
}

func (system localSystem) Verify(ctx context.Context, input verificationInput) error {
	if err := system.verifyPolicy(ctx, true); err != nil {
		return err
	}
	document, err := command(
		ctx,
		"systemctl",
		"show",
		ServiceName,
		"--property=MainPID,ActiveState,SubState,UnitFileState,User,Group,FragmentPath",
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
	correctUnit := fields["FragmentPath"] == system.layout.unit && fields["UnitFileState"] == "enabled"
	correctUser := fields["User"] == serviceUser && fields["Group"] == serviceUser
	active := fields["ActiveState"] == "active" && fields["SubState"] == "running"
	validService := correctUnit && correctUser && active
	if !validService {
		return ErrService
	}
	pid, err := strconv.ParseUint(fields["MainPID"], 10, 32)
	if err != nil || pid < 2 {
		return ErrService
	}
	processPath := "/proc/" + strconv.FormatUint(pid, 10)
	info, err := os.Stat(processPath)
	if err != nil {
		return ErrService
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(input.account.uid) {
		return ErrIntegrity
	}
	status, err := os.ReadFile(filepath.Join(processPath, "status"))
	if err != nil || len(status) > MaxRecordBytes {
		return ErrIntegrity
	}
	if err := verifyProcessIDs(status, input.account); err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(processPath, "exe"))
	if err != nil {
		return ErrService
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, maxProgramBytes+1)); err != nil {
		return ErrService
	}
	if hex.EncodeToString(digest.Sum(nil)) != input.buildID {
		return ErrIntegrity
	}
	return nil
}

func (system localSystem) verifyPolicy(ctx context.Context, running bool) error {
	document, err := command(
		ctx,
		"systemctl",
		"show",
		ServiceName,
		"--property=Type,Restart,WatchdogUSec,RestartUSec,StartLimitIntervalUSec,"+
			"StartLimitBurst,NeedDaemonReload,User,Group,WorkingDirectory,LimitCORE,DropInPaths",
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
	return verifyPolicy(fields, system.layout, running)
}

func verifyPolicy(fields map[string]string, paths layout, running bool) error {
	expected := map[string]string{
		"Type": "notify", "Restart": "on-failure", "StartLimitBurst": "5", "NeedDaemonReload": "no",
		"User": serviceUser, "Group": serviceUser, "WorkingDirectory": paths.state, "LimitCORE": "0",
		"DropInPaths": "",
	}
	for key, value := range expected {
		actual, present := fields[key]
		if !present || actual != value {
			return ErrService
		}
	}
	durations := map[string]time.Duration{
		"RestartUSec": 5 * time.Second, "StartLimitIntervalUSec": 5 * time.Minute,
	}
	// systemd exposes the effective timeout of the last started invocation.
	// Before its first start it is infinity, even with WatchdogSec=30s loaded.
	// checkUnit validates the configured value; active verification stays strict.
	unstartedWatchdog := !running && fields["WatchdogUSec"] == "infinity"
	if !unstartedWatchdog {
		durations["WatchdogUSec"] = 30 * time.Second
	}
	for key, duration := range durations {
		value := strings.ReplaceAll(strings.Join(strings.Fields(fields[key]), ""), "min", "m")
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed != duration {
			return ErrService
		}
	}
	return nil
}

func verifyProcessIDs(document []byte, expected account) error {
	seen := map[string]bool{}
	for _, line := range strings.Split(string(document), "\n") {
		key, value, present := strings.Cut(line, ":")
		identityField := key == "Uid" || key == "Gid" || key == "Groups"
		if !present || !identityField {
			continue
		}
		if seen[key] {
			return ErrIntegrity
		}
		seen[key] = true
		fields := strings.Fields(value)
		count, id := 4, expected.uid
		if key != "Uid" {
			id = expected.gid
		}
		validCount := len(fields) == count
		if key == "Groups" {
			validCount = len(fields) <= 1
		}
		if !validCount {
			return ErrIntegrity
		}
		for _, actual := range fields {
			if actual != strconv.Itoa(id) {
				return ErrIntegrity
			}
		}
	}
	if len(seen) != 3 {
		return ErrIntegrity
	}
	return nil
}

func command(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	output := &limitedOutput{limit: MaxRecordBytes}
	process := exec.CommandContext(ctx, name, arguments...)
	process.Stdout, process.Stderr = output, io.Discard
	process.WaitDelay = 2 * time.Second
	if err := process.Run(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

type limitedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (output *limitedOutput) Write(document []byte) (int, error) {
	if len(document) > output.limit-output.buffer.Len() {
		return 0, ErrIntegrity
	}
	return output.buffer.Write(document)
}
