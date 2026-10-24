package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func (client *Client) SetTaskConcurrency(
	ctx context.Context,
	expected uint32,
	limit uint32,
) (jobcontract.RunnerInfo, error) {
	command := jobcontract.TaskConcurrencyCommand{
		Contract: jobcontract.CurrentContract, NodeID: client.identity.NodeID,
		NodeInstallationID: client.identity.InstallationID, ExpectedMaxActiveJobs: expected, MaxActiveJobs: limit,
	}
	if err := command.Validate(); err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	info, err := client.Probe(ctx)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	if !slices.Contains(info.Capabilities.Features, jobcontract.FeatureTaskConcurrency) {
		return jobcontract.RunnerInfo{}, jobcontract.ErrUnsupported
	}
	document, err := json.Marshal(command)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	raw, err := client.json(
		ctx,
		remotessh.Request{Method: http.MethodPost, Route: "/runner/task-concurrency", Body: bytes.NewReader(document)},
		jobcontract.MaxRunnerInfoBytes,
		managementJSONTimeout,
	)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	info, err = jobcontract.DecodeRunnerInfo(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.RunnerInfo{}, responseError(err)
	}
	owned := info.NodeID == client.identity.NodeID && info.NodeInstallationID == client.identity.InstallationID
	if !owned || info.Budget.MaxActiveJobs != limit {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	return info, nil
}
