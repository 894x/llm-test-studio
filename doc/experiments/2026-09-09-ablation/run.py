"""Offline, single-factor ablations using Go build overlays; never edit runtime source."""
from pathlib import Path
import hashlib
import json
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
REPORT = "internal/application/reporting/generator.go"
STREAM = "engine/apiaudit/openai_evaluators.go"
STREAM_TEST = "engine/apiaudit/response_limit_test.go"
REPORT_TEST = "internal/application/reporting/generator_test.go"

STREAM_FIXTURES = r'''
func TestAblationStreamFixtures(t *testing.T) {
    good := streamObservation{StatusCode: 200, Frames: 3, Content: "OK", FinishCount: 1, Done: true}
    fixtures := []struct {
        name string
        change func(*streamObservation)
        want bool
    }{
        {name: "healthy", change: func(o *streamObservation) {}, want: true},
        {name: "http_500", change: func(o *streamObservation) { o.StatusCode = 500 }},
        {name: "missing_done", change: func(o *streamObservation) { o.Done = false }},
        {name: "missing_finish", change: func(o *streamObservation) { o.FinishCount = 0 }},
        {name: "duplicate_finish", change: func(o *streamObservation) { o.FinishCount = 2 }},
        {name: "empty_content", change: func(o *streamObservation) { o.Content = "" }},
        {name: "single_frame", change: func(o *streamObservation) { o.Frames = 1 }},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            observation := good
            fixture.change(&observation)
            result := CaseResult{}
            got := requireValidStream(&result, observation)
            t.Logf("accepted=%t expected=%t", got, fixture.want)
            if got != fixture.want {
                t.Errorf("fixture %s: accepted=%t, want %t", fixture.name, got, fixture.want)
            }
        })
    }
}
'''

METRIC_FIXTURE = r'''
func TestAblationMetricReadout(t *testing.T) {
    success := domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
    results := []domain.Result{
        {Success: success, Metrics: map[string]float64{"e2e_ms": 10}},
        {Success: success, Metrics: map[string]float64{"e2e_ms": 20}},
        {Metrics: map[string]float64{"e2e_ms": 1000}},
    }
    metrics := aggregateMetrics(results)
    t.Logf("mean=%g p95=%g samples=%d success_rate=%g",
        metrics["e2e_ms"].Value, metrics["e2e_p95_ms"].Value,
        metrics["e2e_ms"].Samples, metrics["success_rate"].Value)
}
'''

def replace_once(source, old, new):
    if source.count(old) != 1:
        raise RuntimeError("Source changed: expected exactly one mutation anchor")
    return source.replace(old, new, 1)

def main():
    paths = [REPORT, STREAM, STREAM_TEST, REPORT_TEST]
    raw = {p: (ROOT / p).read_bytes() for p in paths}
    source = {p: b.decode("utf-8").replace("\r\n", "\n") for p, b in raw.items()}
    summary = {
        "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "go": subprocess.check_output(["go", "version"], cwd=ROOT, text=True).strip(),
        "source_sha256": {p: hashlib.sha256(b).hexdigest() for p, b in raw.items()},
        "runs": [],
    }
    test_pattern = "^(TestAblationStreamFixtures|TestAblationMetricReadout|TestGeneratorBuildsAndPersistsACompletePerformanceReport|TestGeneratorFailsConclusionWhenObservedSLAIsBreached|TestFineStreamingReportMetricsUseOnlySuccessfulCohorts)$"
    for variant in ["baseline", "without_sse_contract", "without_sla_gate", "without_success_cohort"]:
        replacements = {
            STREAM_TEST: source[STREAM_TEST] + STREAM_FIXTURES,
            REPORT_TEST: source[REPORT_TEST] + METRIC_FIXTURE,
        }
        if variant == "without_sse_contract":
            old = source[STREAM].split("func requireValidStream(", 1)[1].split("\nfunc validBoundaryResponse", 1)[0]
            replacement = old[:old.index("\n\tif observation.Frames")] + "\n\treturn true\n}\n"
            replacements[STREAM] = replace_once(source[STREAM], old, replacement)
        if variant == "without_sla_gate":
            replacements[REPORT] = replace_once(source[REPORT], "return observed, issues", "return observed, []string{}")
        if variant == "without_success_cohort":
            replacements[REPORT] = replace_once(source[REPORT],
                "values = successfulMetricSamples(results, metric.name)",
                "values = metricSamples(results, metric.name)")
        with tempfile.TemporaryDirectory(prefix="llm-ablation-") as directory:
            temp = Path(directory)
            overlay = {"Replace": {}}
            for i, (path, contents) in enumerate(replacements.items()):
                target = temp / f"source-{i}.go"
                target.write_text(contents, encoding="utf-8")
                overlay["Replace"][str(ROOT / path)] = str(target)
            overlay_path = temp / "overlay.json"
            overlay_path.write_text(json.dumps(overlay), encoding="utf-8")
            command = ["go", "test", "-overlay", str(overlay_path), "-count=1", "-timeout=90s", "-json",
                       "./engine/apiaudit", "./internal/application/reporting", "-run", test_pattern]
            process = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, encoding="utf-8", timeout=180)
            (OUT / f"{variant}.jsonl").write_text(process.stdout, encoding="utf-8")
            (OUT / f"{variant}.stderr.txt").write_text(process.stderr, encoding="utf-8")
            events = [json.loads(line) for line in process.stdout.splitlines() if line.startswith("{")]
            passed = [e["Test"] for e in events if e.get("Action") == "pass" and "Test" in e]
            failed = [e["Test"] for e in events if e.get("Action") == "fail" and "Test" in e]
            run = {"variant": variant, "exit_code": process.returncode, "passed": passed, "failed": failed}
            summary["runs"].append(run)
            print(json.dumps(run), flush=True)
            if variant == "baseline" and (process.returncode != 0 or len(passed) != 12):
                raise RuntimeError("Baseline did not pass all 5 top-level tests and 7 subtests")
            if variant != "baseline" and (process.returncode != 1 or not failed):
                raise RuntimeError("Ablation did not produce an interpretable test assertion failure")
    summary["source_unchanged"] = all((ROOT / p).read_bytes() == b for p, b in raw.items())
    (OUT / "summary.json").write_text(json.dumps(summary, indent=2), encoding="utf-8")
    if not summary["source_unchanged"]:
        raise RuntimeError("Source changed during experiment; rerun required")

if __name__ == "__main__":
    main()
