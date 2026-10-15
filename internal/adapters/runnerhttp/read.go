package runnerhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
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

func supportsJob(capabilities jobcontract.RunnerCapabilities, spec jobcontract.JobSpec) bool {
	if !slices.Contains(capabilities.JobKinds, spec.Kind) {
		return false
	}
	protocols := []string{}
	features := []string{}
	if spec.Kind == jobcontract.KindPerformance {
		performance := spec.Spec.Performance
		protocols = append(protocols, string(performance.Protocol))
		profile := performance.Profile
		if profile.WarmupRequests > 0 {
			features = append(features, "warmup")
		}
		if profile.RampDurationMS > 0 {
			features = append(features, "ramp")
		}
		if profile.CapacityEnabled {
			features = append(features, "capacity")
		}
		if profile.LoadMode == domain.LoadOpenLoop {
			features = append(features, "open_loop")
		}
		if profile.SliceDurationMS > 0 {
			features = append(features, "time_slices")
		}
		if profile.WorkloadMode == quicktest.PerformanceWorkloadNormal {
			features = append(features, "normal_workload")
		}
		if profile.RandomInput {
			features = append(features, "random_input")
		}
	} else {
		for _, run := range spec.PreparedRuns() {
			protocols = append(protocols, string(run.Snapshot.Channel.Protocol))
			for _, entry := range run.Snapshot.Entries {
				if entry.WarmupCount > 0 {
					features = append(features, "warmup")
				}
				if entry.Load.Mode == domain.LoadOpenLoop {
					features = append(features, "open_loop")
				}
			}
		}
	}
	if len(spec.Assets) > 0 {
		features = append(features, "assets")
	}
	return containsAll(capabilities.Protocols, protocols) && containsAll(capabilities.Features, features)
}

func containsAll(supported, required []string) bool {
	for _, item := range required {
		if !slices.Contains(supported, item) {
			return false
		}
	}
	return true
}

func (handler *Handler) performanceResult(writer http.ResponseWriter, request *http.Request) {
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
	if spec.Kind != jobcontract.KindPerformance || spec.Spec.Performance.ReportID != request.PathValue("report_id") {
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
	if !validRange(request, reference.Size) {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(reference.Size, 10))
		writeError(
			writer,
			http.StatusRequestedRangeNotSatisfiable,
			"invalid_range",
			"Use one satisfiable byte range.",
		)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", `"`+reference.SHA256+`"`)
	writer.Header().Set("X-Content-SHA256", reference.SHA256)
	writer.Header().Set("X-LLM-Test-Report-Kind", string(jobcontract.KindPerformance))
	http.ServeContent(
		writer,
		request,
		"performance.json",
		result.SealedAt,
		bytes.NewReader(sealed.Document),
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
