package ssh

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
)

type SystemInfo struct {
	UID            uint32 `json:"uid"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	SystemdVersion uint32 `json:"systemd_version"`
	SystemdPID1    bool   `json:"systemd_pid1"`
	HasSHA256      bool   `json:"has_sha256"`
	HasRunuser     bool   `json:"has_runuser"`
}

const systemProbe = `set -eu
printf '%s\n' llm-test-system-probe
id -u
uname -s
uname -m
if command -v systemctl >/dev/null 2>&1; then
  systemctl --version | head -n 1
else
  printf '%s\n' 'systemd 0'
fi
cat /proc/1/comm
if command -v sha256sum >/dev/null 2>&1; then printf 'yes\n'; else printf 'no\n'; fi
if command -v runuser >/dev/null 2>&1; then printf 'yes\n'; else printf 'no\n'; fi
`

// Probe executes a fixed read-only script; no endpoint or user-supplied text is
// interpolated into the remote command. Unsupported systems retain their facts.
func (client *Client) Probe(ctx context.Context) (SystemInfo, error) {
	document, err := client.runScript(ctx, systemProbe)
	if err != nil {
		return SystemInfo{}, err
	}
	defer clear(document)
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	if len(lines) != 8 || lines[0] != "llm-test-system-probe" {
		return SystemInfo{}, ErrUnsupported
	}
	uid, err := strconv.ParseUint(lines[1], 10, 32)
	if err != nil {
		return SystemInfo{}, ErrUnsupported
	}
	versionFields := strings.Fields(lines[4])
	if len(versionFields) < 2 || versionFields[0] != "systemd" {
		return SystemInfo{}, ErrUnsupported
	}
	version, err := strconv.ParseUint(versionFields[1], 10, 32)
	if err != nil {
		return SystemInfo{}, ErrUnsupported
	}
	architecture := lines[3]
	switch architecture {
	case "x86_64":
		architecture = "amd64"
	case "aarch64":
		architecture = "arm64"
	}
	supportedArch := architecture == "amd64" || architecture == "arm64"
	if lines[2] != "Linux" || !supportedArch {
		return SystemInfo{}, ErrUnsupported
	}
	return SystemInfo{
		UID: uint32(uid), OS: "linux", Arch: architecture, SystemdVersion: uint32(version),
		SystemdPID1: lines[5] == "systemd", HasSHA256: lines[6] == "yes", HasRunuser: lines[7] == "yes",
	}, nil
}

// runScript is intentionally private. Installer commands live in this package
// as fixed templates with validated parameters, never as an arbitrary UI command.
func (client *Client) runScript(ctx context.Context, script string) ([]byte, error) {
	if client == nil || ctx == nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = client.connection.Close() })
	defer stop()
	session, err := client.connection.NewSession()
	if err != nil {
		return nil, connectionError(ctx, err)
	}
	defer session.Close()
	output := &boundedOutput{limit: 64 << 10}
	session.Stdout, session.Stderr = output, io.Discard
	session.Stdin = strings.NewReader(script)
	if err := session.Run("/bin/sh -s"); err != nil {
		clear(output.Bytes())
		return nil, connectionError(ctx, err)
	}
	if output.exceeded || ctx.Err() != nil {
		clear(output.Bytes())
		return nil, connectionError(ctx, nil)
	}
	return output.Bytes(), nil
}

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (output *boundedOutput) Bytes() []byte { return output.buffer.Bytes() }

func (output *boundedOutput) Write(document []byte) (int, error) {
	if len(document) > output.limit-output.buffer.Len() {
		output.exceeded = true
		return 0, ErrInvalid
	}
	return output.buffer.Write(document)
}
