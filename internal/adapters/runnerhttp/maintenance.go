package runnerhttp

import (
	"net/http"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) maintain(writer http.ResponseWriter, request *http.Request) {
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
	command, err := jobcontract.DecodeMaintenanceCommand(request.Body)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	handler.serviceMu.Lock()
	if command.Action == jobcontract.MaintenancePause && handler.activeUploads != 0 {
		handler.serviceMu.Unlock()
		writeJSON(writer, http.StatusConflict, ErrorResponse{
			Contract: jobcontract.CurrentContract, Code: "upload_in_progress",
			Message: "An active content upload must finish before maintenance.", Retryable: true,
		})
		return
	}
	result, err := handler.maintenance.Apply(request.Context(), command)
	handler.serviceMu.Unlock()
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
