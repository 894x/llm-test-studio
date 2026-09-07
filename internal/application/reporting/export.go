package reporting

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

type ExportFormat string

const (
	ExportJSON         ExportFormat = "json"
	ExportHTML         ExportFormat = "html"
	ExportPNG          ExportFormat = "png"
	ExportPDF          ExportFormat = "pdf"
	DefaultWatermark                = "rhzs"
	MaxWatermarkLength              = 64
)

var ErrUnsupportedExport = errors.New("unsupported report export format")
var ErrInvalidWatermark = errors.New("invalid report watermark")
var ErrUnsupportedExportLocale = errors.New("unsupported report export locale")

// ExportedDocument is safe for JSON/Wails transport. Encoding the bytes here
// keeps JavaScript bindings stable across platforms and Wails versions.
type ExportedDocument struct {
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	DataBase64 string `json:"data_base64"`
}

func (service Service) Export(ctx context.Context, reportID string, format ExportFormat, watermark string) (ExportedDocument, error) {
	return service.export(ctx, reportID, format, watermark, "zh-CN")
}

func (service Service) ExportLocalized(ctx context.Context, reportID string, format ExportFormat, watermark, locale string) (ExportedDocument, error) {
	if locale != "zh-CN" && locale != "en-US" {
		return ExportedDocument{}, ErrUnsupportedExportLocale
	}
	return service.export(ctx, reportID, format, watermark, locale)
}

func (service Service) export(ctx context.Context, reportID string, format ExportFormat, watermark, locale string) (ExportedDocument, error) {
	watermark, err := normalizeWatermark(watermark)
	if err != nil {
		return ExportedDocument{}, err
	}
	detail, err := service.Detail(ctx, reportID)
	if err != nil {
		return ExportedDocument{}, err
	}
	var mediaType, extension string
	var contents []byte
	switch format {
	case ExportJSON:
		mediaType, extension = "application/json", "json"
		contents, err = renderJSON(detail, watermark)
	case ExportHTML:
		mediaType, extension = "text/html; charset=utf-8", "html"
		contents, err = renderHTML(detail, watermark, locale)
	case ExportPNG:
		mediaType, extension = "image/png", "png"
		contents, err = renderPNG(detail, watermark)
	case ExportPDF:
		mediaType, extension = "application/pdf", "pdf"
		contents, err = renderPDF(detail, watermark)
	default:
		return ExportedDocument{}, ErrUnsupportedExport
	}
	if err != nil {
		return ExportedDocument{}, fmt.Errorf("render %s report: %w", format, err)
	}
	return ExportedDocument{
		Filename:   "llm-test-studio-report-" + reportID + "." + extension,
		MediaType:  mediaType,
		DataBase64: base64.StdEncoding.EncodeToString(contents),
	}, nil
}

func normalizeWatermark(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultWatermark, nil
	}
	if utf8.RuneCountInString(value) > MaxWatermarkLength || strings.ContainsAny(value, "\r\n\t") {
		return "", ErrInvalidWatermark
	}
	return value, nil
}

type htmlReport struct {
	Detail       Detail
	Watermark    string
	Language     string
	Labels       htmlLabels
	Conclusion   string
	Metrics      []namedMetric
	SLA          []namedMetric
	Issues       []string
	Results      []resultRow
	GeneratedUTC string
}

type quickHTMLReport struct {
	Detail     Detail
	Watermark  string
	Language   string
	Labels     htmlLabels
	Conclusion string
	Phase      string
}

type htmlLabels struct {
	Request, Status, Input, Output, Error, Queue, Metric, Average, StreamingTiming string
	TTFTAny, TTFTVisible, TTST, ObservedICL, SemanticChunks                        string
	Title, QuickTitle, Conclusion, Plan, Run, Report, Phase                        string
	CoreMetrics, RequestDetails, RequestSamples, SuccessRate, RequestRate          string
	Passed, Failed, StatusPassed, StatusFailed, Samples, Footer, QuickFooter       string
}

type namedMetric struct {
	Name    string
	Value   string
	Unit    string
	Samples int
}

type resultRow struct {
	RequestID     string
	Status        string
	E2E           string
	TTFB          string
	TTFT          string
	TTFTVisible   string
	TTST          string
	ObservedICL   string
	SemanticChunk string
	TPOT          string
	Queue         string
	Input         string
	Output        string
	Error         string
}

func renderHTML(detail Detail, watermark, locale string) ([]byte, error) {
	var output bytes.Buffer
	labels := exportHTMLLabels(locale)
	if detail.Source == SourceQuickPerformance && detail.Performance != nil {
		conclusion := labels.Failed
		if detail.Performance.Success {
			conclusion = labels.Passed
		}
		if err := quickReportHTMLTemplate.Execute(&output, quickHTMLReport{
			Detail: detail, Watermark: watermark, Language: locale, Labels: labels,
			Conclusion: conclusion, Phase: localizedPhase(string(detail.Performance.Progress.Phase), locale),
		}); err != nil {
			return nil, err
		}
		return output.Bytes(), nil
	}
	view := reportHTMLView(detail, watermark, locale)
	if err := reportHTMLTemplate.Execute(&output, view); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func reportHTMLView(detail Detail, watermark, locale string) htmlReport {
	return htmlReport{
		Detail: detail, Watermark: watermark, Language: locale, Labels: exportHTMLLabels(locale), Conclusion: localizedVerdict(detail.Report.Conclusion.Verdict, locale),
		Metrics: sortedMetrics(detail.Report.Metrics), SLA: sortedMetrics(detail.Report.SLA),
		Issues: append([]string(nil), detail.Report.Conclusion.Issues...), Results: resultRows(detail.RequestResults),
		GeneratedUTC: detail.Report.GeneratedAt.Format("2006-01-02 15:04:05 UTC"),
	}
}

func renderJSON(detail Detail, watermark string) ([]byte, error) {
	var performance any
	if detail.Performance != nil {
		performance = detail.Performance
	}
	if detail.Performance != nil && (detail.Performance.SchemaVersion == quicktest.LegacyPerformanceSchemaVersion ||
		detail.Performance.SchemaVersion == quicktest.PerformanceSchemaVersionV2) {
		encoded, err := json.Marshal(detail.Performance)
		if err != nil {
			return nil, err
		}
		var projected any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&projected); err != nil {
			return nil, err
		}
		removeSchemaV3QuickPerformanceFields(projected)
		performance = projected
	}
	return json.MarshalIndent(struct {
		Watermark      string          `json:"watermark"`
		SchemaVersion  int             `json:"schema_version"`
		Source         ReportSource    `json:"source"`
		Report         domain.Report   `json:"report"`
		RequestResults []domain.Result `json:"request_results"`
		Performance    any             `json:"performance,omitempty"`
	}{
		Watermark: watermark, SchemaVersion: detail.SchemaVersion, Source: detail.Source,
		Report: detail.Report, RequestResults: detail.RequestResults, Performance: performance,
	}, "", "  ")
}

