package runnerhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (handler *Handler) uploadAsset(writer http.ResponseWriter, request *http.Request) {
	// Bulk responses have one bounded identity encoding, including their errors.
	if negotiated, ok := writer.(*jsonResponseWriter); ok {
		negotiated.gzip = false
	}
	identity, err := assetRequestIdentity(request)
	if err != nil {
		closeUnreadAssetBody(writer, request)
		switch {
		case errors.Is(err, errAssetContentType):
			writeError(
				writer,
				http.StatusUnsupportedMediaType,
				"invalid_content_type",
				"Use application/octet-stream.",
			)
		case errors.Is(err, errAssetContentEncoding):
			writeError(
				writer,
				http.StatusUnsupportedMediaType,
				"invalid_content_encoding",
				"Use identity content encoding.",
			)
		default:
			writeOperationError(writer, err)
		}
		return
	}
	if handler.content == nil {
		closeUnreadAssetBody(writer, request)
		writeError(
			writer,
			http.StatusNotFound,
			"not_found",
			"Content upload is not available.",
		)
		return
	}
	if err := handler.beginUpload(); err != nil {
		closeUnreadAssetBody(writer, request)
		writeOperationError(writer, err)
		return
	}
	defer handler.endUpload()
	ctx, cancel := context.WithTimeout(request.Context(), jobcontract.AssetUploadTimeout)
	defer cancel()
	controller := http.NewResponseController(writer)
	deadline, _ := ctx.Deadline()
	if err := controller.SetReadDeadline(deadline); err != nil {
		closeUnreadAssetBody(writer, request)
		writeOperationError(writer, jobcontract.ErrAssetStorage)
		return
	}
	if err := controller.SetWriteDeadline(deadline); err != nil {
		closeUnreadAssetBody(writer, request)
		writeOperationError(writer, jobcontract.ErrAssetStorage)
		return
	}
	closeBody := sync.OnceValue(request.Body.Close)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		// Closing a net/http body can wait for its blocked Read. Expire the
		// socket read first so cancellation can always join this callback.
		_ = controller.SetReadDeadline(time.Now())
		_ = closeBody()
		close(closed)
	})
	finishBody := sync.OnceValue(func() error {
		if !stop() {
			<-closed
		}
		return closeBody()
	})
	defer func() { _ = finishBody() }()
	created, err := handler.content.PutContent(ctx, identity, request.Body)
	// Capture the operation's cancellation before our own cleanup cancellation.
	// An early quota/storage refusal may leave its body unread; expire the read
	// before Close can try to drain even a small unfinished HTTP body.
	err = errors.Join(err, ctx.Err())
	if err != nil {
		closeUnreadAssetBody(writer, request)
		cancel()
	}
	if closeErr := finishBody(); closeErr != nil {
		if err == nil {
			err = errors.Join(jobcontract.ErrAssetRead, closeErr)
		} else {
			err = errors.Join(err, closeErr)
		}
	}
	// A stored file is safe to retry after an ambiguous acknowledgement. Never
	// report a success after the operation's cancellation or deadline.
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		writeAssetError(writer, err)
		return
	}
	receipt := jobcontract.AssetReceipt{
		Contract: jobcontract.CurrentContract, NodeID: handler.info.NodeID,
		NodeInstallationID: handler.info.NodeInstallationID, SHA256: identity.SHA256, Size: identity.Size,
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(writer, code, receipt)
}

var (
	errAssetContentType     = errors.New("invalid_asset_content_type")
	errAssetContentEncoding = errors.New("invalid_asset_content_encoding")
)

func assetRequestIdentity(request *http.Request) (jobcontract.ContentIdentity, error) {
	digest := strings.TrimPrefix(request.URL.Path, "/assets/")
	canonicalPath := request.URL.Path == "/assets/"+digest && request.URL.RawPath == "" &&
		request.URL.EscapedPath() == request.URL.Path
	queryAbsent := request.URL.RawQuery == "" && !request.URL.ForceQuery
	validPath := canonicalPath && queryAbsent && jobcontract.IsSHA256(digest)
	if !validPath {
		return jobcontract.ContentIdentity{}, jobcontract.ErrInvalid
	}
	types := request.Header.Values("Content-Type")
	if len(types) != 1 || types[0] != "application/octet-stream" {
		return jobcontract.ContentIdentity{}, errAssetContentType
	}
	encodings := request.Header.Values("Content-Encoding")
	validEncoding := len(encodings) == 0 || len(encodings) == 1 && encodings[0] == "identity"
	if !validEncoding {
		return jobcontract.ContentIdentity{}, errAssetContentEncoding
	}
	if request.ContentLength > jobcontract.MaxAssetBytes {
		return jobcontract.ContentIdentity{}, jobcontract.ErrTooLarge
	}
	invalidBody := request.Body == nil || request.ContentLength < 1 || len(request.TransferEncoding) != 0
	if len(request.Header.Values("Transfer-Encoding")) != 0 {
		invalidBody = true
	}
	if invalidBody {
		return jobcontract.ContentIdentity{}, jobcontract.ErrInvalid
	}
	return jobcontract.ContentIdentity{SHA256: digest, Size: request.ContentLength}, nil
}

func closeUnreadAssetBody(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		return
	}
	// Avoid waiting for net/http to consume a rejected large request before
	// returning its response. net/http remains the owner of an unread body.
	request.Close = true
	writer.Header().Set("Connection", "close")
	_ = http.NewResponseController(writer).SetReadDeadline(time.Now())
}

func (handler *Handler) beginUpload() error {
	// Maintenance takes this lock before Host.mu too. Neither lock is held
	// during content reservation, reads, persistence, or resource closing.
	handler.serviceMu.Lock()
	defer handler.serviceMu.Unlock()
	status := handler.host.Status()
	if status.MaintenanceOperationID != "" {
		return jobhost.ErrMaintenance
	}
	if !status.Healthy {
		return jobhost.ErrUnavailable
	}
	handler.activeUploads++
	return nil
}

func (handler *Handler) endUpload() {
	handler.serviceMu.Lock()
	defer handler.serviceMu.Unlock()
	handler.activeUploads--
}

func writeAssetError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jobcontract.ErrAssetStorage):
		writeOperationError(writer, jobcontract.ErrAssetStorage)
	case errors.Is(err, jobhost.ErrCapacity), errors.Is(err, jobhost.ErrMaintenance):
		writeOperationError(writer, err)
	case errors.Is(err, jobcontract.ErrAssetIntegrity):
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"asset_integrity_mismatch",
			"The bytes do not match the declared content identity.",
		)
	case errors.Is(err, jobcontract.ErrAssetRead), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		writeError(
			writer,
			http.StatusRequestTimeout,
			"asset_read_failed",
			"The upload was interrupted before it could be confirmed.",
		)
	default:
		writeOperationError(writer, err)
	}
}
