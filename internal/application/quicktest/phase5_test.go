package quicktest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

func TestRunPerformanceSchemaV3CarriesFineStreamingTelemetry(t *testing.T) {
	report, err := New(Dependencies{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GotFirstResponseByte != nil {
			trace.GotFirstResponseByte()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode:     AddressModeBaseURL,
		URL:             "https://example.com/v1",
		APIKey:          "secret",
		ModelID:         "model",
		RequestCount:    1,
		Concurrency:     1,
		TimeoutMS:       2_000,
		InputTokens:     10,
		OutputTokens:    2,
		SliceDurationMS: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 3 || !report.Success || len(report.Samples) != 1 {
		t.Fatalf("report header = %#v", report)
	}
	sample := report.Samples[0]
	if sample.TTFBMS <= 0 || sample.TTFTAnyMS <= 0 || sample.TTFTVisibleMS <= 0 || sample.TTSTMS <= 0 ||
		sample.TTFTMS != sample.TTFTAnyMS || sample.SemanticChunkCount != 2 || sample.ObservedICLMS < 0 {
		t.Fatalf("fine streaming sample = %#v", sample)
	}
	if report.Metrics.TTFBSamples != 1 || report.Metrics.TTFTAnySamples != 1 || report.Metrics.TTFTVisibleSamples != 1 ||
		report.Metrics.TTSTSamples != 1 || report.Metrics.ObservedICLSamples != 1 || report.Metrics.SemanticChunkCountSamples != 1 ||
		report.Metrics.TTFTSamples != report.Metrics.TTFTAnySamples || report.Metrics.TTFTP50 != report.Metrics.TTFTAnyP50 {
		t.Fatalf("fine streaming metrics = %#v", report.Metrics)
	}
	if len(report.TimeSlices) != 1 {
		t.Fatalf("time slices = %#v", report.TimeSlices)
	}
	slice := report.TimeSlices[0]
	if slice.TTFB.Count != 1 || slice.TTFTAny.Count != 1 || slice.TTFTVisible.Count != 1 ||
		slice.TTST.Count != 1 || slice.ObservedICL.Count != 1 || slice.SemanticChunkCount.Count != 1 ||
		slice.SemanticChunkCount.Average != 2 || slice.TTFT != slice.TTFTAny {
		t.Fatalf("fine streaming time slice = %#v", slice)
	}
}

func TestRunPerformanceSchemaV3KeepsToolOnlySuccessOutOfTextLatencyCohorts(t *testing.T) {
	report, err := New(Dependencies{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GotFirstResponseByte != nil {
			trace.GotFirstResponseByte()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})}).RunPerformance(context.Background(), performancePhaseFiveCommand())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(report.Samples) != 1 {
		t.Fatalf("report = %#v", report)
	}
	sample := report.Samples[0]
	if sample.SemanticChunkCount != 0 || sample.TTFTAnyMS != 0 || sample.TTFTVisibleMS != 0 || sample.TTSTMS != 0 || sample.ObservedICLMS != 0 {
		t.Fatalf("tool-only sample = %#v", sample)
	}
	if report.Metrics.SemanticChunkCountSamples != 1 || report.Metrics.SemanticChunkCountAverage != 0 ||
		report.Metrics.TTFTAnySamples != 0 || report.Metrics.TTFTVisibleSamples != 0 || report.Metrics.TTSTSamples != 0 || report.Metrics.ObservedICLSamples != 0 {
		t.Fatalf("tool-only metrics = %#v", report.Metrics)
	}
}

func TestValidateArchivedPerformanceReportRejectsFineStreamingTampering(t *testing.T) {
	report := archivedPhaseFiveReport(t)
	if _, err := ValidateArchivedPerformanceReport(report); err != nil {
		t.Fatalf("valid report error = %v", err)
	}

	for _, test := range []struct {
		name      string
		wantError string
		tamper    func(*PerformanceReport)
	}{
		{
			name: "ttfb beyond e2e",
			tamper: func(report *PerformanceReport) {
				report.Samples[0].TTFBMS = report.Samples[0].E2EMS + 1
			},
		},
		{
			name: "legacy alias",
			tamper: func(report *PerformanceReport) {
				report.Samples[0].TTFTMS++
			},
		},
		{
			name: "two chunk observed interval",
			tamper: func(report *PerformanceReport) {
				report.Samples[0].ObservedICLMS++
			},
		},
		{
			name:      "one chunk with a later visible milestone",
			wantError: "quick performance report fine streaming sample is invalid",
			tamper: func(report *PerformanceReport) {
				rewritePhaseFiveReportAsSingleChunk(report, report.Samples[0].E2EMS)
			},
		},
		{
			name: "top aggregate",
			tamper: func(report *PerformanceReport) {
				report.Metrics.TTFBP50++
			},
		},
		{
			name: "time slice average",
			tamper: func(report *PerformanceReport) {
				report.TimeSlices[0].ObservedICL.AverageMS++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := report
			candidate.Samples = append([]PerformanceSample(nil), report.Samples...)
			candidate.TimeSlices = append([]PerformanceTimeSlice(nil), report.TimeSlices...)
			test.tamper(&candidate)
			if _, err := ValidateArchivedPerformanceReport(candidate); err == nil {
				t.Fatalf("ValidateArchivedPerformanceReport() error = nil; sample = %#v", candidate.Samples[0])
			} else if test.wantError != "" && err.Error() != test.wantError {
				t.Fatalf("ValidateArchivedPerformanceReport() error = %q, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateArchivedPerformanceReportAcceptsSingleChunkVisibilityShapes(t *testing.T) {
	report := archivedPhaseFiveReport(t)

	for _, test := range []struct {
		name      string
		visibleMS func(PerformanceSample) float64
	}{
		{name: "reasoning only", visibleMS: func(PerformanceSample) float64 { return 0 }},
		{name: "visible first semantic event", visibleMS: func(sample PerformanceSample) float64 { return sample.TTFTAnyMS }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := report
			candidate.Samples = append([]PerformanceSample(nil), report.Samples...)
			candidate.TimeSlices = append([]PerformanceTimeSlice(nil), report.TimeSlices...)
			rewritePhaseFiveReportAsSingleChunk(&candidate, test.visibleMS(candidate.Samples[0]))
			if _, err := ValidateArchivedPerformanceReport(candidate); err != nil {
				t.Fatalf("ValidateArchivedPerformanceReport() error = %v; sample = %#v", err, candidate.Samples[0])
			}
		})
	}
}

func rewritePhaseFiveReportAsSingleChunk(report *PerformanceReport, visibleMS float64) {
	sample := &report.Samples[0]
	sample.SemanticChunkCount = 1
	sample.TTFTVisibleMS = visibleMS
	sample.TTSTMS = 0
	sample.ObservedICLMS = 0

	report.Metrics.TTFTVisibleSamples = 0
	report.Metrics.TTFTVisibleP50 = 0
	report.Metrics.TTFTVisibleP95 = 0
	report.Metrics.TTFTVisibleP99 = 0
	report.Metrics.TTFTVisibleAverage = 0
	if visibleMS > 0 {
		report.Metrics.TTFTVisibleSamples = 1
		report.Metrics.TTFTVisibleP50 = visibleMS
		report.Metrics.TTFTVisibleP95 = visibleMS
		report.Metrics.TTFTVisibleP99 = visibleMS
		report.Metrics.TTFTVisibleAverage = visibleMS
	}
	report.Metrics.TTSTSamples = 0
	report.Metrics.TTSTP50 = 0
	report.Metrics.TTSTP95 = 0
	report.Metrics.TTSTP99 = 0
	report.Metrics.TTSTAverage = 0
	report.Metrics.ObservedICLSamples = 0
	report.Metrics.ObservedICLP50 = 0
	report.Metrics.ObservedICLP95 = 0
	report.Metrics.ObservedICLP99 = 0
	report.Metrics.ObservedICLAverage = 0
	report.Metrics.SemanticChunkCountSamples = 1
	report.Metrics.SemanticChunkCountP50 = 1
	report.Metrics.SemanticChunkCountP95 = 1
	report.Metrics.SemanticChunkCountP99 = 1
	report.Metrics.SemanticChunkCountAverage = 1

	report.TimeSlices[0].TTFTVisible = PerformanceLatencySlice{}
	if visibleMS > 0 {
		report.TimeSlices[0].TTFTVisible = PerformanceLatencySlice{
			Count: 1, P50MS: visibleMS, P95MS: visibleMS, P99MS: visibleMS, AverageMS: visibleMS,
		}
	}
	report.TimeSlices[0].TTST = PerformanceLatencySlice{}
	report.TimeSlices[0].ObservedICL = PerformanceLatencySlice{}
	report.TimeSlices[0].SemanticChunkCount = PerformanceCountSlice{
		Count: 1, P50: 1, P95: 1, P99: 1, Average: 1,
	}
}

func archivedPhaseFiveReport(t *testing.T) PerformanceReport {
	t.Helper()
	report, err := New(Dependencies{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GotFirstResponseByte != nil {
			trace.GotFirstResponseByte()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(&delayedStreamReader{chunks: []string{
				"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n",
				"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n",
				"data: [DONE]\n\n",
			}}),
		}, nil
	})}).RunPerformance(context.Background(), performancePhaseFiveCommand())
	if err != nil {
		t.Fatal(err)
	}
	report.ReportID = "77777777-7777-4777-8777-777777777757"
	report.GeneratedAt = "2026-09-05T07:08:09Z"
	report.Archived = true
	report.ArchiveStatus = PerformanceArchiveArchived
	return report
}

type delayedStreamReader struct {
	chunks []string
	index  int
	offset int
}

func (reader *delayedStreamReader) Read(buffer []byte) (int, error) {
	if reader.index >= len(reader.chunks) {
		return 0, io.EOF
	}
	if reader.index > 0 && reader.offset == 0 {
		time.Sleep(time.Millisecond)
	}
	chunk := reader.chunks[reader.index]
	read := copy(buffer, chunk[reader.offset:])
	reader.offset += read
	if reader.offset == len(chunk) {
		reader.index++
		reader.offset = 0
	}
	return read, nil
}

func performancePhaseFiveCommand() PerformanceCommand {
	return PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 2, SliceDurationMS: 1_000,
	}
}
