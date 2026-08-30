package apiaudit

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test/engine/common"
)

var signedURLPattern = regexp.MustCompile(`https?://[^\s"<>?]+\?[^\s"<>]+`)
var bearerSecretPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]+`)
var keySecretPattern = regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{8,}`)

func BuildReport(config RunConfig, results []CaseResult) Report {
	report := Report{
		Title: "API 准入审计报告", GeneratedAt: time.Now(), Suite: config.Suite,
		BaseURL: redactURL(config.BaseURL), Model: config.Model, Results: append([]CaseResult(nil), results...), APIKey: config.APIKey,
	}
	if config.Suite == "kimi-k3" {
		report.Notices = []ReportNotice{{
			Title: "固定参数处理策略",
			Body:  "Kimi-K3 官方参数仅支持固定值且不允许调整。审计与接入时忽略调用方传入的这些参数，统一使用官方默认值；因此不将参数不可修改判定为兼容性缺陷。",
			Items: []string{
				"temperature：忽略请求值，使用官方默认值",
				"top_p：0.95",
				"n：1",
				"presence_penalty：0",
				"frequency_penalty：0",
			},
		}, {
			Title: "Thinking 处理策略",
			Body:  "Kimi-K3 官方要求 thinking 始终开启。审计与接入时不提供关闭或切换能力，仅保留未传开关时默认开启的基线测试。",
			Items: []string{
				"thinking：始终开启",
				"关闭值与兼容别名：忽略，不参与测试",
			},
		}}
	}
	dimensions := make(map[string]SummaryCounts)
	criticalFailures := make([]string, 0)
	for _, result := range results {
		counts := dimensions[result.Dimension]
		switch result.Status {
		case StatusPass:
			report.Summary.Pass++
			counts.Pass++
		case StatusWarning:
			report.Summary.Warning++
			counts.Warning++
			report.Warnings = append(report.Warnings, result)
		case StatusFail:
			report.Summary.Fail++
			counts.Fail++
			report.Failures = append(report.Failures, result)
			if result.Severity == "critical" {
				criticalFailures = append(criticalFailures, result.ID)
			}
		default:
			report.Summary.Unknown++
			counts.Unknown++
		}
		dimensions[result.Dimension] = counts
	}
	keys := make([]string, 0, len(dimensions))
	for dimension := range dimensions {
		keys = append(keys, dimension)
	}
	sort.Strings(keys)
	for _, dimension := range keys {
		report.Dimensions = append(report.Dimensions, DimensionSummary{Dimension: dimension, SummaryCounts: dimensions[dimension]})
	}
	if len(criticalFailures) > 0 {
		report.Overall = "rejected"
		report.Verdict = fmt.Sprintf("不合格：%d 项失败，其中 %d 项为 CRITICAL", report.Summary.Fail, len(criticalFailures))
	} else if report.Summary.Fail > 0 {
		report.Overall = "rejected"
		report.Verdict = fmt.Sprintf("不合格：%d 项测试失败", report.Summary.Fail)
	} else if report.Summary.Warning > 0 || report.Summary.Unknown > 0 {
		report.Overall = "review"
		report.Verdict = fmt.Sprintf("需复核：%d 项警告，%d 项无法判定", report.Summary.Warning, report.Summary.Unknown)
	} else {
		report.Overall = "qualified"
		report.Verdict = "合格：所有已执行测试通过"
	}
	return report
}