var schemaV3QuickPerformanceFields = map[string]struct{}{
	"average_ms": {}, "ttfb": {}, "ttft_any": {}, "ttft_visible": {}, "ttst": {}, "observed_icl": {}, "semantic_chunk_count": {},
	"ttfb_ms": {}, "ttft_any_ms": {}, "ttft_visible_ms": {}, "ttst_ms": {}, "observed_icl_ms": {}, "ttft_samples": {},
	"ttfb_samples": {}, "ttfb_p50_ms": {}, "ttfb_p95_ms": {}, "ttfb_p99_ms": {}, "ttfb_average_ms": {},
	"ttft_any_samples": {}, "ttft_any_p50_ms": {}, "ttft_any_p95_ms": {}, "ttft_any_p99_ms": {}, "ttft_any_average_ms": {},
	"ttft_visible_samples": {}, "ttft_visible_p50_ms": {}, "ttft_visible_p95_ms": {}, "ttft_visible_p99_ms": {}, "ttft_visible_average_ms": {},
	"ttst_samples": {}, "ttst_p50_ms": {}, "ttst_p95_ms": {}, "ttst_p99_ms": {}, "ttst_average_ms": {},
	"observed_icl_samples": {}, "observed_icl_p50_ms": {}, "observed_icl_p95_ms": {}, "observed_icl_p99_ms": {}, "observed_icl_average_ms": {},
	"semantic_chunk_count_samples": {}, "semantic_chunk_count_p50": {}, "semantic_chunk_count_p95": {}, "semantic_chunk_count_p99": {}, "semantic_chunk_count_average": {},
}

func removeSchemaV3QuickPerformanceFields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, v3Only := schemaV3QuickPerformanceFields[key]; v3Only {
				delete(typed, key)
				continue
			}
			removeSchemaV3QuickPerformanceFields(child)
		}
	case []any:
		for _, child := range typed {
			removeSchemaV3QuickPerformanceFields(child)
		}
	}
}

func exportHTMLLabels(locale string) htmlLabels {
	if locale == "en-US" {
		return htmlLabels{
			Request: "Request", Status: "Status", Input: "Input", Output: "Output", Error: "Error", Queue: "Queue", Metric: "Metric", Average: "Average", StreamingTiming: "Streaming timing distributions", TTFTAny: "TTFT Any (includes reasoning)", TTFTVisible: "TTFT Visible (visible content)", TTST: "TTST (second semantic chunk)", ObservedICL: "Observed ICL (semantic inter-chunk latency, not Token ITL)", SemanticChunks: "Semantic chunks",
			Title: "LLM Test Studio Test Report", QuickTitle: "LLM Test Studio Quick Performance Test Report", Conclusion: "Conclusion",
			Plan: "Plan", Run: "Run", Report: "Report", Phase: "Phase", CoreMetrics: "Core metrics", RequestDetails: "Request details",
			RequestSamples: "Request samples", SuccessRate: "Success rate", RequestRate: "Request rate", Passed: "All requests passed", Failed: "Performance test failed", StatusPassed: "passed", StatusFailed: "failed",
			Samples: "samples", Footer: "All values are derived from the sealed Go Core report document.", QuickFooter: "Generated from the archived Go Core quick performance report.",
		}
	}
	return htmlLabels{
		Request: "请求", Status: "状态", Input: "输入", Output: "输出", Error: "错误", Queue: "排队", Metric: "指标", Average: "平均", StreamingTiming: "流式时序分布", TTFTAny: "TTFT（含推理）", TTFTVisible: "TTFT（可见内容）", TTST: "TTST（第二语义块）", ObservedICL: "Observed ICL（语义块间隔，非 Token ITL）", SemanticChunks: "语义块数",
		Title: "LLM Test Studio 测试报告", QuickTitle: "LLM Test Studio 快速性能测试报告", Conclusion: "结论",
		Plan: "计划", Run: "运行", Report: "报告", Phase: "阶段", CoreMetrics: "核心指标", RequestDetails: "请求明细",
		RequestSamples: "请求样本", SuccessRate: "成功率", RequestRate: "请求速率", Passed: "全部请求成功", Failed: "性能测试未通过", StatusPassed: "通过", StatusFailed: "失败",
		Samples: "个样本", Footer: "所有数值均来自 Go Core 封存的报告文档。", QuickFooter: "由 Go Core 归档的快速性能报告生成。",
	}
}

func localizedVerdict(verdict, locale string) string {
	if locale == "en-US" {
		switch verdict {
		case "pass":
			return "Passed"
		case "fail":
			return "Failed"
		case "cancelled":
			return "Cancelled"
		}
		return verdict
	}
	switch verdict {
	case "pass":
		return "通过"
	case "fail":
		return "未通过"
	case "cancelled":
		return "已取消"
	}
	return verdict
}

