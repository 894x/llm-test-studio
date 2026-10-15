package runnerhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) jobs(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(request, "after", "limit") {
		writeOperationError(writer, jobhost.ErrInvalid)
		return
	}
	parameters := request.URL.Query()
	var after uint64
	validAfter := true
	if values, present := parameters["after"]; present {
		after, validAfter = queryNumber(values[0], 63)
	}
	limit, validLimit := queryNumber(parameters.Get("limit"), 32)
	validPage := validAfter && validLimit && limit >= 1 && limit <= 200
	if !validPage {
		writeOperationError(writer, jobhost.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
	defer cancel()
	identity := handler.host.Identity()
	page, err := handler.repository.ListJobs(ctx, jobhost.JobListQuery{
		InstallationID: identity.InstallationID, Target: identity.Target, After: after, Limit: uint32(limit),
	})
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) runReport(writer http.ResponseWriter, request *http.Request, spec jobcontract.JobSpec) {
	ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
	defer cancel()
	document, err := handler.repository.ReadRunReport(ctx, spec.JobID, request.PathValue("report_id"))
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	report, reference := document.Report, document.Reference
	ownedRun := slices.Contains(runIDs(spec), report.RunID) && reference.Kind == jobcontract.KindRun
	matchingIdentity := reference.ID == report.ID && reference.RunID == report.RunID &&
		report.ID == request.PathValue("report_id")
	owned := ownedRun && matchingIdentity
	canonical, err := jobcontract.EncodeRunReport(report)
	digest := sha256.Sum256(document.Document)
	validBytes := err == nil && bytes.Equal(canonical, document.Document) &&
		reference.Size == int64(len(document.Document)) && reference.SHA256 == hex.EncodeToString(digest[:])
	if !owned || !validBytes {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	serveReport(writer, request, reportDownload{
		Document: document.Document, SHA256: reference.SHA256, Kind: jobcontract.KindRun, ModifiedAt: report.GeneratedAt,
	})
}

func runIDs(spec jobcontract.JobSpec) []string {
	ids := []string{}
	for _, input := range spec.PreparedRuns() {
		ids = append(ids, input.Meta.ID)
	}
	return ids
}
