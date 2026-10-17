package runnerhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) performanceArchive(writer http.ResponseWriter, request *http.Request) {
	reportID := request.PathValue("report_id")
	if !validQuery(request) || !domain.IsUUID(reportID) {
		writeOperationError(writer, jobhost.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
	defer cancel()
	_, spec, err := handler.ownJob(ctx, request.PathValue("job_id"))
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	validOwner := spec.Kind == jobcontract.KindPerformance && spec.Spec.Performance.ReportID == reportID
	if !validOwner {
		writeOperationError(writer, jobhost.ErrNotFound)
		return
	}
	archive, err := handler.repository.ReadPerformanceArchive(ctx, spec.JobID, reportID)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	report, reference := archive.Report, archive.Reference
	prepared := quicktest.PreparedPerformance{
		ReportID: report.ReportID, Protocol: report.Protocol, AddressMode: report.AddressMode,
		BaseURL: report.BaseURL, Endpoint: report.Endpoint, ModelID: report.ModelID, Profile: report.Profile,
	}
	canonical, err := jobcontract.EncodePerformanceArchive(report)
	digest := sha256.Sum256(archive.Document)
	validReference := reference.Kind == jobcontract.KindPerformance && reference.ID == reportID && reference.RunID == ""
	validBytes := err == nil && bytes.Equal(canonical, archive.Document) &&
		reference.Size == int64(len(archive.Document)) && reference.SHA256 == hex.EncodeToString(digest[:])
	if !validReference || !validBytes || !reflect.DeepEqual(prepared, *spec.Spec.Performance) {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	modifiedAt, err := time.Parse(time.RFC3339Nano, report.GeneratedAt)
	if err != nil {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	serveReport(writer, request, reportDownload{
		Document: archive.Document, SHA256: reference.SHA256, Kind: jobcontract.KindPerformance, ModifiedAt: modifiedAt,
	})
}