func localizedPhase(phase, locale string) string {
	if locale == "en-US" {
		switch phase {
		case "not_started":
			return "Preparing"
		case "warming_up":
			return "Warming up"
		case "ramping":
			return "Ramping"
		case "sending":
			return "Sending"
		case "draining":
			return "Draining"
		case "completed":
			return "Completed"
		case "cancelled":
			return "Cancelled"
		}
		return phase
	}
	switch phase {
	case "not_started":
		return "准备中"
	case "warming_up":
		return "正在热身"
	case "ramping":
		return "正在爬坡"
	case "sending":
		return "发送中"
	case "draining":
		return "排空中"
	case "completed":
		return "已完成"
	case "cancelled":
		return "已取消"
	}
	return phase
}

func sortedMetrics(values map[string]domain.MetricValue) []namedMetric {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]namedMetric, 0, len(names))
	for _, name := range names {
		metric := values[name]
		value := "-"
		if metric.Samples > 0 {
			value = formatNumber(metric.Value)
		}
		result = append(result, namedMetric{Name: name, Value: value, Unit: metric.Unit, Samples: metric.Samples})
	}
	return result
}

func resultRows(results []domain.Result) []resultRow {
	rows := make([]resultRow, 0, len(results))
	for _, result := range results {
		status := "passed"
		errorCode := ""
		if !result.Success.Overall() {
			status = "failed"
			errorCode = string(result.ErrorCode)
		}
		rows = append(rows, resultRow{
			RequestID: result.RequestID, Status: status,
			E2E: metricText(result.Metrics, "e2e_ms"), TTFB: metricText(result.Metrics, "ttfb_ms"),
			TTFT: metricTextFirst(result.Metrics, "ttft_any_ms", "ttft_ms"), TTFTVisible: metricText(result.Metrics, "ttft_visible_ms"),
			TTST: metricText(result.Metrics, "ttst_ms"), ObservedICL: metricText(result.Metrics, "observed_icl_ms"),
			SemanticChunk: metricText(result.Metrics, "semantic_chunk_count"),
			TPOT:          metricText(result.Metrics, "tpot_ms"), Queue: metricText(result.Metrics, "schedule_lag_ms"),
			Input: metricText(result.Metrics, "prompt_tokens"), Output: metricText(result.Metrics, "completion_tokens"),
			Error: errorCode,
		})
	}
	return rows
}

func metricTextFirst(metrics map[string]float64, names ...string) string {
	for _, name := range names {
		if value, ok := metrics[name]; ok {
			return formatNumber(value)
		}
	}
	return "-"
}

