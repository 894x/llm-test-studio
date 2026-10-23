package runnerhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) events(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(
		request,
		"after_sequence",
		"limit",
		"wait_ms",
	) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"Use one value for each documented event parameter.",
		)
		return
	}
	query := request.URL.Query()
	after, validAfter := queryNumber(query.Get("after_sequence"), 63)
	limit, validLimit := queryNumber(query.Get("limit"), 32)
	var wait uint64
	validWait := true
	if values, present := query["wait_ms"]; present {
		wait, validWait = queryNumber(values[0], 32)
	}
	valid := validAfter && validLimit && validWait && limit >= 1 && limit <= 200 && wait <= 20000
	if !valid {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"Invalid event cursor, limit or wait duration.",
		)
		return
	}
	id := request.PathValue("job_id")
	deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
		_, _, err := handler.ownJob(ctx, id)
		if err != nil {
			cancel()
			writeOperationError(writer, err)
			return
		}
		page, err := handler.repository.JobEvents(
			ctx,
			id,
			after,
			uint32(limit),
		)
		cancel()
		if err != nil {
			writeOperationError(writer, err)
			return
		}
		if len(page.Events) > 0 || wait == 0 || !time.Now().Before(deadline) {
			writeJSON(writer, http.StatusOK, page)
			return
		}
		timer := time.NewTimer(min(100*time.Millisecond, time.Until(deadline)))
		select {
		case <-request.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func queryNumber(value string, bits int) (uint64, bool) {
	number, err := strconv.ParseUint(value, 10, bits)
	return number, err == nil && strconv.FormatUint(number, 10) == value
}

func (handler *Handler) report(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(request) || !domain.IsUUID(request.PathValue("report_id")) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			"Use a valid report identity without query parameters.",
		)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
	defer cancel()
	snapshot, spec, err := handler.ownJob(ctx, request.PathValue("job_id"))
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	if spec.Kind != jobcontract.KindPerformance {
		handler.runReport(writer, request, spec)
		return
	}
	if spec.Spec.Performance.ReportID != request.PathValue("report_id") {
		writeOperationError(writer, jobhost.ErrNotFound)
		return
	}
	sealed, err := handler.repository.ReadPerformanceResult(ctx, spec.JobID)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	result, reference := sealed.Result, sealed.Reference
	matching := result.JobID == spec.JobID && result.ReportID == spec.Spec.Performance.ReportID &&
		reflect.DeepEqual(result.Terminal.Receipt, snapshot.Receipt) &&
		reflect.DeepEqual(result.Terminal.Failure, snapshot.Failure)
	if !matching || result.Validate() != nil || reference.Validate() != nil {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	if result.Observation != nil && result.Observation.ValidateFor(spec) != nil {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	canonical, err := jobcontract.EncodePerformanceResult(result)
	digest := sha256.Sum256(sealed.Document)
	validBytes := err == nil && bytes.Equal(canonical, sealed.Document) && reference.JobID == result.JobID &&
		reference.ReportID == result.ReportID && reference.Size == int64(len(sealed.Document)) &&
		reference.SHA256 == hex.EncodeToString(digest[:])
	if !validBytes {
		writeOperationError(writer, jobhost.ErrUnavailable)
		return
	}
	serveReport(writer, request, reportDownload{
		Document: sealed.Document, SHA256: reference.SHA256, Kind: jobcontract.KindPerformance, ModifiedAt: result.SealedAt,
	})
}

const reportGzipMinimumBytes = 64 << 10

type reportDownload struct {
	Document   []byte
	SHA256     string
	Kind       jobcontract.Kind
	ModifiedAt time.Time
}

func serveReport(writer http.ResponseWriter, request *http.Request, report reportDownload) {
	if negotiated, ok := writer.(*jsonResponseWriter); ok && negotiated.gzip && len(report.Document) >= reportGzipMinimumBytes {
		report.Document = gzipJSON(report.Document)
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Add("Vary", "Accept-Encoding")
	}
	size := int64(len(report.Document))
	if !validRange(request, size) {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		writeError(
			writer,
			http.StatusRequestedRangeNotSatisfiable,
			"invalid_range",
			"Use one satisfiable byte range.",
		)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", `"`+report.SHA256+`"`)
	writer.Header().Set("X-Content-SHA256", report.SHA256)
	writer.Header().Set("X-LLM-Test-Report-Kind", string(report.Kind))
	http.ServeContent(
		writer,
		request,
		string(report.Kind)+".json",
		report.ModifiedAt,
		bytes.NewReader(report.Document),
	)
}

func validRange(request *http.Request, size int64) bool {
	values, present := request.Header["Range"]
	if !present {
		return true
	}
	if len(values) != 1 || len(values[0]) > 64 || !strings.HasPrefix(values[0], "bytes=") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(values[0], "bytes="), "-")
	if len(parts) != 2 {
		return false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseUint(parts[1], 10, 63)
		return err == nil && suffix > 0
	}
	start, err := strconv.ParseUint(parts[0], 10, 63)
	if err != nil || start >= uint64(size) {
		return false
	}
	if parts[1] == "" {
		return true
	}
	end, err := strconv.ParseUint(parts[1], 10, 63)
	return err == nil && end >= start
}
