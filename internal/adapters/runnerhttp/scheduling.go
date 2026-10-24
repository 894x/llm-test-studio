package runnerhttp

import (
	"net/http"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) configureTaskConcurrency(writer http.ResponseWriter, request *http.Request) {
	if handler.scheduling == nil {
		writeOperationError(writer, jobcontract.ErrUnsupported)
		return
	}
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
	command, err := jobcontract.DecodeTaskConcurrencyCommand(request.Body)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	if err := handler.scheduling.Apply(request.Context(), command); err != nil {
		writeOperationError(writer, err)
		return
	}
	handler.runner(writer, request)
}
