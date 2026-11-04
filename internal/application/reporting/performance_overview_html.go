package reporting

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
)

type htmlPerformanceOverview struct {
	PerformanceOverview
	Title, Samples, CountLabel, Observed, Unevaluated, Summary, Scope string
	Metrics                                                           []htmlPerformanceOverviewMetric
}

type htmlPerformanceOverviewMetric struct {
	Name, Value, Status, StatusLabel, Target string
}

func renderPerformanceOverviewHTML(report *quicktest.PerformanceReport, locale string) (template.HTML, error) {
	overview := performanceOverview(report)
	names := []string{"请求成功率", "SLO 达标率", "TTFT P95（含推理）", "TPOT P95", "E2E P95", "成功吞吐"}
	statuses := map[string]string{
		"passed": "达标", "failed": "未达标", "observed": "仅观测",
		"not_evaluated": "未评估", "unavailable": "无有效观测",
	}
	summaries := map[string]string{
		"passed": "已评估指标达标", "failed": "部分指标未达标",
		"observed": "仅观测", "not_evaluated": "部分指标未评估",
	}
	view := htmlPerformanceOverview{
		PerformanceOverview: overview,
		Title:               "关键性能指标", Samples: fmt.Sprintf("稳态样本 · %d 次请求", overview.SampleCount),
		CountLabel: "项达标", Observed: fmt.Sprintf("另有 %d 项仅观测", overview.ObservedCount),
		Unevaluated: fmt.Sprintf("%d 项未评估", overview.UnevaluatedCount),
		Scope:       "仅统计稳态阶段。成功率沿用全部请求成功标准；P95 对照已保存的 SLO 单请求上限，仅作参考，不替代原报告的逐请求 SLO 与容量结论。",
		Metrics:     make([]htmlPerformanceOverviewMetric, 0, len(overview.Metrics)),
	}
	targetLabel, referenceLabel, noTarget := "目标", "参考上限", "未设置目标"
	if locale == "en-US" {
		names = []string{"Request success rate", "SLO compliance", "TTFT P95 (includes reasoning)", "TPOT P95", "E2E P95", "Successful throughput"}
		statuses = map[string]string{
			"passed": "Met", "failed": "Not met", "observed": "Observation only",
			"not_evaluated": "Not evaluated", "unavailable": "No valid observations",
		}
		summaries = map[string]string{
			"passed": "Evaluated metrics met", "failed": "Some metrics missed their targets",
			"observed": "Observation only", "not_evaluated": "Some metrics not evaluated",
		}
		view.Title, view.Samples = "Key performance metrics", fmt.Sprintf("Steady-state sample · %d requests", overview.SampleCount)
		view.CountLabel = "metrics met"
		view.Observed = fmt.Sprintf("%d observation-only metrics", overview.ObservedCount)
		view.Unevaluated = fmt.Sprintf("%d unevaluated metrics", overview.UnevaluatedCount)
		view.Scope = "Steady-state observations only. Success rate retains the all-requests-successful criterion. " +
			"P95 comparisons use the saved per-request SLO limits as references; they do not replace " +
			"the archived per-request SLO or capacity conclusions."
		targetLabel, referenceLabel, noTarget = "Target", "Reference limit", "No target configured"
	}
	view.Summary = summaries[overview.Status]
	for index, metric := range overview.Metrics {
		row := htmlPerformanceOverviewMetric{
			Name: names[index], Value: "—", Status: metric.Status, StatusLabel: statuses[metric.Status], Target: noTarget,
		}
		if metric.Value != nil {
			row.Value = overviewMetricText(*metric.Value, metric.Unit)
		}
		if metric.Target != nil {
			label, comparison := targetLabel, "≥"
			if metric.Reference {
				label = referenceLabel
			}
			if metric.Comparison == "at_most" {
				comparison = "≤"
			}
			row.Target = label + " " + comparison + " " + overviewMetricText(*metric.Target, metric.Unit)
		}
		view.Metrics = append(view.Metrics, row)
	}
	var output bytes.Buffer
	if err := performanceOverviewHTMLTemplate.Execute(&output, view); err != nil {
		return "", err
	}
	// All text has already passed through html/template's contextual escaping.
	return template.HTML(output.String()), nil
}

func overviewMetricText(value float64, unit string) string {
	if unit == "percent" {
		return strconv.FormatFloat(value, 'f', 1, 64) + "%"
	}
	if unit == "ms" && value >= 1000 {
		return strconv.FormatFloat(value/1000, 'f', 2, 64) + " s"
	}
	return strconv.FormatFloat(value, 'f', 2, 64) + " " + unit
}

var performanceOverviewHTMLTemplate = template.Must(template.New("performance-overview").Parse(`
<section class="performance-overview" aria-label="{{.Title}}">
<style>
.performance-overview{container-type:inline-size;margin:24px 0;color-scheme:light;--po-text:light-dark(#172033,#e2e2e2);--po-muted:light-dark(#657189,#a5a5a5);--po-pass:light-dark(#15803d,#4ade80);--po-fail:light-dark(#c24152,#ff9aa8);color:var(--po-text)}
.performance-overview .po-heading,.performance-overview .po-summary{display:flex;flex-wrap:wrap;align-items:center;gap:12px;margin:12px 0}.performance-overview .po-heading{justify-content:space-between}.performance-overview h2{font-size:16px;margin:0}.performance-overview .po-meta{font-size:12px;color:var(--po-muted)}
.performance-overview .po-metrics{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:20px;padding:16px;border-radius:8px;background:light-dark(#f1f3f6,#272727)}
.performance-overview .po-metric{min-width:0;overflow-wrap:anywhere}.performance-overview .po-value{font-size:26px;font-weight:600;margin:12px 0}.performance-overview .po-status{font-size:12px;margin:8px 0}.performance-overview [data-status=passed] .po-status{color:var(--po-pass)}.performance-overview [data-status=failed] .po-status,.performance-overview [data-status=failed] .po-value,.performance-overview .po-summary[data-status=failed]>strong{color:var(--po-fail)}
@container(max-width:1000px){.performance-overview .po-metrics{grid-template-columns:repeat(3,minmax(0,1fr))}}@container(max-width:560px){.performance-overview .po-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
<div class="po-heading"><h2>{{.Title}}</h2><span class="po-meta">{{.Samples}}</span></div>
<div class="po-summary" data-status="{{.Status}}"><strong>{{.Summary}}</strong>{{if .EvaluatedCount}}<span>{{.PassedCount}} / {{.EvaluatedCount}} {{.CountLabel}}</span>{{end}}{{if .ObservedCount}}<span class="po-meta">{{.Observed}}</span>{{end}}{{if .UnevaluatedCount}}<span class="po-meta">{{.Unevaluated}}</span>{{end}}</div>
<div class="po-metrics">{{range .Metrics}}<div class="po-metric" data-status="{{.Status}}"><div class="po-meta">{{.Name}}</div><div class="po-value">{{.Value}}</div><div class="po-status">{{if eq .Status "passed"}}✓ {{else if eq .Status "failed"}}× {{else}}— {{end}}{{.StatusLabel}}</div><div class="po-meta">{{.Target}}</div></div>{{end}}</div>
<p class="po-meta">{{.Scope}}</p></section>`))
