package remotenodes

import (
	"context"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type ArtifactSource interface {
	Open(context.Context, string, string) (*runnerartifacts.Artifact, error)
}

type Pairing struct {
	Identity runnerinstall.Identity
	Runner   jobcontract.RunnerInfo
	Token    *credentials.Lease
}

func (Pairing) String() string               { return "remote_pairing{redacted}" }
func (Pairing) GoString() string             { return "remote_pairing{redacted}" }
func (Pairing) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

type RemoteSession interface {
	Probe(context.Context) (remotessh.SystemInfo, error)
	Shared(context.Context) (Pairing, error)
	Bound(context.Context, Binding, *credentials.Lease) (jobcontract.RunnerInfo, error)
	Provision(
		context.Context,
		runnerinstall.Request,
		remotessh.Artifact,
		func(runnerinstall.Progress),
	) (runnerinstall.Result, error)
	Operation(context.Context, string) (runnerinstall.Operation, error)
	CleanupBootstrap(context.Context, string) error
	Close() error
}

type Connector func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error)

func ConnectSSH(
	ctx context.Context,
	endpoint remotessh.Endpoint,
	authentication remotessh.Authentication,
) (RemoteSession, error) {
	client, err := remotessh.Dial(ctx, endpoint, authentication)
	if err != nil {
		return nil, err
	}
	return &sshSession{client: client}, nil
}

type sshSession struct{ client *remotessh.Client }

func (session *sshSession) Probe(ctx context.Context) (remotessh.SystemInfo, error) {
	return session.client.Probe(ctx)
}

func (session *sshSession) Shared(ctx context.Context) (Pairing, error) {
	node, info, err := session.client.ConnectNode(ctx)
	if err != nil {
		return Pairing{}, err
	}
	defer node.Close()
	token, err := node.PairingLease()
	if err != nil {
		return Pairing{}, err
	}
	return Pairing{Identity: node.Identity(), Runner: info, Token: token}, nil
}

func (session *sshSession) Bound(
	ctx context.Context,
	binding Binding,
	token *credentials.Lease,
) (jobcontract.RunnerInfo, error) {
	node, info, err := session.client.ConnectBoundNode(ctx, binding.Identity, token)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	defer node.Close()
	return info, nil
}

func (session *sshSession) Provision(
	ctx context.Context,
	request runnerinstall.Request,
	artifact remotessh.Artifact,
	progress func(runnerinstall.Progress),
) (runnerinstall.Result, error) {
	return session.client.Provision(
		ctx,
		request,
		artifact,
		progress,
	)
}

func (session *sshSession) Operation(ctx context.Context, id string) (runnerinstall.Operation, error) {
	return session.client.InstallationOperation(ctx, id)
}

func (session *sshSession) CleanupBootstrap(ctx context.Context, directory string) error {
	return session.client.CleanupBootstrap(ctx, directory)
}

func (session *sshSession) Close() error { return session.client.Close() }
