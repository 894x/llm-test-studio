package ssh

import (
	"context"
	"errors"
	"io"

	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

type privilegeMode uint8

const (
	privilegeUnknown privilegeMode = iota
	privilegeRoot
	privilegeSudo
	privilegeDenied
)

const sudoPrefix = "/usr/bin/sudo -n -- "

// Only fixed Linux distribution paths are considered. Both the executable and
// its parents must remain owned by root and unavailable for unprivileged writes.
const sudoSFTPScript = `set -eu
trusted_path() {
  current="$1"
  while :; do
    [ ! -L "$current" ] || return 1
    owner=$(/usr/bin/stat -c %u -- "$current") || return 1
    mode=$(/usr/bin/stat -c %a -- "$current") || return 1
    [ "$owner" = 0 ] || return 1
    case "$mode" in ""|*[!0-7]*) return 1;; esac
    [ "$((0$mode & 022))" = 0 ] || return 1
    [ "$current" != / ] || return 0
    current="${current%/*}"
    [ -n "$current" ] || current=/
    [ -d "$current" ] || return 1
  done
}
for executable in /usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server /usr/lib/ssh/sftp-server; do
  if [ -f "$executable" ] && [ -x "$executable" ] && trusted_path "$executable"; then
    exec "$executable"
  fi
done
exit 78
`

const sudoSFTPCommand = sudoPrefix + "/bin/sh -c '" + sudoSFTPScript + "'"

func (client *Client) administrativeMode(ctx context.Context) (privilegeMode, error) {
	if client == nil || ctx == nil {
		return privilegeUnknown, ErrInvalid
	}
	client.mu.Lock()
	mode, closed := client.privilege, client.closed
	client.mu.Unlock()
	if closed {
		return privilegeUnknown, ErrConnection
	}
	if mode == privilegeUnknown {
		if _, err := client.Probe(ctx); err != nil {
			return privilegeUnknown, err
		}
		client.mu.Lock()
		mode = client.privilege
		client.mu.Unlock()
	}
	if mode != privilegeRoot && mode != privilegeSudo {
		return mode, runnerinstall.ErrRootRequired
	}
	return mode, nil
}

func (client *Client) administrativeCommand(ctx context.Context, command string) (string, error) {
	mode, err := client.administrativeMode(ctx)
	if err != nil {
		return "", err
	}
	if mode == privilegeSudo {
		return sudoPrefix + command, nil
	}
	return command, nil
}

func (client *Client) sudoSFTP(ctx context.Context) (*sftp.Client, func(), error) {
	session, err := client.connection.NewSession()
	if err != nil {
		return nil, nil, connectionError(ctx, err)
	}
	input, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, connectionError(ctx, err)
	}
	output, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, connectionError(ctx, err)
	}
	session.Stderr = io.Discard
	if err := session.Start(sudoSFTPCommand); err != nil {
		_ = session.Close()
		return nil, nil, connectionError(ctx, err)
	}
	remote, err := sftp.NewClientPipe(
		output,
		input,
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(16),
	)
	if err != nil {
		_ = session.Close()
		waitErr := session.Wait()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		var exited *cryptossh.ExitError
		if errors.As(waitErr, &exited) {
			switch exited.ExitStatus() {
			case 78:
				return nil, nil, ErrUnsupported
			case 1:
				return nil, nil, runnerinstall.ErrRootRequired
			}
		}
		return nil, nil, connectionError(ctx, err)
	}
	return remote, func() {
		_ = remote.Close()
		_ = session.Close()
		_ = session.Wait()
	}, nil
}
