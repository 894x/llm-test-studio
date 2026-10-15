package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

type preparedConnectionResolver func(context.Context, ChannelSelection) (ChannelConnection, error)

func (resolver preparedConnectionResolver) Resolve(
	ctx context.Context,
	selection ChannelSelection,
) (ChannelConnection, error) {
	return resolver(ctx, selection)
}

func writeHostTestStream(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
	fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n")
	fmt.Fprint(writer, "data: [DONE]\n\n")
}

func hostTestCommand(endpoint string) PerformanceCommand {
	return PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: AddressModeBaseURL,
		URL: endpoint, APIKey: "prepared-node-secret", ModelID: "prepared-model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 2000, InputTokens: 10, OutputTokens: 3,
	}
}

func TestPerformanceHostUsesTransportedInputWithoutDesktopResolvers(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer prepared-node-secret" {
			t.Error("host did not receive the transferred credential")
		}
		writeHostTestStream(writer)
	}))
	defer server.Close()
	var resolutions atomic.Int64
	preparer := New(Dependencies{ChannelConnections: preparedConnectionResolver(
		func(context.Context, ChannelSelection) (ChannelConnection, error) {
			resolutions.Add(1)
			return ChannelConnection{BaseURL: server.URL, APIKey: []byte("prepared-node-secret")}, nil
		},
	)})
	command := hostTestCommand("")
	command.APIKey = ""
	command.ChannelID = "77777777-7777-4777-8777-777777777774"
	ctx, cancel := context.WithCancel(context.Background())
	prepared, lease, code := preparer.BuildPerformance(ctx, command)
	if code != "" || prepared.Validate() != nil {
		t.Fatalf("prepare error = %q", code)
	}
	if requests.Load() != 0 || resolutions.Load() != 1 {
		t.Fatal("preparation sent traffic or resolved the connection more than once")
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"prepared-node-secret", "api_key", "channel_id", "credential_run_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("prepared input contains %q", forbidden)
		}
	}
	var transported PreparedPerformance
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	cancel()
	archive := &capturingPerformanceArchive{}
	host := NewHost(HostDependencies{Transport: server.Client().Transport, Archive: archive})
	report, err := host.ExecutePrepared(context.Background(), transported, lease, ExecutionOptions{})
	if err != nil || !report.Success || requests.Load() != 2 {
		t.Fatalf("host result: success=%v requests=%d error=%v", report.Success, requests.Load(), err)
	}
	if report.ReportID != prepared.ReportID || len(archive.saved) != 1 {
		t.Fatal("host replaced the report identity assigned before submission")
	}
	if resolutions.Load() != 1 {
		t.Fatal("host used the desktop resolver")
	}
	if _, err := lease.Bytes(); err == nil {
		t.Fatal("completed host execution retained its credential")
	}
}

func TestPerformanceHostStopSendingDrainsWithoutLaunchingMoreRequests(t *testing.T) {
	var requests atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			writeHostTestStream(writer)
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	command := hostTestCommand(server.URL)
	command.RequestCount = 100
	prepared, lease, code := New(Dependencies{}).BuildPerformance(context.Background(), command)
	if code != "" {
		t.Fatalf("prepare error = %q", code)
	}
	stop := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan PerformanceReport, 1)
	go func() {
		report, _ := NewHost(HostDependencies{Transport: server.Client().Transport}).ExecutePrepared(
			ctx,
			prepared,
			lease,
			ExecutionOptions{StopSending: stop},
		)
		result <- report
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("host did not launch its first request")
	}
	close(stop)
	close(release)
	select {
	case report := <-result:
		if !report.Progress.Stopped || report.Metrics.Completed != 1 || requests.Load() != 1 {
			t.Fatalf("drain launched more traffic: progress=%+v requests=%d", report.Progress, requests.Load())
		}
		if report.ReportID != prepared.ReportID {
			t.Fatal("unarchived execution lost its prepared report identity")
		}
	case <-ctx.Done():
		t.Fatal("host failed to drain")
	}
}

