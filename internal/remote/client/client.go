// Package client implements the current runner management protocol. Its caller
// owns the SSH connection; ending an HTTP request never cancels accepted jobs.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

var (
	ErrInvalid    = errors.New("invalid_remote_request")
	ErrIntegrity  = errors.New("remote_response_integrity_failed")
	ErrConnection = errors.New("remote_connection_failed")
)

type Identity struct {
	NodeID         string
	InstallationID string
}

type Transport func(context.Context, remotessh.Request) (*http.Response, error)

type Client struct {
	identity Identity
	do       Transport
}

// RemoteError retains only a fixed public code and retry advice. Server message
// text and response bodies never become errors or diagnostics.
type RemoteError struct {
	Code      string
	Retryable bool
}

func (err *RemoteError) Error() string { return "remote_runner_" + err.Code }

func New(identity Identity, transport Transport) (*Client, error) {
	validIdentity := domain.IsUUID(identity.NodeID) && domain.IsUUID(identity.InstallationID)
	if !validIdentity || transport == nil {
		return nil, ErrInvalid
	}
	return &Client{identity: identity, do: transport}, nil
}

func (client *Client) Probe(ctx context.Context) (jobcontract.RunnerInfo, error) {
	raw, err := client.json(ctx, remotessh.Request{
		Method: http.MethodGet, Route: "/runner",
	}, jobcontract.MaxRunnerInfoBytes)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	info, err := jobcontract.DecodeRunnerInfo(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.RunnerInfo{}, responseError(err)
	}
	if !slices.Contains(info.Capabilities.Features, jobcontract.FeatureExecutionOrigin) {
		return jobcontract.RunnerInfo{}, jobcontract.ErrUnsupported
	}
	owned := info.NodeID == client.identity.NodeID && info.NodeInstallationID == client.identity.InstallationID
	if !owned {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	return info, nil
}

func (client *Client) Submit(ctx context.Context, submission *jobcontract.Submission) (jobcontract.Receipt, error) {
	if submission == nil {
		return jobcontract.Receipt{}, ErrInvalid
	}
	if err := submission.Validate(); err != nil {
		return jobcontract.Receipt{}, err
	}
	spec := submission.Job
	owned := spec.Target.Kind == jobcontract.TargetRemote && spec.Target.NodeID == client.identity.NodeID &&
		spec.NodeInstallationID == client.identity.InstallationID
	if !owned {
		return jobcontract.Receipt{}, ErrInvalid
	}
	document, err := json.Marshal(submission)
	if err != nil || len(document) > jobcontract.MaxSubmissionBytes {
		clear(document)
		return jobcontract.Receipt{}, ErrInvalid
	}
	body := &submissionBody{document: document}
	defer body.Close()
	raw, err := client.json(ctx, remotessh.Request{
		Method: http.MethodPost, Route: "/jobs", Body: body,
	}, jobcontract.MaxStateBytes)
	if err != nil {
		return jobcontract.Receipt{}, err
	}
	receipt, err := jobcontract.DecodeReceipt(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.Receipt{}, responseError(err)
	}
	expected, err := spec.InitialSnapshot(receipt.AcceptedAt, receipt.Origin)
	if err != nil {
		return jobcontract.Receipt{}, ErrIntegrity
	}
	want := expected.Receipt
	matching := receipt.JobID == want.JobID && receipt.Kind == want.Kind &&
		receipt.Origin == want.Origin
	children := slices.Equal(receipt.RunIDs, want.RunIDs) && slices.Equal(receipt.ReportIDs, want.ReportIDs) &&
		receipt.ComparisonID == want.ComparisonID
	if !matching || !children {
		return jobcontract.Receipt{}, ErrIntegrity
	}
	return receipt, nil
}

func (client *Client) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	if !domain.IsUUID(id) {
		return jobcontract.Snapshot{}, ErrInvalid
	}
	raw, err := client.json(ctx, remotessh.Request{
		Method: http.MethodGet, Route: "/jobs/" + id,
	}, jobcontract.MaxStateBytes)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	snapshot, err := jobcontract.DecodeSnapshot(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.Snapshot{}, responseError(err)
	}
	if snapshot.Receipt.JobID != id || !client.owns(snapshot) {
		return jobcontract.Snapshot{}, ErrIntegrity
	}
	return snapshot, nil
}

