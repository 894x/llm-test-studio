package runnerhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestAssetRequestSizeBoundaryDoesNotAllocateBody(t *testing.T) {
	for _, size := range []int64{-1, 0, 1, 42, jobcontract.MaxAssetBytes, jobcontract.MaxAssetBytes + 1} {
		request := httptest.NewRequest(http.MethodPut, "/assets/"+strings.Repeat("a", 64), http.NoBody)
		request.Header.Set("Content-Type", "application/octet-stream")
		request.ContentLength = size
		identity, err := assetRequestIdentity(request)
		valid := size >= 1 && size <= jobcontract.MaxAssetBytes
		if (err == nil) != valid {
			t.Fatalf("size %d admission mismatch: %v", size, err)
		}
		if valid && identity.Size != size {
			t.Fatal("admission changed declared size")
		}
		if size > jobcontract.MaxAssetBytes && !errors.Is(err, jobcontract.ErrTooLarge) {
			t.Fatal("oversize did not retain its error class")
		}
	}
}
