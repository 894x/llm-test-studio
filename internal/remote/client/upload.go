package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"slices"
	"sync"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

const maxEmptyUploadReads = 100

// UploadContent takes ownership of source on entry and closes it on every exit.
// source must support concurrent Close that promptly unblocks a pending Read.
// A lost acknowledgement is returned to the caller; retry requires a fresh,
// complete source for the same identity and never submits a job.
func (client *Client) UploadContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.ReadCloser,
) (receipt jobcontract.AssetReceipt, err error) {
	if source == nil {
		return jobcontract.AssetReceipt{}, ErrInvalid
	}
	body := &uploadSource{source: source, identity: identity, digest: sha256.New()}
	defer func() {
		if closeErr := body.Close(); err == nil && closeErr != nil {
			receipt, err = jobcontract.AssetReceipt{}, jobcontract.ErrAssetRead
		}
	}()
	if client == nil || ctx == nil || identity.Validate() != nil {
		return jobcontract.AssetReceipt{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, jobcontract.AssetUploadTimeout)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = body.Close()
		close(closed)
	})
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
	}()
	info, err := client.Probe(ctx)
	if err != nil {
		return jobcontract.AssetReceipt{}, err
	}
	if !slices.Contains(info.Capabilities.Features, jobcontract.FeatureContentUpload) {
		return jobcontract.AssetReceipt{}, jobcontract.ErrUnsupported
	}
	response, closeResponse, err := client.open(ctx, remotessh.Request{
		Method: http.MethodPut, Route: "/assets/" + identity.SHA256, Body: body, Content: &identity,
	}, jobcontract.AssetUploadTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return jobcontract.AssetReceipt{}, ctx.Err()
		}
		if sourceErr := body.failure(); sourceErr != nil {
			return jobcontract.AssetReceipt{}, sourceErr
		}
		return jobcontract.AssetReceipt{}, err
	}
	defer closeResponse()
	if !identityEncoding(response) || response.Uncompressed {
		return jobcontract.AssetReceipt{}, ErrIntegrity
	}
	validStatus := response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated
	if !validStatus {
		return jobcontract.AssetReceipt{}, uploadError(ctx, decodeError(response))
	}
	raw, err := readJSON(response, jobcontract.MaxAssetReceiptBytes)
	if err != nil {
		return jobcontract.AssetReceipt{}, uploadError(ctx, err)
	}
	receipt, err = jobcontract.DecodeAssetReceipt(bytes.NewReader(raw))
	if err != nil {
		return jobcontract.AssetReceipt{}, responseError(err)
	}
	owned := receipt.NodeID == client.identity.NodeID &&
		receipt.NodeInstallationID == client.identity.InstallationID
	matching := receipt.SHA256 == identity.SHA256 && receipt.Size == identity.Size
	if !owned || !matching {
		return jobcontract.AssetReceipt{}, ErrIntegrity
	}
	if err := body.verify(); err != nil {
		return jobcontract.AssetReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return jobcontract.AssetReceipt{}, err
	}
	return receipt, nil
}

func uploadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// The source owns cancellation, rather than a goroutine trying to interrupt an
// arbitrary Reader. Closing joins reads before returning the source lease.
type uploadSource struct {
	source   io.ReadCloser
	identity jobcontract.ContentIdentity
	mu       sync.Mutex
	closed   bool
	reads    sync.WaitGroup
	stats    sync.Mutex
	digest   hash.Hash
	count    int64
	failed   bool
	eof      bool
	empty    int
	once     sync.Once
	closeErr error
}

func (body *uploadSource) Read(destination []byte) (int, error) {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	body.reads.Add(1)
	body.mu.Unlock()
	defer body.reads.Done()
	body.stats.Lock()
	remaining, eof := body.identity.Size-body.count, body.eof
	body.stats.Unlock()
	if eof {
		return 0, io.EOF
	}
	if remaining <= 0 {
		return 0, jobcontract.ErrAssetIntegrity
	}
	if int64(len(destination)) > remaining {
		destination = destination[:remaining]
	}
	count, err := body.source.Read(destination)
	count, err = body.record(destination, count, err)
	if err != nil || int64(count) != remaining {
		return count, err
	}
	// Withhold the final declared bytes until a bounded EOF probe completes.
	// An early HTTP acknowledgement cannot race the transport's later probe.
	return count, body.probeEnd()
}

func (body *uploadSource) record(destination []byte, count int, err error) (int, error) {
	body.stats.Lock()
	defer body.stats.Unlock()
	if count < 0 || count > len(destination) {
		body.failed = true
		return 0, jobcontract.ErrAssetRead
	}
	if count == 0 && err == nil {
		body.empty++
		if body.empty >= maxEmptyUploadReads {
			body.failed = true
			return 0, jobcontract.ErrAssetRead
		}
	} else {
		body.empty = 0
	}
	body.count += int64(count)
	_, _ = body.digest.Write(destination[:count])
	if err != nil && err != io.EOF {
		body.failed = true
		err = jobcontract.ErrAssetRead
	}
	body.eof = body.eof || err == io.EOF
	return count, err
}

func (body *uploadSource) probeEnd() error {
	var extra [1]byte
	for {
		count, err := body.source.Read(extra[:])
		count, err = body.record(extra[:], count, err)
		if count > 0 {
			return jobcontract.ErrAssetIntegrity
		}
		if err != nil {
			return err
		}
	}
}

func (body *uploadSource) Close() error {
	body.once.Do(func() {
		body.mu.Lock()
		body.closed = true
		body.mu.Unlock()
		body.closeErr = body.source.Close()
		body.reads.Wait()
	})
	return body.closeErr
}

func (body *uploadSource) failure() error {
	body.stats.Lock()
	defer body.stats.Unlock()
	if body.failed {
		return jobcontract.ErrAssetRead
	}
	wrongLength := body.count > body.identity.Size || body.eof && body.count != body.identity.Size
	if wrongLength {
		return jobcontract.ErrAssetIntegrity
	}
	return nil
}

func (body *uploadSource) verify() error {
	body.stats.Lock()
	defer body.stats.Unlock()
	if body.failed {
		return jobcontract.ErrAssetRead
	}
	matching := body.eof && body.count == body.identity.Size &&
		hex.EncodeToString(body.digest.Sum(nil)) == body.identity.SHA256
	if !matching {
		return jobcontract.ErrAssetIntegrity
	}
	return nil
}