func TestPerformanceHostStopSendingDoesNotAdvanceToLaterPhases(t *testing.T) {
	for _, phase := range []string{"warmup", "ramp", "capacity"} {
		t.Run(phase, func(t *testing.T) {
			command := hostTestCommand("https://example.test")
			command.RequestCount = 4
			stopAtRequest := int64(1)
			switch phase {
			case "warmup":
				command.WarmupRequests = 3
				command.RampDurationMS, command.RampRequestCap = 1000, 3
			case "ramp":
				command.WarmupRequests = 1
				command.RampDurationMS, command.RampRequestCap = 1000, 3
				stopAtRequest = 2
			case "capacity":
				command.Concurrency = 3
				command.CapacityEnabled = true
				command.CapacityStart, command.CapacityStep = 1, 1
				command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			prepared, lease, code := New(Dependencies{}).BuildPerformance(ctx, command)
			if code != "" {
				t.Fatalf("prepare error = %q", code)
			}
			var requests atomic.Int64
			entered, release := make(chan struct{}, 1), make(chan struct{})
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if requests.Add(1) == stopAtRequest {
					entered <- struct{}{}
					select {
					case <-release:
					case <-request.Context().Done():
						return nil, request.Context().Err()
					}
				}
				return successfulStreamResponse(), nil
			})
			archive := &capturingPerformanceArchive{}
			host := NewHost(HostDependencies{Transport: transport, Archive: archive})
			stop := make(chan struct{})
			done := make(chan PerformanceReport, 1)
			go func() {
				report, _ := host.ExecutePrepared(ctx, prepared, lease, ExecutionOptions{StopSending: stop})
				done <- report
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("phase did not start")
			}
			close(stop)
			close(release)
			var report PerformanceReport
			select {
			case report = <-done:
			case <-ctx.Done():
				t.Fatal("phase failed to drain")
			}
			if !report.Progress.Stopped || requests.Load() != stopAtRequest {
				t.Fatalf("host advanced after stop: progress=%+v requests=%d", report.Progress, requests.Load())
			}
			if report.ReportID != prepared.ReportID || report.Archived || len(archive.saved) != 0 {
				t.Fatal("stopped phase lost its identity or archived an incomplete measurement")
			}
			switch phase {
			case "warmup":
				if report.Warmup == nil || report.Warmup.Completed != 1 || report.Ramp != nil {
					t.Fatal("warmup summary or phase boundary was lost")
				}
			case "ramp":
				if report.Warmup == nil || report.Warmup.Completed != 1 || report.Ramp == nil {
					t.Fatal("completed warmup or stopped ramp summary was lost")
				}
				if report.Ramp.Traffic.Completed != 1 {
					t.Fatal("ramp did not drain its in-flight request")
				}
			case "capacity":
				if report.CapacityResult == nil || len(report.CapacityResult.Rungs) != 1 {
					t.Fatal("capacity started another rung or lost the stopped rung")
				}
				if report.CapacityResult.Status != PerformanceSLONotEvaluated {
					t.Fatal("stopped capacity run was evaluated")
				}
			}
			if _, err := lease.Bytes(); err == nil {
				t.Fatal("stopped phase retained its credential")
			}
		})
	}
}

func TestBuildPerformanceRejectsCancelledContextBeforeResolvingChannel(t *testing.T) {
	preparer := New(Dependencies{ChannelConnections: preparedConnectionResolver(
		func(context.Context, ChannelSelection) (ChannelConnection, error) {
			t.Fatal("cancelled preparation resolved a desktop connection")
			return ChannelConnection{}, nil
		},
	)})
	command := hostTestCommand("")
	command.APIKey = ""
	command.ChannelID = "77777777-7777-4777-8777-777777777774"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, lease, code := preparer.BuildPerformance(ctx, command)
	if lease != nil || code != "cancelled" {
		t.Fatalf("cancelled preparation: lease=%v code=%q", lease, code)
	}
}

func TestPerformanceHostRejectsTamperedPreparedInputAndClosesLease(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(
		context.Background(), hostTestCommand("http://example.test"),
	)
	if code != "" {
		t.Fatalf("prepare error = %q", code)
	}
	prepared.Endpoint = "http://other.example.test/v1/chat/completions"
	var requests atomic.Int64
	host := NewHost(HostDependencies{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("should not be called")
	})})
	report, err := host.ExecutePrepared(context.Background(), prepared, lease, ExecutionOptions{})
	if !errors.Is(err, ErrInvalidPreparedPerformance) || requests.Load() != 0 {
		t.Fatalf("tampered input: code=%s requests=%d error=%v", report.ErrorCode, requests.Load(), err)
	}
	if _, err := lease.Bytes(); err == nil {
		t.Fatal("rejected input retained its lease")
	}
}
