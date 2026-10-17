package runnerinstall

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

func TestUpgradeMaintenanceClassifiesUploadBusyWithoutConfirmingPause(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{
			name: "upload_busy", want: ErrBusy,
			body: `{"contract":1,"code":"upload_in_progress","message":"fixed","retryable":true}`,
		},
		{
			name: "identity_conflict", want: ErrConflict,
			body: `{"contract":1,"code":"job_conflict","message":"fixed","retryable":false}`,
		},
		{
			name: "busy_without_retry", want: ErrIntegrity,
			body: `{"contract":1,"code":"upload_in_progress","message":"fixed","retryable":false}`,
		},
		{
			name: "missing_retry", want: ErrIntegrity,
			body: `{"contract":1,"code":"job_conflict","message":"fixed"}`,
		},
		{
			name: "duplicate_code", want: ErrIntegrity,
			body: `{"contract":1,"code":"job_conflict","code":"upload_in_progress","message":"fixed","retryable":true}`,
		},
		{
			name: "unknown", want: ErrIntegrity,
			body: `{"contract":1,"code":"unknown","message":"fixed","retryable":true}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/runner/maintenance" {
					t.Error("unexpected maintenance path")
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			_, port, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			number, err := strconv.ParseUint(port, 10, 16)
			if err != nil {
				t.Fatal(err)
			}
			config := runnerconfig.Config{ListenPort: uint16(number)}
			result, err := applyUpgradeMaintenance(t.Context(), config, []byte("test-token"), jobcontract.MaintenanceCommand{})
			if !errors.Is(err, test.want) || result.Record.Paused || result.Status.Pending != 0 {
				t.Fatalf("maintenance refusal was treated as a confirmed pause: result=%+v err=%v", result, err)
			}
		})
	}
}