func metricText(metrics map[string]float64, name string) string {
	value, ok := metrics[name]
	if !ok {
		return "-"
	}
	return formatNumber(value)
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

var reportHTMLTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>LLM Test Studio Report {{.Detail.Report.ID}}</title><style>
:root{font-family:Inter,"Segoe UI",sans-serif;color:#172033;background:#eef2f7}*{box-sizing:border-box}body{margin:0;padding:32px}.watermark{position:fixed;inset:42% auto auto 12%;z-index:10;transform:rotate(-24deg);font-size:72px;font-weight:700;letter-spacing:.12em;color:#6070891c;pointer-events:none;white-space:nowrap}main{max-width:1280px;margin:auto;background:#fff;border-radius:18px;padding:36px;box-shadow:0 14px 45px #16233a1c}h1{margin:0;font-size:30px}h2{margin-top:30px}.muted{color:#657189}.pass{color:#16794a}.fail{color:#b42318}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px}.card{border:1px solid #dde4ee;border-radius:12px;padding:13px;background:#f8fafc}.card strong,.card span{display:block}.card span{font-size:12px;color:#657189;margin-top:4px}table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:9px;border-bottom:1px solid #e6eaf0;text-align:right}th:first-child,td:first-child{text-align:left}.scroll{overflow:auto}.issues{color:#b42318}@media print{body{padding:0;background:#fff}main{box-shadow:none;border-radius:0;max-width:none}tr{break-inside:avoid}}
</style></head><body><div class="watermark">{{.Watermark}}</div><main>
<h1>{{.Labels.Title}}</h1><p class="muted">{{.Detail.Report.Model.Name}} · {{.Detail.Report.Channel.Name}} · {{.GeneratedUTC}}</p>
<h2 class="{{if .Detail.Report.Conclusion.Passed}}pass{{else}}fail{{end}}">{{.Labels.Conclusion}}: {{.Conclusion}}</h2>
<p>{{.Labels.Plan}} {{.Detail.Report.PlanSnapshot.Plan.ID}} · {{.Labels.Run}} {{.Detail.Report.RunID}} · {{.Labels.Report}} {{.Detail.Report.ID}}</p>
{{if .Issues}}<ul class="issues">{{range .Issues}}<li>{{.}}</li>{{end}}</ul>{{end}}
<h2>{{.Labels.CoreMetrics}}</h2><section class="grid">{{range .Metrics}}<div class="card"><strong>{{.Value}} {{.Unit}}</strong><span>{{.Name}} · {{.Samples}} {{$.Labels.Samples}}</span></div>{{end}}</section>
<h2>SLA</h2><section class="grid">{{range .SLA}}<div class="card"><strong>{{.Value}} {{.Unit}}</strong><span>{{.Name}} · {{.Samples}} samples</span></div>{{end}}</section>
<h2>{{.Labels.RequestDetails}} ({{len .Results}})</h2><div class="scroll"><table><thead><tr><th>{{.Labels.Request}}</th><th>{{.Labels.Status}}</th><th>E2E ms</th><th>TTFB ms</th><th>{{.Labels.TTFTAny}} ms</th><th>{{.Labels.TTFTVisible}} ms</th><th>{{.Labels.TTST}} ms</th><th>{{.Labels.ObservedICL}} ms</th><th>{{.Labels.SemanticChunks}}</th><th>TPOT ms/token</th><th>{{.Labels.Queue}} ms</th><th>{{.Labels.Input}}</th><th>{{.Labels.Output}}</th><th>{{.Labels.Error}}</th></tr></thead><tbody>{{range .Results}}<tr><td>{{.RequestID}}</td><td>{{if eq .Status "passed"}}{{$.Labels.StatusPassed}}{{else}}{{$.Labels.StatusFailed}}{{end}}</td><td>{{.E2E}}</td><td>{{.TTFB}}</td><td>{{.TTFT}}</td><td>{{.TTFTVisible}}</td><td>{{.TTST}}</td><td>{{.ObservedICL}}</td><td>{{.SemanticChunk}}</td><td>{{.TPOT}}</td><td>{{.Queue}}</td><td>{{.Input}}</td><td>{{.Output}}</td><td>{{.Error}}</td></tr>{{end}}</tbody></table></div>
<p class="muted">Schema v{{.Detail.SchemaVersion}} · {{.Labels.Footer}}</p>
</main></body></html>`))

var quickReportHTMLTemplate = template.Must(template.New("quick-report").Funcs(template.FuncMap{
	"sampledValue":      sampledMetricValue,
	"sampledSamples":    sampledMetricSamples,
	"legacyTTFTSamples": quickPerformanceLegacyTTFTSamples,
	"tpotSamples":       quickPerformanceTPOTSamples,
}).Parse(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>LLM Test Studio Quick Performance Report {{.Detail.Performance.ReportID}}</title><style>
:root{font-family:Inter,"Segoe UI",sans-serif;color:#172033;background:#eef2f7}*{box-sizing:border-box}body{margin:0;padding:32px}.watermark{position:fixed;inset:42% auto auto 12%;z-index:10;transform:rotate(-24deg);font-size:72px;font-weight:700;letter-spacing:.12em;color:#6070891c;pointer-events:none;white-space:nowrap}main{max-width:1280px;margin:auto;background:#fff;border-radius:18px;padding:36px;box-shadow:0 14px 45px #16233a1c}h1{margin:0;font-size:30px}h2{margin-top:30px}.muted{color:#657189}.pass{color:#16794a}.fail{color:#b42318}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px}.card{border:1px solid #dde4ee;border-radius:12px;padding:13px;background:#f8fafc}.card strong,.card span{display:block}.card span{font-size:12px;color:#657189;margin-top:4px}table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:9px;border-bottom:1px solid #e6eaf0;text-align:right}th:first-child,td:first-child{text-align:left}.scroll{overflow:auto}@media print{body{padding:0;background:#fff}main{box-shadow:none;border-radius:0;max-width:none}tr{break-inside:avoid}}
</style></head><body><div class="watermark">{{.Watermark}}</div><main>
<h1>{{.Labels.QuickTitle}}</h1><p class="muted">{{.Detail.Performance.ModelID}} · {{.Detail.Performance.BaseURL}} · {{.Detail.Performance.GeneratedAt}}</p>
<h2 class="{{if .Detail.Performance.Success}}pass{{else}}fail{{end}}">{{.Labels.Conclusion}}: {{.Conclusion}}</h2>
<p>{{.Labels.Report}} {{.Detail.Performance.ReportID}} · {{.Labels.Phase}} {{.Phase}}</p>
<h2>{{.Labels.CoreMetrics}}</h2><section class="grid">
<div class="card"><strong>{{.Detail.Performance.Metrics.SuccessRatePercent}}%</strong><span>{{.Labels.SuccessRate}}</span></div>
<div class="card"><strong>{{.Detail.Performance.Metrics.RequestQPS}} req/s</strong><span>{{.Labels.RequestRate}}</span></div>
{{if ne .Detail.Performance.SchemaVersion 3}}<div class="card"><strong>{{sampledValue (legacyTTFTSamples .Detail.Performance.Samples) .Detail.Performance.Metrics.TTFTP50}} / {{sampledValue (legacyTTFTSamples .Detail.Performance.Samples) .Detail.Performance.Metrics.TTFTP95}} ms</strong><span>TTFT P50 / P95</span></div>{{end}}
<div class="card"><strong>{{sampledValue (tpotSamples .Detail.Performance.Samples) .Detail.Performance.Metrics.TPOTP50}} / {{sampledValue (tpotSamples .Detail.Performance.Samples) .Detail.Performance.Metrics.TPOTP95}} ms/token</strong><span>TPOT P50 / P95</span></div>
<div class="card"><strong>{{sampledValue .Detail.Performance.Metrics.Succeeded .Detail.Performance.Metrics.E2EP50}} / {{sampledValue .Detail.Performance.Metrics.Succeeded .Detail.Performance.Metrics.E2EP95}} ms</strong><span>E2E P50 / P95</span></div>
</section>
{{if eq .Detail.Performance.SchemaVersion 3}}<h2>{{.Labels.StreamingTiming}}</h2><div class="scroll"><table><thead><tr><th>{{.Labels.Metric}}</th><th>{{.Labels.Average}}</th><th>P50</th><th>P95</th><th>P99</th><th>{{.Labels.Samples}}</th></tr></thead><tbody>
<tr><td>TTFB (ms)</td><td>{{sampledValue .Detail.Performance.Metrics.TTFBSamples .Detail.Performance.Metrics.TTFBAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFBSamples .Detail.Performance.Metrics.TTFBP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFBSamples .Detail.Performance.Metrics.TTFBP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFBSamples .Detail.Performance.Metrics.TTFBP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.TTFBSamples}}</td></tr>
<tr><td>{{.Labels.TTFTAny}} (ms)</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTAnySamples .Detail.Performance.Metrics.TTFTAnyAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTAnySamples .Detail.Performance.Metrics.TTFTAnyP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTAnySamples .Detail.Performance.Metrics.TTFTAnyP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTAnySamples .Detail.Performance.Metrics.TTFTAnyP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.TTFTAnySamples}}</td></tr>
<tr><td>{{.Labels.TTFTVisible}} (ms)</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTVisibleSamples .Detail.Performance.Metrics.TTFTVisibleAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTVisibleSamples .Detail.Performance.Metrics.TTFTVisibleP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTVisibleSamples .Detail.Performance.Metrics.TTFTVisibleP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTFTVisibleSamples .Detail.Performance.Metrics.TTFTVisibleP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.TTFTVisibleSamples}}</td></tr>
<tr><td>{{.Labels.TTST}} (ms)</td><td>{{sampledValue .Detail.Performance.Metrics.TTSTSamples .Detail.Performance.Metrics.TTSTAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTSTSamples .Detail.Performance.Metrics.TTSTP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTSTSamples .Detail.Performance.Metrics.TTSTP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.TTSTSamples .Detail.Performance.Metrics.TTSTP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.TTSTSamples}}</td></tr>
<tr><td>{{.Labels.ObservedICL}} (ms)</td><td>{{sampledValue .Detail.Performance.Metrics.ObservedICLSamples .Detail.Performance.Metrics.ObservedICLAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.ObservedICLSamples .Detail.Performance.Metrics.ObservedICLP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.ObservedICLSamples .Detail.Performance.Metrics.ObservedICLP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.ObservedICLSamples .Detail.Performance.Metrics.ObservedICLP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.ObservedICLSamples}}</td></tr>
<tr><td>{{.Labels.SemanticChunks}}</td><td>{{sampledValue .Detail.Performance.Metrics.SemanticChunkCountSamples .Detail.Performance.Metrics.SemanticChunkCountAverage}}</td><td>{{sampledValue .Detail.Performance.Metrics.SemanticChunkCountSamples .Detail.Performance.Metrics.SemanticChunkCountP50}}</td><td>{{sampledValue .Detail.Performance.Metrics.SemanticChunkCountSamples .Detail.Performance.Metrics.SemanticChunkCountP95}}</td><td>{{sampledValue .Detail.Performance.Metrics.SemanticChunkCountSamples .Detail.Performance.Metrics.SemanticChunkCountP99}}</td><td>{{sampledSamples .Detail.Performance.Metrics.SemanticChunkCountSamples}}</td></tr>
</tbody></table></div>{{end}}
<h2>{{.Labels.RequestSamples}} ({{len .Detail.Performance.Samples}})</h2>{{if eq .Detail.Performance.SchemaVersion 3}}<div class="scroll"><table><thead><tr><th>#</th><th>{{.Labels.Status}}</th><th>HTTP</th><th>E2E ms</th><th>TTFB ms</th><th>{{.Labels.TTFTAny}} ms</th><th>{{.Labels.TTFTVisible}} ms</th><th>{{.Labels.TTST}} ms</th><th>{{.Labels.ObservedICL}} ms</th><th>{{.Labels.SemanticChunks}}</th><th>TPOT ms/token</th><th>{{.Labels.Input}}</th><th>{{.Labels.Output}}</th><th>{{.Labels.Error}}</th></tr></thead><tbody>{{range .Detail.Performance.Samples}}<tr><td>{{.RequestIndex}}</td><td>{{if .Success}}{{$.Labels.StatusPassed}}{{else}}{{$.Labels.StatusFailed}}{{end}}</td><td>{{.HTTPStatus}}</td><td>{{.E2EMS}}</td><td>{{if .TTFBMS}}{{.TTFBMS}}{{else}}-{{end}}</td><td>{{if .TTFTAnyMS}}{{.TTFTAnyMS}}{{else}}-{{end}}</td><td>{{if .TTFTVisibleMS}}{{.TTFTVisibleMS}}{{else}}-{{end}}</td><td>{{if ge .SemanticChunkCount 2}}{{.TTSTMS}}{{else}}-{{end}}</td><td>{{if ge .SemanticChunkCount 2}}{{.ObservedICLMS}}{{else}}-{{end}}</td><td>{{.SemanticChunkCount}}</td><td>{{if .TPOTMS}}{{.TPOTMS}}{{else}}-{{end}}</td><td>{{.PromptTokens}}</td><td>{{.CompletionTokens}}</td><td>{{.ErrorCode}}</td></tr>{{end}}</tbody></table></div>{{else}}<div class="scroll"><table><thead><tr><th>#</th><th>{{.Labels.Status}}</th><th>HTTP</th><th>E2E ms</th><th>TTFT ms</th><th>TPOT ms</th><th>{{.Labels.Input}}</th><th>{{.Labels.Output}}</th><th>{{.Labels.Error}}</th></tr></thead><tbody>{{range .Detail.Performance.Samples}}<tr><td>{{.RequestIndex}}</td><td>{{if .Success}}{{$.Labels.StatusPassed}}{{else}}{{$.Labels.StatusFailed}}{{end}}</td><td>{{.HTTPStatus}}</td><td>{{.E2EMS}}</td><td>{{if .TTFTMS}}{{.TTFTMS}}{{else}}-{{end}}</td><td>{{if .TPOTMS}}{{.TPOTMS}}{{else}}-{{end}}</td><td>{{.PromptTokens}}</td><td>{{.CompletionTokens}}</td><td>{{.ErrorCode}}</td></tr>{{end}}</tbody></table></div>{{end}}
<p class="muted">Schema v{{.Detail.Performance.SchemaVersion}} · {{.Labels.QuickFooter}}</p>
</main></body></html>`))

