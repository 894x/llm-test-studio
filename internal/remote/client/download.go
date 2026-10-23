package client

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

// DownloadReport resumes only against the supplied immutable reference. The
// caller owns its private partial file and publishes it only after nil return.
// Download time never changes metrics in the stored report.
func (client *Client) DownloadReport(
	ctx context.Context,
	jobID string,
	reference jobcontract.ReportReference,
	file io.ReadWriteSeeker,
) error {
	return client.downloadReport(ctx, reportRequest{JobID: jobID, Reference: reference, File: file, Route: "reports"})
}

// DownloadPerformanceArchive retrieves the statistical archive referenced by an
// EventReport. Full execution results use DownloadReport with their own digest.
func (client *Client) DownloadPerformanceArchive(
	ctx context.Context,
	jobID string,
	reference jobcontract.ReportReference,
	file io.ReadWriteSeeker,
) error {
	if reference.Kind != jobcontract.KindPerformance || reference.RunID != "" {
		return ErrInvalid
	}
	return client.downloadReport(ctx, reportRequest{JobID: jobID, Reference: reference, File: file, Route: "archives"})
}

type reportRequest struct {
	JobID     string
	Reference jobcontract.ReportReference
	File      io.ReadWriteSeeker
	Route     string
}

func (client *Client) downloadReport(ctx context.Context, download reportRequest) error {
	jobID, reference, file := download.JobID, download.Reference, download.File
	validIdentity := ctx != nil && domain.IsUUID(jobID) && domain.IsUUID(reference.ID) && file != nil
	limit := int64(jobcontract.MaxRunReportBytes)
	if reference.Kind == jobcontract.KindPerformance && download.Route == "reports" {
		limit = jobcontract.MaxPerformanceResultBytes
	}
	validKind := reference.Kind == jobcontract.KindRun || reference.Kind == jobcontract.KindPerformance
	validBytes := jobcontract.IsSHA256(reference.SHA256) && reference.Size > 0 && reference.Size <= limit
	if !validIdentity || !validKind || !validBytes {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil || offset < 0 || offset > reference.Size {
		return ErrInvalid
	}
	if offset == reference.Size {
		return verifyDownload(file, reference)
	}
	input := remotessh.Request{
		Method: http.MethodGet, Route: "/jobs/" + jobID + "/" + download.Route + "/" + reference.ID,
		JSONGzip: offset == 0,
	}
	if offset > 0 {
		input.RangeStart = &offset
	}
	response, close, err := client.open(ctx, input, 2*time.Minute)
	if err != nil {
		return err
	}
	defer close()
	expectedStatus := http.StatusOK
	if offset > 0 {
		expectedStatus = http.StatusPartialContent
	}
	if response.StatusCode != expectedStatus {
		err := decodeError(response)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	remaining := reference.Size - offset
	encoding := response.Header.Values("Content-Encoding")
	compressed := len(encoding) == 1 && strings.EqualFold(encoding[0], "gzip")
	validEncoding := identityEncoding(response) || compressed && offset == 0 && !response.Uncompressed
	validDigest := oneHeader(response, "X-Content-SHA256", reference.SHA256) &&
		oneHeader(response, "ETag", `"`+reference.SHA256+`"`)
	validResponse := oneHeader(response, "X-LLM-Test-Report-Kind", string(reference.Kind)) &&
		oneHeader(response, "Content-Type", "application/json") && validEncoding
	wireLimit := remaining
	validLength := response.ContentLength == remaining
	if compressed {
		wireLimit += gzipWireAllowance
		validLength = response.ContentLength >= -1 && response.ContentLength <= wireLimit
	}
	if !validDigest || !validResponse || !validLength {
		return ErrIntegrity
	}
	if offset > 0 {
		expectedRange := "bytes " + strconv.FormatInt(offset, 10) + "-" +
			strconv.FormatInt(reference.Size-1, 10) + "/" + strconv.FormatInt(reference.Size, 10)
		if !oneHeader(response, "Content-Range", expectedRange) {
			return ErrIntegrity
		}
	} else if response.Header.Get("Content-Range") != "" {
		return ErrIntegrity
	}
	if err := copyReportBody(file, response.Body, remaining, wireLimit, compressed); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return verifyDownload(file, reference)
}

func copyReportBody(
	destination io.Writer,
	body io.Reader,
	remaining int64,
	wireLimit int64,
	compressed bool,
) error {
	source := body
	var wire *bufio.Reader
	if compressed {
		wire = bufio.NewReader(io.LimitReader(body, wireLimit+1))
		reader, err := gzip.NewReader(wire)
		if err != nil {
			return ErrIntegrity
		}
		defer reader.Close()
		reader.Multistream(false)
		source = reader
	}
	if _, err := io.CopyN(destination, source, remaining); err != nil {
		return ErrConnection
	}
	var extra [1]byte
	if count, err := source.Read(extra[:]); count != 0 || err != io.EOF {
		return ErrIntegrity
	}
	if compressed {
		if count, err := wire.Read(extra[:]); count != 0 || err != io.EOF {
			return ErrIntegrity
		}
	}
	return nil
}

func oneHeader(response *http.Response, name, expected string) bool {
	values := response.Header.Values(name)
	return len(values) == 1 && values[0] == expected
}

func verifyDownload(file io.ReadWriteSeeker, reference jobcontract.ReportReference) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ErrIntegrity
	}
	digest := sha256.New()
	count, err := io.Copy(digest, io.LimitReader(file, reference.Size+1))
	matching := err == nil && count == reference.Size && hex.EncodeToString(digest.Sum(nil)) == reference.SHA256
	if !matching {
		return ErrIntegrity
	}
	return nil
}