func WriteReport(outputDir string, report Report) error {
	if outputDir == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	safeReport := report
	safeReport.Results = redactCaseResults(report.Results, report.APIKey)
	safeReport.Failures = redactCaseResults(report.Failures, report.APIKey)
	safeReport.Warnings = redactCaseResults(report.Warnings, report.APIKey)
	rawRoot := filepath.Join(outputDir, "raw")
	for i := range safeReport.Results {
		if !safeResultIDPattern.MatchString(safeReport.Results[i].ID) {
			return fmt.Errorf("unsafe result id %q", safeReport.Results[i].ID)
		}
		artifactDir := filepath.Join(rawRoot, safeReport.Results[i].ID)
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return fmt.Errorf("create artifact directory: %w", err)
		}
		for j := range safeReport.Results[i].Exchanges {
			exchange := safeReport.Results[i].Exchanges[j]
			encoded, err := common.Marshal(exchange)
			if err != nil {
				return fmt.Errorf("encode exchange: %w", err)
			}
			if err := os.WriteFile(filepath.Join(artifactDir, fmt.Sprintf("exchange-%02d.json", j+1)), encoded, 0o644); err != nil {
				return fmt.Errorf("write exchange: %w", err)
			}
		}
		safeReport.Results[i].ArtifactDir = filepath.ToSlash(filepath.Join("raw", safeReport.Results[i].ID))
	}
	encoded, err := common.Marshal(safeReport)
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "report.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}

	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"total": func(summary SummaryCounts) int {
			return summary.Pass + summary.Warning + summary.Fail + summary.Unknown
		},
		"passRate": func(summary SummaryCounts) string {
			total := summary.Pass + summary.Warning + summary.Fail + summary.Unknown
			if total == 0 {
				return "0%"
			}
			return fmt.Sprintf("%.1f%%", float64(summary.Pass)/float64(total)*100)
		},
		"statusLabel": func(status string) string {
			switch status {
			case StatusPass:
				return "通过"
			case StatusWarning:
				return "警告"
			case StatusFail:
				return "失败"
			default:
				return "待确认"
			}
		},
		"overallLabel": func(overall string) string {
			switch overall {
			case "qualified":
				return "准入通过"
			case "review":
				return "需要复核"
			default:
				return "准入未通过"
			}
		},
	}).Parse(reportHTMLTemplate)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	file, err := os.Create(filepath.Join(outputDir, "report.html"))
	if err != nil {
		return fmt.Errorf("create HTML report: %w", err)
	}
	executeErr := tmpl.Execute(file, safeReport)
	closeErr := file.Close()
	if executeErr != nil {
		return fmt.Errorf("render HTML report: %w", executeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close HTML report: %w", closeErr)
	}
	return nil
}

func redactCaseResults(results []CaseResult, apiKey string) []CaseResult {
	if len(results) == 0 {
		return nil
	}
	redacted := make([]CaseResult, len(results))
	for i, result := range results {
		result.Evidence = redactText(result.Evidence, apiKey)
		result.Exchanges = append([]HTTPExchange(nil), result.Exchanges...)
		for j := range result.Exchanges {
			result.Exchanges[j].URL = redactURL(result.Exchanges[j].URL)
			result.Exchanges[j].ResponseBody = redactResponseBody(result.Exchanges[j].ResponseBody, apiKey)
			if result.Exchanges[j].RequestBody != nil {
				result.Exchanges[j].RequestBody = redactJSONValue(result.Exchanges[j].RequestBody, apiKey).(map[string]any)
			}
		}
		if result.Usage != nil {
			result.Usage = redactJSONValue(result.Usage, apiKey).(map[string]any)
		}
		if result.Metrics != nil {
			result.Metrics = redactJSONValue(result.Metrics, apiKey).(map[string]any)
		}
		redacted[i] = result
	}
	return redacted
}

// RedactCaseResult returns a safe copy for machine-readable progress events.
// Reports use the same redaction path before anything is written to disk.
func RedactCaseResult(result CaseResult, apiKey string) CaseResult {
	return redactCaseResults([]CaseResult{result}, apiKey)[0]
}

func redactResponseBody(body, apiKey string) string {
	var value any
	if err := common.Unmarshal([]byte(body), &value); err == nil {
		if encoded, marshalErr := common.Marshal(redactJSONValue(value, apiKey)); marshalErr == nil {
			return string(encoded)
		}
	}
	return redactText(body, apiKey)
}

func redactText(value, apiKey string) string {
	value = signedURLPattern.ReplaceAllStringFunc(value, redactURL)
	value = bearerSecretPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = keySecretPattern.ReplaceAllString(value, "sk-[REDACTED]")
	if apiKey != "" {
		value = strings.ReplaceAll(value, apiKey, "[REDACTED]")
	}
	return value
}

func redactJSONValue(value any, apiKey string) any {
	switch typed := value.(type) {
	case string:
		return redactText(typed, apiKey)
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactJSONValue(item, apiKey)
		}
		return redacted
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			lowerKey := strings.ToLower(key)
			if lowerKey == "authorization" || lowerKey == "api_key" || lowerKey == "apikey" || lowerKey == "access_token" || lowerKey == "secret" || lowerKey == "token" {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = redactJSONValue(item, apiKey)
		}
		return redacted
	default:
		return value
	}
}

const reportHTMLTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} - {{.Model}}</title><style>
:root{color-scheme:light;--ink:#17202d;--muted:#667085;--line:#e4e7ec;--panel:#fff;--canvas:#f3f5f8;--pass:#15803d;--pass-soft:#eaf7ef;--warn:#a15c00;--warn-soft:#fff5df;--fail:#b42318;--fail-soft:#fff0ee;--unknown:#475467;--unknown-soft:#eef1f5;--accent:#3157d5;--accent-soft:#edf2ff;--shadow:0 10px 30px rgba(16,24,40,.06)}
*{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;overflow-x:hidden;background:var(--canvas);color:var(--ink);font-family:Inter,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;line-height:1.55}.wrap{width:min(1240px,calc(100% - 40px));margin:32px auto 64px}.card{margin-top:18px;padding:24px;background:var(--panel);border:1px solid var(--line);border-radius:16px;box-shadow:var(--shadow)}h1,h2,h3,p{margin-top:0}h1{margin-bottom:8px;font-size:clamp(28px,4vw,42px);line-height:1.16;letter-spacing:-.03em}h2{margin-bottom:4px;font-size:20px;letter-spacing:-.01em}.section-heading{display:flex;align-items:flex-end;justify-content:space-between;gap:16px;margin-bottom:18px}.section-heading p{margin:4px 0 0;color:var(--muted)}a{color:var(--accent);text-underline-offset:3px}.eyebrow{margin-bottom:12px;color:var(--accent);font-size:12px;font-weight:800;letter-spacing:.12em;text-transform:uppercase}
.hero{padding:28px;background:#101828;color:#fff;border-color:#101828;overflow:hidden}.hero-top{display:flex;align-items:flex-start;justify-content:space-between;gap:24px}.hero-copy{max-width:780px}.hero .eyebrow{color:#a9bcff}.status-badge{display:inline-flex;align-items:center;gap:8px;flex:0 0 auto;padding:8px 12px;border:1px solid rgba(255,255,255,.18);border-radius:999px;background:rgba(255,255,255,.09);font-size:13px;font-weight:800}.status-dot{width:8px;height:8px;border-radius:50%;background:#f97066;box-shadow:0 0 0 4px rgba(249,112,102,.16)}.status-badge.qualified .status-dot{background:#47cd89}.status-badge.review .status-dot{background:#fdb022}.verdict{margin:0;color:#d0d5dd;font-size:16px}.meta-grid{display:grid;grid-template-columns:1fr 1fr 1.5fr 1.2fr;gap:12px;margin-top:26px;padding-top:20px;border-top:1px solid rgba(255,255,255,.14)}.meta-item span{display:block;margin-bottom:3px;color:#98a2b3;font-size:11px;font-weight:700;letter-spacing:.06em;text-transform:uppercase}.meta-item strong{display:block;color:#f2f4f7;font-size:13px;font-weight:600;overflow-wrap:anywhere}
.notice{border-color:#b9c7f8;background:#f7f9ff}.notice-layout{display:grid;grid-template-columns:minmax(220px,.75fr) minmax(0,2fr);gap:28px;align-items:start}.notice h2{margin-bottom:7px}.notice p{margin:0;color:#475467}.notice-list{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin:0;padding:0;list-style:none}.notice-list li{padding:10px 12px;border:1px solid #d7dffb;border-radius:9px;background:#fff;color:#344054;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;font-weight:650;overflow-wrap:anywhere}
.overview{display:grid;grid-template-columns:1.35fr 2fr;gap:18px}.score-card{display:flex;align-items:center;gap:22px;min-height:178px}.score-ring{display:grid;place-items:center;width:112px;height:112px;flex:0 0 auto;border-radius:50%;background:conic-gradient(var(--pass) 0 var(--score),#e6eaf0 var(--score) 100%);position:relative}.score-ring:after{content:"";position:absolute;inset:10px;border-radius:50%;background:#fff}.score-value{position:relative;z-index:1;text-align:center}.score-value strong{display:block;font-size:27px;line-height:1}.score-value span{color:var(--muted);font-size:11px}.score-copy strong{display:block;margin-bottom:5px;font-size:18px}.score-copy p{margin:0;color:var(--muted)}.summary-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:10px}.metric{min-width:0;padding:18px;border:1px solid var(--line);border-radius:12px;background:#fafbfc}.metric span{display:block;color:var(--muted);font-size:12px}.metric strong{display:block;margin:4px 0 1px;font-size:30px;line-height:1}.metric.pass strong{color:var(--pass)}.metric.warning strong{color:var(--warn)}.metric.fail strong{color:var(--fail)}.metric.unknown strong{color:var(--unknown)}
.dimension-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.dimension{padding:16px;border:1px solid var(--line);border-radius:12px}.dimension-head,.dimension-counts{display:flex;align-items:center;justify-content:space-between;gap:12px}.dimension-head strong{font-size:14px;overflow-wrap:anywhere}.dimension-total{color:var(--muted);font-size:12px}.dimension-bar{display:flex;height:8px;margin:12px 0 10px;overflow:hidden;border-radius:99px;background:#edf0f3}.dimension-bar span{flex:var(--value) 1 0;min-width:0}.dimension-bar .bar-pass{background:#47b86b}.dimension-bar .bar-warning{background:#fdb022}.dimension-bar .bar-fail{background:#f97066}.dimension-bar .bar-unknown{background:#98a2b3}.dimension-counts{justify-content:flex-start;flex-wrap:wrap;color:var(--muted);font-size:12px}.dimension-counts b{color:var(--ink);font-weight:700}.count-fail b{color:var(--fail)}
.issue-list{display:grid;gap:10px}.issue{display:grid;grid-template-columns:auto minmax(240px,.9fr) minmax(280px,1.4fr) auto;align-items:center;gap:14px;padding:14px 16px;border:1px solid #f0c9c4;border-left:4px solid var(--fail);border-radius:10px;background:#fffbfa}.issue.warning-issue{border-color:#f3d9a6;border-left-color:var(--warn);background:#fffdfa}.issue-severity{padding:3px 7px;border-radius:6px;background:var(--fail-soft);color:var(--fail);font-size:10px;font-weight:900;letter-spacing:.05em}.warning-issue .issue-severity{background:var(--warn-soft);color:var(--warn)}.issue-title{min-width:0}.issue-title strong,.issue-title span{display:block}.issue-title strong{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;overflow-wrap:anywhere}.issue-title span{margin-top:2px;color:#344054}.issue-evidence{margin:0;color:#475467;overflow-wrap:anywhere}.issue-link{white-space:nowrap;font-weight:700}
.toolbar{display:flex;align-items:center;justify-content:space-between;gap:16px;margin:18px 0 14px}.filters{display:flex;gap:7px;flex-wrap:wrap}.filter-button{appearance:none;padding:8px 12px;border:1px solid var(--line);border-radius:999px;background:#fff;color:#344054;font:inherit;font-weight:700;cursor:pointer}.filter-button:hover{border-color:#98a2b3}.filter-button[aria-pressed="true"]{border-color:#101828;background:#101828;color:#fff}.search-wrap{position:relative;min-width:260px}.search-wrap:before{content:"⌕";position:absolute;left:11px;top:50%;transform:translateY(-52%);color:var(--muted);font-size:19px}.search-input{width:100%;padding:9px 12px 9px 34px;border:1px solid var(--line);border-radius:9px;background:#fff;color:var(--ink);font:inherit}.search-input:focus{outline:3px solid rgba(49,87,213,.14);border-color:var(--accent)}.table-shell{overflow:auto;border:1px solid var(--line);border-radius:12px}.results-table{width:100%;border-collapse:separate;border-spacing:0;table-layout:fixed}.results-table th,.results-table td{padding:13px 12px;border-bottom:1px solid #edf0f3;text-align:left;vertical-align:top}.results-table th{position:sticky;top:0;z-index:1;background:#f8fafc;color:#475467;font-size:11px;letter-spacing:.04em;text-transform:uppercase}.results-table tr[hidden]{display:none}.results-table tr:last-child td{border-bottom:0}.results-table tbody tr:hover{background:#fafbfc}.results-table .col-status{width:88px}.results-table .col-case{width:30%}.results-table .col-dimension{width:15%}.results-table .col-http{width:105px}.results-table .col-evidence{width:auto}.results-table .col-action{width:110px}.case-id{display:block;margin-bottom:4px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11px;font-weight:700;color:#475467;overflow-wrap:anywhere}.case-name{display:block;font-weight:700;color:#1d2939}.status-tag{display:inline-flex;align-items:center;padding:4px 8px;border-radius:999px;font-size:11px;font-weight:800}.status-tag.pass{background:var(--pass-soft);color:var(--pass)}.status-tag.warning{background:var(--warn-soft);color:var(--warn)}.status-tag.fail{background:var(--fail-soft);color:var(--fail)}.status-tag.unknown{background:var(--unknown-soft);color:var(--unknown)}.http-meta strong,.http-meta span{display:block}.http-meta span{color:var(--muted);font-size:11px}.evidence-text{margin:0;color:#344054;overflow-wrap:anywhere}.payload{margin-top:7px}.payload summary{color:var(--accent);font-size:12px;cursor:pointer}.payload-grid{display:grid;grid-template-columns:1fr;gap:8px;margin-top:7px}.payload pre{max-height:180px;margin:0;padding:9px;overflow:auto;border-radius:7px;background:#f2f4f7;color:#344054;font:11px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;overflow-wrap:anywhere}.raw-link{display:inline-flex;align-items:center;gap:4px;font-size:12px;font-weight:700}.empty-state{display:none;padding:34px;text-align:center;color:var(--muted)}.results-meta{color:var(--muted);font-size:12px}.results-meta strong{color:var(--ink)}
@media(max-width:900px){.meta-grid{grid-template-columns:1fr 1fr}.overview{grid-template-columns:1fr}.issue{grid-template-columns:auto 1fr auto}.issue-evidence{grid-column:2/4}.summary-grid{grid-template-columns:repeat(4,1fr)}}
@media(max-width:700px){.wrap{width:min(100% - 24px,1240px);margin:12px auto 36px}.card{padding:18px;border-radius:13px}.hero{padding:22px}.hero-top{display:block}.status-badge{margin-top:18px}.meta-grid{grid-template-columns:1fr 1fr;gap:14px}.notice-layout{grid-template-columns:1fr;gap:16px}.notice-list{grid-template-columns:1fr}.score-card{align-items:flex-start;min-height:0}.score-ring{width:96px;height:96px}.summary-grid{grid-template-columns:1fr 1fr}.metric{padding:14px}.dimension-grid{grid-template-columns:1fr}.issue{grid-template-columns:auto 1fr}.issue-evidence,.issue-link{grid-column:1/3}.toolbar{align-items:stretch;flex-direction:column}.search-wrap{min-width:0}.table-shell{border:0;overflow:visible}.results-table,.results-table tbody{display:block}.results-table thead{display:none}.results-table tbody{display:grid;gap:10px}.results-table tr{display:grid;grid-template-columns:auto 1fr auto;padding:14px;border:1px solid var(--line);border-radius:11px;background:#fff}.results-table td{display:block;padding:5px 0;border:0}.results-table td:nth-child(1){grid-column:1}.results-table td:nth-child(2){grid-column:2/4;padding-left:10px}.results-table td:nth-child(3){grid-column:2/4;padding-left:10px;color:var(--muted);font-size:12px}.results-table td:nth-child(4){grid-column:3;grid-row:1;text-align:right}.results-table td:nth-child(5),.results-table td:nth-child(6){grid-column:1/4}.results-table td:nth-child(5){padding-top:9px;border-top:1px solid #edf0f3}.results-table td:nth-child(6){padding-top:7px}.payload-grid{grid-template-columns:1fr}.empty-state{border:1px solid var(--line);border-radius:11px}.section-heading{align-items:flex-start;flex-direction:column}}
@media(max-width:420px){.meta-grid{grid-template-columns:1fr}.score-card{gap:15px}.score-ring{width:88px;height:88px}.score-value strong{font-size:23px}}
@media(prefers-reduced-motion:reduce){html{scroll-behavior:auto}}
</style></head><body><main class="wrap" data-total="{{total .Summary}}">
<section class="card hero">
  <div class="hero-top"><div class="hero-copy"><div class="eyebrow">API AUDIT · {{.Suite}}</div><h1>{{.Model}} 准入测试</h1><p class="verdict">{{.Verdict}}</p></div><div class="status-badge {{.Overall}}"><span class="status-dot" aria-hidden="true"></span>{{overallLabel .Overall}}</div></div>
  <div class="meta-grid"><div class="meta-item"><span>模型</span><strong>{{.Model}}</strong></div><div class="meta-item"><span>测试套件</span><strong>{{.Suite}}</strong></div><div class="meta-item"><span>接口地址</span><strong>{{.BaseURL}}</strong></div><div class="meta-item"><span>生成时间</span><strong>{{.GeneratedAt.Format "2006-01-02 15:04:05 MST"}}</strong></div></div>
</section>
{{range .Notices}}<section class="card notice"><div class="notice-layout"><div><div class="eyebrow">接入约定</div><h2>{{.Title}}</h2><p>{{.Body}}</p></div>{{if .Items}}<ul class="notice-list">{{range .Items}}<li>{{.}}</li>{{end}}</ul>{{end}}</div></section>{{end}}
<div class="overview">
  <section class="card score-card" aria-label="通过率"><div class="score-ring" style="--score:{{passRate .Summary}}"><div class="score-value"><strong>{{passRate .Summary}}</strong><span>通过率</span></div></div><div class="score-copy"><div class="eyebrow">总体结果</div><strong>{{.Summary.Pass}} / {{total .Summary}} 项通过</strong><p>先查看失败分布，再进入用例明细定位证据。</p></div></section>
  <section class="card"><div class="section-heading"><div><h2>结果概览</h2><p>共执行 {{total .Summary}} 个测试用例</p></div></div><div class="summary-grid"><div class="metric pass"><span>通过</span><strong>{{.Summary.Pass}}</strong><span>符合预期</span></div><div class="metric fail"><span>失败</span><strong>{{.Summary.Fail}}</strong><span>需要处理</span></div><div class="metric warning"><span>警告</span><strong>{{.Summary.Warning}}</strong><span>建议复核</span></div><div class="metric unknown"><span>待确认</span><strong>{{.Summary.Unknown}}</strong><span>无法判定</span></div></div></section>
</div>
<section class="card"><div class="section-heading"><div><h2>能力维度</h2><p>颜色条显示每个维度的通过与风险占比</p></div></div><div class="dimension-grid">{{range .Dimensions}}<article class="dimension"><div class="dimension-head"><strong>{{.Dimension}}</strong><span class="dimension-total">共 {{total .SummaryCounts}} 项</span></div><div class="dimension-bar" aria-label="通过 {{.Pass}}，警告 {{.Warning}}，失败 {{.Fail}}，待确认 {{.Unknown}}"><span class="bar-pass" style="--value:{{.Pass}}"></span><span class="bar-warning" style="--value:{{.Warning}}"></span><span class="bar-fail" style="--value:{{.Fail}}"></span><span class="bar-unknown" style="--value:{{.Unknown}}"></span></div><div class="dimension-counts"><span><b>{{.Pass}}</b> 通过</span><span class="count-fail"><b>{{.Fail}}</b> 失败</span>{{if .Warning}}<span><b>{{.Warning}}</b> 警告</span>{{end}}{{if .Unknown}}<span><b>{{.Unknown}}</b> 待确认</span>{{end}}</div></article>{{end}}</div></section>
{{if .Failures}}<section class="card"><div class="section-heading"><div><h2>需要处理</h2><p>{{len .Failures}} 个失败项，CRITICAL 项会直接阻断准入</p></div><a href="#case-results">查看完整明细 ↓</a></div><div class="issue-list">{{range .Failures}}<article class="issue"><span class="issue-severity">{{if eq .Severity "critical"}}CRITICAL{{else}}FAILED{{end}}</span><div class="issue-title"><strong>{{.ID}}</strong><span>{{.Name}}</span></div><p class="issue-evidence">{{.Evidence}}</p><a class="issue-link" href="#case-{{.ID}}">定位用例</a></article>{{end}}</div></section>{{end}}
{{if .Warnings}}<section class="card"><div class="section-heading"><div><h2>建议复核</h2><p>{{len .Warnings}} 个警告项不会直接阻断准入</p></div></div><div class="issue-list">{{range .Warnings}}<article class="issue warning-issue"><span class="issue-severity">WARNING</span><div class="issue-title"><strong>{{.ID}}</strong><span>{{.Name}}</span></div><p class="issue-evidence">{{.Evidence}}</p><a class="issue-link" href="#case-{{.ID}}">定位用例</a></article>{{end}}</div></section>{{end}}
<section class="card" id="case-results"><div class="section-heading"><div><h2>用例明细</h2><p>按结果筛选，或搜索用例 ID、名称、维度和依据</p></div><div class="results-meta">显示 <strong id="visible-count">{{total .Summary}}</strong> / {{total .Summary}} 项</div></div>
  <div class="toolbar" role="search" aria-label="用例筛选"><div class="filters" aria-label="按结果筛选"><button class="filter-button" type="button" data-filter="all" aria-pressed="true">全部 {{total .Summary}}</button><button class="filter-button" type="button" data-filter="fail" aria-pressed="false">失败 {{.Summary.Fail}}</button><button class="filter-button" type="button" data-filter="pass" aria-pressed="false">通过 {{.Summary.Pass}}</button><button class="filter-button" type="button" data-filter="warning" aria-pressed="false">警告 {{.Summary.Warning}}</button><button class="filter-button" type="button" data-filter="unknown" aria-pressed="false">待确认 {{.Summary.Unknown}}</button></div><label class="search-wrap"><span style="position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0">搜索用例</span><input class="search-input" id="case-search" type="search" placeholder="搜索用例、维度或依据"></label></div>
  <div class="table-shell"><table class="results-table"><thead><tr><th class="col-status">结果</th><th class="col-case">测试用例</th><th class="col-dimension">维度</th><th class="col-http">请求</th><th class="col-evidence">判定依据</th><th class="col-action">记录</th></tr></thead><tbody id="case-list">{{range .Results}}<tr id="case-{{.ID}}" data-status="{{.Status}}" data-search="{{.ID}} {{.Name}} {{.Dimension}} {{.Evidence}}"><td><span class="status-tag {{.Status}}">{{statusLabel .Status}}</span></td><td><span class="case-id">{{.ID}}</span><span class="case-name">{{.Name}}</span></td><td>{{.Dimension}}</td><td><div class="http-meta"><strong>{{if .HTTPStatus}}HTTP {{.HTTPStatus}}{{else}}未请求{{end}}</strong><span>{{.ElapsedMS}} ms</span></div></td><td><p class="evidence-text">{{.Evidence}}</p>{{if or .Usage .Metrics}}<details class="payload"><summary>查看 usage / metrics</summary><div class="payload-grid">{{if .Usage}}<pre>usage
{{.Usage}}</pre>{{end}}{{if .Metrics}}<pre>metrics
{{.Metrics}}</pre>{{end}}</div></details>{{end}}</td><td>{{if .ArtifactDir}}<a class="raw-link" href="{{.ArtifactDir}}/">原始记录 ↗</a>{{else}}—{{end}}</td></tr>{{end}}</tbody></table><div class="empty-state" id="empty-state">没有符合当前条件的用例。</div></div>
</section>
</main><script>
(function(){
  var activeFilter='all';
  var searchInput=document.getElementById('case-search');
  var rows=Array.prototype.slice.call(document.querySelectorAll('#case-list tr'));
  var filterButtons=Array.prototype.slice.call(document.querySelectorAll('[data-filter]'));
  var visibleCount=document.getElementById('visible-count');
  var emptyState=document.getElementById('empty-state');
  function applyFilters(){
    var query=searchInput.value.trim().toLowerCase();
    var count=0;
    rows.forEach(function(row){
      var matchesStatus=activeFilter==='all'||row.dataset.status===activeFilter;
      var matchesQuery=!query||(row.dataset.search||'').toLowerCase().indexOf(query)!==-1;
      var visible=matchesStatus&&matchesQuery;
      row.hidden=!visible;
      if(visible){count+=1;}
    });
    visibleCount.textContent=String(count);
    emptyState.style.display=count===0?'block':'none';
  }
  filterButtons.forEach(function(button){button.addEventListener('click',function(){
    activeFilter=button.dataset.filter;
    filterButtons.forEach(function(item){item.setAttribute('aria-pressed',String(item===button));});
    applyFilters();
  });});
  searchInput.addEventListener('input',applyFilters);
})();
</script></body></html>`