func renderPNG(detail Detail, watermark string) ([]byte, error) {
	canvas := drawReportImage(detail, watermark)
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func drawReportImage(detail Detail, watermark string) image.Image {
	const width, height = 1400, 1800
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 242, G: 245, B: 249, A: 255}}, image.Point{}, draw.Src)
	fillRect(canvas, 45, 45, width-45, height-45, color.RGBA{255, 255, 255, 255})
	if detail.Source == SourceQuickPerformance && detail.Performance != nil {
		drawQuickPerformanceImage(canvas, *detail.Performance)
		drawWatermark(canvas, watermark)
		return canvas
	}
	drawBitmapText(canvas, 80, 82, 5, "LLM STUDIO REPORT", color.RGBA{23, 32, 51, 255})
	drawBitmapText(canvas, 80, 132, 2, strings.ToUpper(detail.Report.Conclusion.Verdict), conclusionColor(detail.Report.Conclusion.Passed))
	drawBitmapText(canvas, 80, 170, 2, "REPORT "+detail.Report.ID, color.RGBA{91, 105, 128, 255})
	drawBitmapText(canvas, 80, 200, 2, "RUN "+detail.Report.RunID, color.RGBA{91, 105, 128, 255})

	metrics := sortedMetrics(detail.Report.Metrics)
	if len(metrics) > 12 {
		metrics = metrics[:12]
	}
	for index, metric := range metrics {
		column, row := index%3, index/3
		x, y := 80+column*420, 280+row*170
		fillRect(canvas, x, y, x+390, y+140, color.RGBA{248, 250, 252, 255})
		drawBitmapText(canvas, x+20, y+22, 2, strings.ToUpper(metric.Name), color.RGBA{91, 105, 128, 255})
		drawBitmapText(canvas, x+20, y+72, 3, metric.Value+" "+strings.ToUpper(metric.Unit), color.RGBA{23, 32, 51, 255})
	}

	chartY := 1010
	drawBitmapText(canvas, 80, chartY-55, 3, "E2E LATENCY MS", color.RGBA{23, 32, 51, 255})
	values := metricValues(detail.RequestResults, "e2e_ms")
	drawBars(canvas, image.Rect(80, chartY, 1320, chartY+390), values)
	drawBitmapText(canvas, 80, 1440, 3, "REQUEST RESULTS", color.RGBA{23, 32, 51, 255})
	rows := resultRows(detail.RequestResults)
	for index, row := range rows {
		if index >= 3 {
			break
		}
		for lineIndex, line := range resultImageLines(row) {
			drawBitmapText(canvas, 80, 1490+index*88+lineIndex*20, 2, line, color.RGBA{56, 67, 84, 255})
		}
	}
	drawWatermark(canvas, watermark)
	return canvas
}