func (client *Client) Command(
	ctx context.Context,
	command jobcontract.Command,
) (jobcontract.CommandConfirmation, error) {
	if err := command.Validate(); err != nil {
		return jobcontract.CommandConfirmation{}, err
	}
	suffix := "/cancel"
	if command.Kind == jobcontract.CommandStopSending {
		suffix = "/stop-sending"
	}
	document, err := json.Marshal(command)
	if err != nil {
		return jobcontract.CommandConfirmation{}, ErrInvalid
	}
	raw, err := client.json(ctx, remotessh.Request{
		Method: http.MethodPost, Route: "/jobs/" + command.JobID + suffix, Body: bytes.NewReader(document),
	}, jobcontract.MaxStateBytes)
	if err != nil {
		return jobcontract.CommandConfirmation{}, err
	}
	confirmation, err := jobcontract.DecodeCommandConfirmation(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.CommandConfirmation{}, responseError(err)
	}
	if confirmation.Command != command || !client.owns(confirmation.Snapshot) {
		return jobcontract.CommandConfirmation{}, ErrIntegrity
	}
	return confirmation, nil
}

func (client *Client) owns(snapshot jobcontract.Snapshot) bool {
	origin := snapshot.Receipt.Origin
	return origin.Kind == string(jobcontract.TargetRemote) && origin.NodeID == client.identity.NodeID &&
		origin.InstallationID == client.identity.InstallationID
}

func (client *Client) json(ctx context.Context, input remotessh.Request, limit int64) ([]byte, error) {
	input.JSONGzip = true
	response, close, err := client.open(ctx, input, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer close()
	validStatus := response.StatusCode == http.StatusOK ||
		(input.Method == http.MethodPost && input.Route == "/jobs" && response.StatusCode == http.StatusAccepted)
	if !validStatus {
		err := decodeError(response)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	raw, err := readJSON(response, limit)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return raw, err
}

func (client *Client) open(
	ctx context.Context,
	input remotessh.Request,
	timeout time.Duration,
) (*http.Response, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalid
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	response, err := client.do(requestCtx, input)
	if err != nil {
		cancel()
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if requestCtx.Err() != nil && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, ErrConnection
	}
	if response == nil || response.Body == nil {
		cancel()
		return nil, nil, ErrIntegrity
	}
	return response, func() { _ = response.Body.Close(); cancel() }, nil
}

func identityEncoding(response *http.Response) bool {
	values := response.Header.Values("Content-Encoding")
	return len(values) == 0 || (len(values) == 1 && values[0] == "identity")
}

func responseError(err error) error {
	if errors.Is(err, jobcontract.ErrUnsupported) {
		return jobcontract.ErrUnsupported
	}
	return ErrIntegrity
}

// net/http may still be closing a request body when Do returns. Serialize reads
// with cleanup so early HTTP rejection cannot race erasure of credential bytes.
type submissionBody struct {
	mu       sync.Mutex
	document []byte
	offset   int
}

func (*submissionBody) String() string               { return "runner_submission_body{redacted}" }
func (*submissionBody) GoString() string             { return "runner_submission_body{redacted}" }
func (*submissionBody) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

func (body *submissionBody) Read(destination []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.offset == len(body.document) {
		return 0, io.EOF
	}
	count := copy(destination, body.document[body.offset:])
	body.offset += count
	return count, nil
}

func (body *submissionBody) Close() error {
	body.mu.Lock()
	defer body.mu.Unlock()
	clear(body.document)
	body.document, body.offset = []byte{}, 0
	return nil
}
