// Package runnerhttp exposes the current private runner management protocol.
package runnerhttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var ErrInvalid = errors.New("invalid_runner_http_configuration")

const storageTimeout = 5 * time.Second

type ReadRepository interface {
	GetJob(context.Context, string) (jobcontract.Snapshot, error)
	GetJobSpec(context.Context, string) (jobcontract.JobSpec, error)
	JobEvents(context.Context, string, uint64, uint32) (jobhost.EventPage, error)
	ReadPerformanceResult(context.Context, string) (jobhost.PerformanceResultDocument, error)
	ListJobs(context.Context, jobhost.JobListQuery) (jobhost.JobPage, error)
	ReadRunReport(context.Context, string, string) (jobhost.RunReportDocument, error)
	ReadPerformanceArchive(context.Context, string, string) (jobhost.PerformanceArchiveDocument, error)
}

type Dependencies struct {
	Host         *jobhost.Host
	Maintenance  *jobhost.Maintenance
	Repository   ReadRepository
	TokenSHA256  [32]byte
	Capabilities jobcontract.RunnerCapabilities
}

type Handler struct {
	host        *jobhost.Host
	maintenance *jobhost.Maintenance
	repository  ReadRepository
	tokenHash   [32]byte
	info        jobcontract.RunnerInfo
	mux         *http.ServeMux
}

type ErrorResponse struct {
	Contract  int    `json:"contract"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field,omitempty"`
	Retryable bool   `json:"retryable"`
}

func New(dependencies Dependencies) (*Handler, error) {
	missingRepository := dependencies.Repository == nil
	if !missingRepository {
		value := reflect.ValueOf(dependencies.Repository)
		missingRepository = value.Kind() == reflect.Pointer && value.IsNil()
	}
	invalid := dependencies.Host == nil || missingRepository || dependencies.TokenSHA256 == [32]byte{}
	if invalid || !dependencies.Maintenance.Owns(dependencies.Host) {
		return nil, ErrInvalid
	}
	identity := dependencies.Host.Identity()
	if identity.Target.Kind != jobcontract.TargetRemote {
		return nil, ErrInvalid
	}
	policy := identity.Policy
	info := jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: identity.Origin.NodeID,
		NodeInstallationID: identity.Origin.InstallationID, EngineBuildID: identity.Origin.EngineBuildID,
		RunnerVersion: identity.Origin.RunnerVersion, OS: identity.Origin.OS, Arch: identity.Origin.Arch,
		Capabilities: dependencies.Capabilities,
		Budget: jobcontract.RunnerBudget{
			MaxActiveJobs: policy.MaxActiveJobs, MaxQueuedJobs: policy.MaxQueuedJobs,
			MaxConcurrency: policy.MaxConcurrency, QueueTimeoutNS: int64(policy.QueueTimeout),
			ExecutionTimeoutNS: int64(policy.ExecutionTimeout),
		},
	}
	if err := info.Validate(); err != nil {
		return nil, ErrInvalid
	}
	handler := &Handler{
		host: dependencies.Host, repository: dependencies.Repository, tokenHash: dependencies.TokenSHA256,
		maintenance: dependencies.Maintenance,
		info:        info.Clone(), mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /runner", handler.runner)
	handler.mux.HandleFunc("POST /runner/maintenance", handler.maintain)
	handler.mux.HandleFunc("POST /jobs", handler.submit)
	handler.mux.HandleFunc("GET /jobs", handler.jobs)
	handler.mux.HandleFunc("GET /jobs/{job_id}", handler.job)
	handler.mux.HandleFunc("GET /jobs/{job_id}/events", handler.events)
	handler.mux.HandleFunc("POST /jobs/{job_id}/stop-sending", handler.command(jobcontract.CommandStopSending))
	handler.mux.HandleFunc("POST /jobs/{job_id}/cancel", handler.command(jobcontract.CommandCancel))
	handler.mux.HandleFunc("GET /jobs/{job_id}/reports/{report_id}", handler.report)
	handler.mux.HandleFunc("GET /jobs/{job_id}/archives/{report_id}", handler.performanceArchive)
	handler.mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		writeError(
			writer,
			http.StatusNotFound,
			"not_found",
			"The management route does not exist.",
		)
	})
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer = &jsonResponseWriter{ResponseWriter: writer, gzip: acceptsJSONGzip(request)}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if !handler.authorized(request) {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeError(
			writer,
			http.StatusUnauthorized,
			"unauthorized",
			"A valid node token is required.",
		)
		return
	}
	contracts := request.Header.Values(jobcontract.ContractHeader)
	if len(contracts) != 1 || contracts[0] != strconv.Itoa(jobcontract.CurrentContract) {
		writeError(
			writer,
			http.StatusUpgradeRequired,
			"unsupported_contract",
			"Use the current runner contract.",
		)
		return
	}
	if len(request.URL.RawQuery) > 4096 {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"The query is too large.",
		)
		return
	}
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) authorized(request *http.Request) bool {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 256 {
		return false
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	if token == "" || strings.TrimSpace(token) != token {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], handler.tokenHash[:]) == 1
}

func (handler *Handler) runner(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(request) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"This operation takes no query parameters.",
		)
		return
	}
	info, status := handler.info.Clone(), handler.host.Status()
	info.Ready, info.Active, info.Queued = status.Ready, status.Active, status.Queued
	info.Healthy, info.Pending = status.Healthy, status.Pending
	info.MaintenanceOperationID = status.MaintenanceOperationID
	writeJSON(writer, http.StatusOK, info)
}