func drawQuickPerformanceImage(canvas *image.RGBA, report quicktest.PerformanceReport) {
	drawBitmapText(canvas, 80, 82, 5, "LLM STUDIO QUICK PERFORMANCE", color.RGBA{23, 32, 51, 255})
	verdict := "PERFORMANCE TEST FAILED"
	if report.Success {
		verdict = "ALL REQUESTS PASSED"
	}
	drawBitmapText(canvas, 80, 132, 2, verdict, conclusionColor(report.Success))
	drawBitmapText(canvas, 80, 170, 2, "MODEL "+report.ModelID, color.RGBA{91, 105, 128, 255})
	drawBitmapText(canvas, 80, 200, 2, "REPORT "+report.ReportID, color.RGBA{91, 105, 128, 255})
	metrics := quickPerformanceImageMetrics(report)
	for index, metric := range metrics {
		column, row := index%3, index/3
		x, y := 80+column*420, 280+row*170
		fillRect(canvas, x, y, x+390, y+140, color.RGBA{248, 250, 252, 255})
		labelScale := 2
		if len(metric.name) > 29 {
			labelScale = 1
		}
		drawBitmapText(canvas, x+20, y+22, labelScale, metric.name, color.RGBA{91, 105, 128, 255})
		drawBitmapText(canvas, x+20, y+72, 3, metric.value, color.RGBA{23, 32, 51, 255})
	}
	values := make([]float64, 0, len(report.Samples))
	for _, sample := range report.Samples {
		values = append(values, sample.E2EMS)
	}
	if report.SchemaVersion != quicktest.PerformanceSchemaVersion {
		drawBitmapText(canvas, 80, 660, 3, "E2E LATENCY MS", color.RGBA{23, 32, 51, 255})
		drawBars(canvas, image.Rect(80, 710, 1320, 1100), values)
		drawBitmapText(canvas, 80, 1170, 3, "REQUEST SAMPLES", color.RGBA{23, 32, 51, 255})
		for index, sample := range report.Samples {
			if index >= 16 {
				break
			}
			line := quickPerformanceLegacySampleLine(sample)
			drawBitmapText(canvas, 80, 1220+index*26, 2, line, color.RGBA{56, 67, 84, 255})
		}
		return
	}
	drawBitmapText(canvas, 80, 980, 3, "E2E LATENCY MS", color.RGBA{23, 32, 51, 255})
	drawBars(canvas, image.Rect(80, 1030, 1320, 1280), values)
	drawBitmapText(canvas, 80, 1340, 3, "REQUEST SAMPLES", color.RGBA{23, 32, 51, 255})
	for index, sample := range report.Samples {
		if index >= 5 {
			break
		}
		for lineIndex, line := range quickPerformanceSampleLines(sample) {
			drawBitmapText(canvas, 80, 1390+index*72+lineIndex*24, 2, line, color.RGBA{56, 67, 84, 255})
		}
	}
}

type quickPerformanceImageMetric struct {
	name  string
	value string
}

