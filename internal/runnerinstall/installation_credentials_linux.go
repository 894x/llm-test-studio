package runnerinstall

import (
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func (paths layout) credentials(state *runnerstate.State, owner account) (runnerconfig.Config, []byte, error) {
	config, exists, err := paths.readConfig(owner)
	if err != nil || !exists {
		config.Clear()
		return runnerconfig.Config{}, nil, ErrPairingRequired
	}
	fail := func(err error) (runnerconfig.Config, []byte, error) {
		config.Clear()
		return runnerconfig.Config{}, nil, err
	}
	document, err := state.ReadPrivate("identity.json", MaxRequestBytes)
	if err != nil {
		return fail(ErrPairingRequired)
	}
	identity, err := DecodeIdentity(document)
	expected := Identity{
		Contract: config.Contract, NodeID: config.NodeID, InstallationID: config.InstallationID,
		ListenPort: config.ListenPort, TokenSHA256: config.TokenSHA256,
	}
	if err != nil || identity != expected {
		return fail(ErrPairingRequired)
	}
	token, err := state.ReadPrivate(PairingName, 128)
	if err != nil || !tokenMatches(config, token) {
		clear(token)
		return fail(ErrPairingRequired)
	}
	return config, token, nil
}