func (handler *Handler) submit(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(request) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"This operation takes no query parameters.",
		)
		return
	}
	if !jsonContentType(request) {
		writeError(
			writer,
			http.StatusUnsupportedMediaType,
			"invalid_content_type",
			"Use application/json.",
		)
		return
	}
	submission, err := jobcontract.DecodeSubmission(request.Body)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	defer submission.Clear()
	if !handler.info.Capabilities.SupportsJob(submission.Job) {
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"capability_missing",
			"Upgrade the runner to provide this task's required capabilities.",
		)
		return
	}
	snapshot, created, err := handler.host.Submit(request.Context(), &submission)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, snapshot.Receipt)
}

func (handler *Handler) ownJob(ctx context.Context, id string) (jobcontract.Snapshot, jobcontract.JobSpec, error) {
	if !domain.IsUUID(id) {
		return jobcontract.Snapshot{}, jobcontract.JobSpec{}, jobhost.ErrInvalid
	}
	snapshot, err := handler.repository.GetJob(ctx, id)
	if err != nil {
		return jobcontract.Snapshot{}, jobcontract.JobSpec{}, err
	}
	spec, err := handler.repository.GetJobSpec(ctx, id)
	if err != nil {
		return jobcontract.Snapshot{}, jobcontract.JobSpec{}, err
	}
	owner := spec.NodeInstallationID == handler.info.NodeInstallationID &&
		spec.Target.Kind == jobcontract.TargetRemote && spec.Target.NodeID == handler.info.NodeID
	if !owner {
		return jobcontract.Snapshot{}, jobcontract.JobSpec{}, jobhost.ErrNotFound
	}
	if snapshot.Validate(spec) != nil {
		return jobcontract.Snapshot{}, jobcontract.JobSpec{}, jobhost.ErrUnavailable
	}
	return snapshot, spec, nil
}

func (handler *Handler) job(writer http.ResponseWriter, request *http.Request) {
	if !validQuery(request) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_query",
			"This operation takes no query parameters.",
		)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
	defer cancel()
	snapshot, _, err := handler.ownJob(ctx, request.PathValue("job_id"))
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, snapshot)
}

func (handler *Handler) command(kind jobcontract.CommandKind) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !validQuery(request) {
			writeError(
				writer,
				http.StatusBadRequest,
				"invalid_query",
				"This operation takes no query parameters.",
			)
			return
		}
		if !jsonContentType(request) {
			writeError(
				writer,
				http.StatusUnsupportedMediaType,
				"invalid_content_type",
				"Use application/json.",
			)
			return
		}
		command, err := jobcontract.DecodeCommand(request.Body)
		if err != nil {
			writeOperationError(writer, err)
			return
		}
		if command.JobID != request.PathValue("job_id") || command.Kind != kind {
			writeError(
				writer,
				http.StatusBadRequest,
				"invalid_command",
				"Command identity and kind must match the path.",
			)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), storageTimeout)
		defer cancel()
		if _, _, err := handler.ownJob(ctx, command.JobID); err != nil {
			writeOperationError(writer, err)
			return
		}
		confirmation, _, err := handler.host.ApplyCommand(ctx, command)
		if err != nil {
			writeOperationError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, confirmation)
	}
}

func validQuery(request *http.Request, names ...string) bool {
	// ParseQuery reports malformed escapes and semicolon separators instead of
	// silently treating a malformed query as an empty set.
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return false
	}
	for name, entries := range values {
		if !slices.Contains(names, name) || len(entries) != 1 {
			return false
		}
	}
	return true
}

func jsonContentType(request *http.Request) bool {
	if len(request.Header.Values("Content-Type")) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return false
	}
	for name, value := range parameters {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	document, err := json.Marshal(value)
	if err != nil {
		writeError(
			writer,
			http.StatusServiceUnavailable,
			"storage_unavailable",
			"The runner cannot return valid data.",
		)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Add("Vary", "Accept-Encoding")
	if negotiated, ok := writer.(*jsonResponseWriter); ok && negotiated.gzip {
		document = gzipJSON(document)
		writer.Header().Set("Content-Encoding", "gzip")
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(document)))
	writer.WriteHeader(status)
	_, _ = writer.Write(document)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, ErrorResponse{
		Contract: jobcontract.CurrentContract, Code: code, Message: message,
		Retryable: status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests,
	})
}

func writeOperationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jobcontract.ErrTooLarge):
		writeError(
			writer,
			http.StatusRequestEntityTooLarge,
			"body_too_large",
			"The request exceeds its byte limit.",
		)
	case errors.Is(err, jobcontract.ErrUnsupported):
		writeError(
			writer,
			http.StatusUpgradeRequired,
			"unsupported_contract",
			"Use the current runner contract.",
		)
	case errors.Is(err, jobcontract.ErrInvalid), errors.Is(err, jobhost.ErrInvalid):
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			"The request does not match the current contract.",
		)
	case errors.Is(err, jobhost.ErrIdentity), errors.Is(err, jobhost.ErrConflict):
		writeError(
			writer,
			http.StatusConflict,
			"job_conflict",
			"The task conflicts with existing state or runner identity.",
		)
	case errors.Is(err, jobhost.ErrNotFound):
		writeError(
			writer,
			http.StatusNotFound,
			"not_found",
			"The requested task data is not available.",
		)
	case errors.Is(err, jobhost.ErrCapacity):
		writeError(
			writer,
			http.StatusTooManyRequests,
			"capacity_exceeded",
			"The node queue or resource budget is full.",
		)
	case errors.Is(err, jobhost.ErrMaintenance):
		writeError(
			writer,
			http.StatusLocked,
			"runner_maintenance",
			"The node is paused for maintenance. Existing tasks continue.",
		)
	default:
		writeError(
			writer,
			http.StatusServiceUnavailable,
			"storage_unavailable",
			"The runner is temporarily unavailable.",
		)
	}
}