func quickPerformanceImageMetrics(report quicktest.PerformanceReport) []quickPerformanceImageMetric {
	metrics := []quickPerformanceImageMetric{
		{"SUCCESS RATE", formatNumber(report.Metrics.SuccessRatePercent) + " %"},
		{"REQUEST QPS", formatNumber(report.Metrics.RequestQPS)},
	}
	if report.SchemaVersion == quicktest.PerformanceSchemaVersion {
		metrics = append(metrics,
			quickPerformanceImageMetric{"TTFB P50", sampledMetricText(report.Metrics.TTFBSamples, report.Metrics.TTFBP50, " MS")},
			quickPerformanceImageMetric{"TTFT ANY INCLUDES REASONING P50", sampledMetricText(report.Metrics.TTFTAnySamples, report.Metrics.TTFTAnyP50, " MS")},
			quickPerformanceImageMetric{"TTFT VISIBLE CONTENT P50", sampledMetricText(report.Metrics.TTFTVisibleSamples, report.Metrics.TTFTVisibleP50, " MS")},
			quickPerformanceImageMetric{"TTST SECOND SEMANTIC CHUNK P50", sampledMetricText(report.Metrics.TTSTSamples, report.Metrics.TTSTP50, " MS")},
			quickPerformanceImageMetric{"OBS ICL SEMANTIC GAP NOT TOKEN ITL AVG", sampledMetricText(report.Metrics.ObservedICLSamples, report.Metrics.ObservedICLAverage, " MS")},
			quickPerformanceImageMetric{"SEMANTIC CHUNKS AVG", sampledMetricText(report.Metrics.SemanticChunkCountSamples, report.Metrics.SemanticChunkCountAverage, "")},
		)
	} else {
		ttftSamples := quickPerformanceLegacyTTFTSamples(report.Samples)
		metrics = append(metrics,
			quickPerformanceImageMetric{"TTFT P50", sampledMetricText(ttftSamples, report.Metrics.TTFTP50, " MS")},
			quickPerformanceImageMetric{"TTFT P95", sampledMetricText(ttftSamples, report.Metrics.TTFTP95, " MS")},
		)
	}
	tpotSamples := quickPerformanceTPOTSamples(report.Samples)
	return append(metrics,
		quickPerformanceImageMetric{"TPOT P50", sampledMetricText(tpotSamples, report.Metrics.TPOTP50, " MS/TOKEN")},
		quickPerformanceImageMetric{"E2E P95", sampledMetricText(report.Metrics.Succeeded, report.Metrics.E2EP95, " MS")},
	)
}

func resultImageLines(row resultRow) []string {
	errorCode := row.Error
	if errorCode == "" {
		errorCode = "-"
	}
	return []string{
		fmt.Sprintf("%s  %s  E2E %s  TTFB %s", row.RequestID, strings.ToUpper(row.Status), row.E2E, row.TTFB),
		fmt.Sprintf("TTFT ANY INCLUDES REASONING %s  TTFT VISIBLE CONTENT %s", row.TTFT, row.TTFTVisible),
		fmt.Sprintf("TTST SECOND SEMANTIC CHUNK %s  OBS ICL SEMANTIC GAP NOT TOKEN ITL %s", row.TTST, row.ObservedICL),
		fmt.Sprintf("SEMANTIC CHUNKS %s  ERROR %s", row.SemanticChunk, errorCode),
	}
}

func quickPerformanceSampleLines(sample quicktest.PerformanceSample) []string {
	status := "PASSED"
	if !sample.Success {
		status = "FAILED"
	}
	semanticIntervalAvailable := sample.SemanticChunkCount >= 2
	return []string{
		fmt.Sprintf("%d  %s  HTTP %d  E2E %.2f  TTFB %s  TTFT ANY INCLUDES REASONING %s", sample.RequestIndex, status, sample.HTTPStatus, sample.E2EMS, optionalPerformanceMetric(sample.TTFBMS, sample.TTFBMS > 0), optionalPerformanceMetric(sample.TTFTAnyMS, sample.TTFTAnyMS > 0)),
		fmt.Sprintf("TTFT VISIBLE CONTENT %s  TTST SECOND SEMANTIC CHUNK %s", optionalPerformanceMetric(sample.TTFTVisibleMS, sample.TTFTVisibleMS > 0), optionalPerformanceMetric(sample.TTSTMS, semanticIntervalAvailable)),
		fmt.Sprintf("OBS ICL SEMANTIC GAP NOT TOKEN ITL %s  CHUNKS %d  TPOT %s", optionalPerformanceMetric(sample.ObservedICLMS, semanticIntervalAvailable), sample.SemanticChunkCount, optionalPerformanceMetric(sample.TPOTMS, sample.TPOTMS > 0)),
	}
}

func quickPerformanceLegacySampleLine(sample quicktest.PerformanceSample) string {
	status := "PASSED"
	if !sample.Success {
		status = "FAILED"
	}
	return fmt.Sprintf("%d  %s  HTTP %d  E2E %.2f  TTFT %s  TPOT %s", sample.RequestIndex, status, sample.HTTPStatus, sample.E2EMS, optionalPerformanceMetric(sample.TTFTMS, sample.TTFTMS > 0), optionalPerformanceMetric(sample.TPOTMS, sample.TPOTMS > 0))
}

func optionalPerformanceMetric(value float64, available bool) string {
	if !available {
		return "-"
	}
	return formatNumber(value)
}

func quickPerformanceLegacyTTFTSamples(samples []quicktest.PerformanceSample) uint64 {
	var count uint64
	for _, sample := range samples {
		if sample.Success && sample.TTFTMS > 0 {
			count++
		}
	}
	return count
}

func quickPerformanceTPOTSamples(samples []quicktest.PerformanceSample) uint64 {
	var count uint64
	for _, sample := range samples {
		if sample.Success && sample.TPOTMS > 0 {
			count++
		}
	}
	return count
}

func sampledMetricText(samples uint64, value float64, suffix string) string {
	if samples == 0 {
		return "-"
	}
	return formatNumber(value) + suffix
}

func sampledMetricValue(samples uint64, value float64) string {
	return sampledMetricText(samples, value, "")
}

func sampledMetricSamples(samples uint64) string {
	if samples == 0 {
		return "-"
	}
	return strconv.FormatUint(samples, 10)
}

func drawWatermark(canvas *image.RGBA, watermark string) {
	label := "WATERMARK " + watermark
	ink := color.RGBA{R: 220, G: 226, B: 234, A: 255}
	for y := 520; y < canvas.Bounds().Dy()-100; y += 360 {
		for x := 140; x < canvas.Bounds().Dx()-300; x += 620 {
			drawBitmapText(canvas, x, y, 4, label, ink)
		}
	}
}

func metricValues(results []domain.Result, name string) []float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		if value, ok := result.Metrics[name]; ok && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			values = append(values, value)
		}
	}
	return values
}

func drawBars(canvas *image.RGBA, bounds image.Rectangle, values []float64) {
	fillRect(canvas, bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, color.RGBA{248, 250, 252, 255})
	if len(values) == 0 {
		drawBitmapText(canvas, bounds.Min.X+24, bounds.Min.Y+24, 2, "NO SAMPLES", color.RGBA{91, 105, 128, 255})
		return
	}
	maxValue := values[0]
	for _, value := range values[1:] {
		maxValue = math.Max(maxValue, value)
	}
	if maxValue == 0 {
		maxValue = 1
	}
	count := len(values)
	if count > 80 {
		count = 80
	}
	gap := 3
	barWidth := (bounds.Dx() - 48 - gap*(count-1)) / count
	if barWidth < 2 {
		barWidth = 2
	}
	for index := 0; index < count; index++ {
		barHeight := int(values[index] / maxValue * float64(bounds.Dy()-70))
		x := bounds.Min.X + 24 + index*(barWidth+gap)
		fillRect(canvas, x, bounds.Max.Y-35-barHeight, x+barWidth, bounds.Max.Y-35, color.RGBA{37, 99, 235, 255})
	}
	drawBitmapText(canvas, bounds.Min.X+24, bounds.Max.Y-26, 2, "0", color.RGBA{91, 105, 128, 255})
	drawBitmapText(canvas, bounds.Max.X-180, bounds.Max.Y-26, 2, "MAX "+formatNumber(maxValue), color.RGBA{91, 105, 128, 255})
}

func conclusionColor(passed bool) color.RGBA {
	if passed {
		return color.RGBA{22, 121, 74, 255}
	}
	return color.RGBA{180, 35, 24, 255}
}

func fillRect(canvas *image.RGBA, x1, y1, x2, y2 int, value color.RGBA) {
	draw.Draw(canvas, image.Rect(x1, y1, x2, y2), &image.Uniform{C: value}, image.Point{}, draw.Src)
}

func drawBitmapText(canvas *image.RGBA, x, y, scale int, value string, ink color.RGBA) {
	for _, character := range strings.ToUpper(value) {
		glyph, ok := bitmapGlyphs[character]
		if !ok {
			glyph = bitmapGlyphs['?']
		}
		for row, bits := range glyph {
			for column := 0; column < 5; column++ {
				if bits&(1<<uint(4-column)) != 0 {
					fillRect(canvas, x+column*scale, y+row*scale, x+(column+1)*scale, y+(row+1)*scale, ink)
				}
			}
		}
		x += 6 * scale
	}
}

var bitmapGlyphs = map[rune][7]byte{
	' ': {}, '?': {14, 17, 1, 2, 4, 0, 4}, '-': {0, 0, 0, 31}, '.': {0, 0, 0, 0, 0, 6, 6}, '/': {1, 2, 4, 8, 16}, ':': {0, 4, 4, 0, 4, 4}, '_': {0, 0, 0, 0, 0, 0, 31},
	'0': {14, 17, 19, 21, 25, 17, 14}, '1': {4, 12, 4, 4, 4, 4, 14}, '2': {14, 17, 1, 2, 4, 8, 31}, '3': {30, 1, 1, 14, 1, 1, 30}, '4': {2, 6, 10, 18, 31, 2, 2}, '5': {31, 16, 16, 30, 1, 1, 30}, '6': {14, 16, 16, 30, 17, 17, 14}, '7': {31, 1, 2, 4, 8, 8, 8}, '8': {14, 17, 17, 14, 17, 17, 14}, '9': {14, 17, 17, 15, 1, 1, 14},
	'A': {14, 17, 17, 31, 17, 17, 17}, 'B': {30, 17, 17, 30, 17, 17, 30}, 'C': {14, 17, 16, 16, 16, 17, 14}, 'D': {30, 17, 17, 17, 17, 17, 30}, 'E': {31, 16, 16, 30, 16, 16, 31}, 'F': {31, 16, 16, 30, 16, 16, 16}, 'G': {14, 17, 16, 23, 17, 17, 15}, 'H': {17, 17, 17, 31, 17, 17, 17}, 'I': {14, 4, 4, 4, 4, 4, 14}, 'J': {7, 2, 2, 2, 18, 18, 12}, 'K': {17, 18, 20, 24, 20, 18, 17}, 'L': {16, 16, 16, 16, 16, 16, 31}, 'M': {17, 27, 21, 21, 17, 17, 17}, 'N': {17, 25, 21, 19, 17, 17, 17}, 'O': {14, 17, 17, 17, 17, 17, 14}, 'P': {30, 17, 17, 30, 16, 16, 16}, 'Q': {14, 17, 17, 17, 21, 18, 13}, 'R': {30, 17, 17, 30, 20, 18, 17}, 'S': {15, 16, 16, 14, 1, 1, 30}, 'T': {31, 4, 4, 4, 4, 4, 4}, 'U': {17, 17, 17, 17, 17, 17, 14}, 'V': {17, 17, 17, 17, 17, 10, 4}, 'W': {17, 17, 17, 21, 21, 21, 10}, 'X': {17, 17, 10, 4, 10, 17, 17}, 'Y': {17, 17, 10, 4, 4, 4, 4}, 'Z': {31, 1, 2, 4, 8, 16, 31},
}

func renderPDF(detail Detail, watermark string) ([]byte, error) {
	canvas := drawReportImage(detail, watermark)
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, canvas, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return imagePDF(jpegData.Bytes(), canvas.Bounds().Dx(), canvas.Bounds().Dy()), nil
}

func imagePDF(jpegData []byte, width, height int) []byte {
	pageWidth, pageHeight := 595.0, 842.0
	scale := math.Min(pageWidth/float64(width), pageHeight/float64(height))
	drawWidth, drawHeight := float64(width)*scale, float64(height)*scale
	x, y := (pageWidth-drawWidth)/2, (pageHeight-drawHeight)/2
	content := fmt.Sprintf("q %.3f 0 0 %.3f %.3f %.3f cm /Im0 Do Q", drawWidth, drawHeight, x, y)
	objects := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", pageWidth, pageHeight)),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)),
		append([]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, len(jpegData))), append(jpegData, []byte("\nendstream")...)...),
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n", index+1)
		output.Write(object)
		output.WriteString("\nendobj\n")
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
