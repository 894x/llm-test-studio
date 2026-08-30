"""Standalone HTML, PNG, and PDF exports for saved load-test reports."""

from collections import Counter
from dataclasses import dataclass
from html import escape
from io import BytesIO
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from llm_studio.metrics import status_label


REPORT_WIDTH = 1600
REPORT_HEIGHT = 3000


@dataclass(frozen=True)
class ReportArtifacts:
    html: bytes
    png: bytes
    pdf: bytes


def _number(value, digits=1):
    try:
        return f"{float(value):,.{digits}f}"
    except (TypeError, ValueError):
        return "0.0"


def _histogram_svg(values, title):
    clean_values = [float(value) for value in values if value is not None and value >= 0]
    width = 430
    height = 210
    if not clean_values:
        return (
            f'<svg viewBox="0 0 {width} {height}" role="img" '
            f'aria-label="{escape(title)} histogram"><text x="20" y="105" '
            'fill="#64748b">No successful samples</text></svg>'
        )

    low = min(clean_values)
    high = max(clean_values)
    bin_count = min(18, max(1, int(len(clean_values) ** 0.5)))
    counts = [0] * bin_count
    spread = high - low
    for value in clean_values:
        index = 0 if spread == 0 else min(int((value - low) / spread * bin_count), bin_count - 1)
        counts[index] += 1

    plot_left = 38
    plot_top = 38
    plot_width = 370
    plot_height = 125
    gap = 4
    bar_width = (plot_width - gap * (bin_count - 1)) / bin_count
    peak = max(counts)
    bars = []
    for index, count in enumerate(counts):
        bar_height = 0 if peak == 0 else count / peak * plot_height
        x = plot_left + index * (bar_width + gap)
        y = plot_top + plot_height - bar_height
        bars.append(
            f'<rect x="{x:.1f}" y="{y:.1f}" width="{bar_width:.1f}" '
            f'height="{bar_height:.1f}" rx="3" fill="#2563eb" />'
        )

    return f"""
    <svg viewBox="0 0 {width} {height}" role="img" aria-label="{escape(title)} histogram">
      <text x="18" y="24" class="chart-title">{escape(title)}</text>
      <line x1="{plot_left}" y1="{plot_top + plot_height}" x2="{plot_left + plot_width}" y2="{plot_top + plot_height}" stroke="#cbd5e1" />
      {''.join(bars)}
      <text x="{plot_left}" y="190" class="axis-label">{low:,.1f}</text>
      <text x="{plot_left + plot_width}" y="190" text-anchor="end" class="axis-label">{high:,.1f}</text>
    </svg>
    """


def build_timeline_data(results):
    series = {
        "ttft": [(result.started_at_s, result.ttft_ms) for result in results],
        "e2e": [(result.started_at_s, result.e2e_ms) for result in results],
        "tpot": [(result.started_at_s, result.tpot_ms) for result in results],
        "queue": [(result.started_at_s, result.queue_ms) for result in results],
    }
    events = []
    for result in results:
        events.append((result.started_at_s, 1))
        events.append((result.completed_at_s, -1))
    events.sort(key=lambda event: (event[0], event[1]))

    active = 0
    concurrency = []
    for event_time, change in events:
        active += change
        concurrency.append((event_time, active))
    series["concurrency"] = concurrency
    series["statuses"] = Counter(status_label(result) for result in results)
    return series


def _series_svg(points, title, mode="scatter"):
    width = 650
    height = 240
    if not points:
        return (
            f'<svg viewBox="0 0 {width} {height}" role="img" '
            f'aria-label="{escape(title)}"><text x="20" y="120" '
            'fill="#64748b">No samples</text></svg>'
        )

    left = 54
    right = 18
    top = 42
    bottom = 42
    plot_width = width - left - right
    plot_height = height - top - bottom
    x_values = [point[0] for point in points]
    y_values = [point[1] for point in points]
    x_low = min(x_values)
    x_high = max(x_values)
    y_low = min(y_values)
    y_high = max(y_values)
    x_spread = x_high - x_low
    y_spread = y_high - y_low

    scaled = []
    for x_value, y_value in points:
        x = left if x_spread == 0 else left + (x_value - x_low) / x_spread * plot_width
        y = top + plot_height / 2 if y_spread == 0 else top + plot_height - (y_value - y_low) / y_spread * plot_height
        scaled.append((x, y))

    marks = ""
    if mode == "line":
        path = " ".join(
            f"{'M' if index == 0 else 'L'} {x:.1f} {y:.1f}"
            for index, (x, y) in enumerate(scaled)
        )
        marks += f'<path d="{path}" fill="none" stroke="#2563eb" stroke-width="3" />'
    marks += "".join(
        f'<circle cx="{x:.1f}" cy="{y:.1f}" r="3.5" fill="#2563eb" />'
        for x, y in scaled
    )

    return f"""
    <svg viewBox="0 0 {width} {height}" role="img" aria-label="{escape(title)}">
      <text x="18" y="25" class="chart-title">{escape(title)}</text>
      <line x1="{left}" y1="{top}" x2="{left}" y2="{top + plot_height}" stroke="#cbd5e1" />
      <line x1="{left}" y1="{top + plot_height}" x2="{left + plot_width}" y2="{top + plot_height}" stroke="#cbd5e1" />
      {marks}
      <text x="{left}" y="225" class="axis-label">{x_low:,.2f}s</text>
      <text x="{left + plot_width}" y="225" text-anchor="end" class="axis-label">{x_high:,.2f}s</text>
      <text x="{left - 8}" y="{top + 10}" text-anchor="end" class="axis-label">{y_high:,.1f}</text>
      <text x="{left - 8}" y="{top + plot_height}" text-anchor="end" class="axis-label">{y_low:,.1f}</text>
    </svg>
    """


def _status_svg(status_counts):
    width = 650
    height = 240
    if not status_counts:
        return (
            f'<svg viewBox="0 0 {width} {height}" role="img" '
            'aria-label="Status codes"><text x="20" y="120" '
            'fill="#64748b">No samples</text></svg>'
        )

    items = sorted(status_counts.items())
    peak = max(status_counts.values())
    bar_height = min(30, 130 / max(len(items), 1))
    bars = []
    for index, (status, count) in enumerate(items):
        y = 50 + index * (bar_height + 10)
        bar_width = count / peak * 450
        color = "#16a34a" if status == "200" else "#dc2626"
        bars.append(
            f'<text x="18" y="{y + bar_height * .75:.1f}" class="axis-label">{escape(status)}</text>'
            f'<rect x="82" y="{y:.1f}" width="{bar_width:.1f}" height="{bar_height:.1f}" rx="5" fill="{color}" />'
            f'<text x="{92 + bar_width:.1f}" y="{y + bar_height * .75:.1f}" class="axis-label">{count}</text>'
        )
    return f"""
    <svg viewBox="0 0 {width} {height}" role="img" aria-label="Status codes">
      <text x="18" y="25" class="chart-title">Status codes</text>
      {''.join(bars)}
    </svg>
    """


def build_report_html(run_id, summary, metrics, results):
    timeline = build_timeline_data(results)
    status_counts = Counter(status_label(result) for result in results)
    status_items = "".join(
        f'<span class="status-pill">{escape(status)}: {count}</span>'
        for status, count in sorted(status_counts.items())
    )
    request_rows = "".join(
        "<tr>"
        f"<td>{result.id}</td>"
        f"<td>{escape(status_label(result))}</td>"
        f"<td>{_number(result.ttft_ms)}</td>"
        f"<td>{_number(result.tpot_ms, 2)}</td>"
        f"<td>{_number(result.e2e_ms)}</td>"
        f"<td>{_number(result.queue_ms)}</td>"
        f"<td>{result.prompt_tokens:,}</td>"
        f"<td>{result.completion_tokens:,}</td>"
        f"<td class=\"error\">{escape(result.error or '')}</td>"
        "</tr>"
        for result in results
    )

    health_cards = [
        ("Success rate", f"{metrics['success_rate']:.1f}%"),
        ("Completed", f"{metrics['total']}/{summary.get('requests') or metrics['total']}"),
        ("Failures", f"{metrics['failures']:,}"),
        ("Request rate", f"{metrics['request_qps']:.1f}/s"),
        ("Peak in-flight", f"{metrics['peak_in_flight']:,}"),
        ("Elapsed", f"{metrics['elapsed']:.1f}s"),
    ]
    throughput_cards = [
        ("RPM", _number(metrics["rpm"])),
        ("Input TPM", _number(metrics["input_tpm"])),
        ("Output TPM", _number(metrics["output_tpm"])),
        ("Total TPM", _number(metrics["total_tpm"])),
        ("Generation TPS", _number(metrics["generation_tps"])),
        ("KV cache hit", f"{metrics['cache_rate']:.1f}%"),
    ]

    def cards(items):
        return "".join(
            f'<div class="metric"><span>{escape(label)}</span><strong>{escape(value)}</strong></div>'
            for label, value in items
        )

    model = escape(str(summary.get("model") or "Unknown"))
    timestamp = escape(str(summary.get("ts") or "Unknown"))
    api_url = escape(str(summary.get("url") or "Unknown"))
    return f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Load Test Report #{run_id}</title>
  <style>
    :root {{ color-scheme: light; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #0f172a; background: #f1f5f9; }}
    * {{ box-sizing: border-box; }}
    body {{ margin: 0; padding: 40px; }}
    main {{ max-width: 1480px; margin: auto; background: white; padding: 48px; border-radius: 24px; box-shadow: 0 18px 60px rgba(15, 23, 42, .10); }}
    h1 {{ margin: 0 0 8px; font-size: 34px; }}
    h2 {{ margin: 36px 0 16px; font-size: 21px; }}
    .subtitle {{ color: #475569; margin-bottom: 8px; }}
    .url {{ color: #64748b; overflow-wrap: anywhere; }}
    .grid {{ display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 12px; }}
    .metric {{ border: 1px solid #e2e8f0; border-radius: 14px; padding: 16px; background: #f8fafc; }}
    .metric span {{ display: block; color: #64748b; font-size: 12px; text-transform: uppercase; letter-spacing: .05em; }}
    .metric strong {{ display: block; margin-top: 8px; font-size: 23px; }}
    .latency, table {{ width: 100%; border-collapse: collapse; }}
    th, td {{ padding: 11px 12px; border-bottom: 1px solid #e2e8f0; text-align: right; vertical-align: top; }}
    th {{ color: #475569; background: #f8fafc; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }}
    th:first-child, td:first-child, td.error {{ text-align: left; }}
    td.error {{ max-width: 420px; overflow-wrap: anywhere; }}
    .charts {{ display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px; }}
    .timeline-grid {{ display: grid; grid-template-columns: repeat(2, 1fr); gap: 14px; }}
    .chart {{ border: 1px solid #e2e8f0; border-radius: 14px; padding: 8px; }}
    .chart-title {{ fill: #0f172a; font-size: 15px; font-weight: 700; }}
    .axis-label {{ fill: #64748b; font-size: 11px; }}
    .status-pill {{ display: inline-block; margin: 0 8px 8px 0; border-radius: 999px; padding: 7px 12px; background: #eff6ff; color: #1d4ed8; font-weight: 650; }}
    .table-wrap {{ overflow-x: auto; }}
    footer {{ margin-top: 36px; color: #64748b; font-size: 12px; }}
    @media (max-width: 960px) {{ .grid {{ grid-template-columns: repeat(2, 1fr); }} .charts, .timeline-grid {{ grid-template-columns: 1fr; }} body {{ padding: 0; }} main {{ border-radius: 0; padding: 24px; }} }}
    @media print {{ body {{ padding: 0; background: white; }} main {{ max-width: none; padding: 20px; box-shadow: none; }} .table-wrap {{ overflow: visible; }} tr {{ break-inside: avoid; }} }}
  </style>
</head>
<body>
<main>
  <header>
    <h1>API Load Test Report <span>#{run_id}</span></h1>
    <div class="subtitle">{model} · {timestamp}</div>
    <div class="url">{api_url}</div>
  </header>
  <h2>Run health</h2>
  <section class="grid">{cards(health_cards)}</section>
  <h2>Throughput and tokens</h2>
  <section class="grid">{cards(throughput_cards)}</section>
  <h2>Latency percentiles</h2>
  <table class="latency">
    <thead><tr><th>Metric</th><th>Average</th><th>P50</th><th>P90</th><th>P95</th><th>P99</th></tr></thead>
    <tbody>
      <tr><td>TTFT (ms)</td><td>{_number(metrics['ttft_avg'])}</td><td>{_number(metrics['ttft_p50'])}</td><td>{_number(metrics['ttft_p90'])}</td><td>{_number(metrics['ttft_p95'])}</td><td>{_number(metrics['ttft_p99'])}</td></tr>
      <tr><td>TPOT (ms/token)</td><td>{_number(metrics['tpot_avg'], 2)}</td><td>{_number(metrics['tpot_p50'], 2)}</td><td>{_number(metrics['tpot_p90'], 2)}</td><td>{_number(metrics['tpot_p95'], 2)}</td><td>{_number(metrics['tpot_p99'], 2)}</td></tr>
      <tr><td>E2E (ms)</td><td>{_number(metrics['e2e_avg'])}</td><td>{_number(metrics['e2e_p50'])}</td><td>{_number(metrics['e2e_p90'])}</td><td>{_number(metrics['e2e_p95'])}</td><td>{_number(metrics['e2e_p99'])}</td></tr>
    </tbody>
  </table>
  <h2>Latency distributions</h2>
  <section class="charts">
    <div class="chart">{_histogram_svg([result.ttft_ms for result in results if result.status == 200 and result.ttft_ms > 0], 'TTFT (ms)')}</div>
    <div class="chart">{_histogram_svg([result.tpot_ms for result in results if result.status == 200 and result.tpot_ms > 0], 'TPOT (ms/token)')}</div>
    <div class="chart">{_histogram_svg([result.e2e_ms for result in results if result.status == 200], 'E2E (ms)')}</div>
  </section>
  <h2>Timelines</h2>
  <section class="timeline-grid">
    <div class="chart">{_series_svg(timeline['ttft'], 'TTFT (ms)')}</div>
    <div class="chart">{_series_svg(timeline['e2e'], 'E2E (ms)')}</div>
    <div class="chart">{_series_svg(timeline['tpot'], 'TPOT (ms/token)')}</div>
    <div class="chart">{_series_svg(timeline['queue'], 'Client queue (ms)')}</div>
    <div class="chart">{_series_svg(timeline['concurrency'], 'In-flight requests', mode='line')}</div>
    <div class="chart">{_status_svg(timeline['statuses'])}</div>
  </section>
  <h2>Status breakdown</h2>
  <section>{status_items or '<span class="status-pill">No results</span>'}</section>
  <h2>Request results</h2>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Request</th><th>Status</th><th>TTFT ms</th><th>TPOT ms</th><th>E2E ms</th><th>Queue ms</th><th>Input</th><th>Output</th><th>Error</th></tr></thead>
      <tbody>{request_rows}</tbody>
    </table>
  </div>
  <footer>Generated from the saved load-test run. The JSON export contains full recorded request and response payloads.</footer>
</main>
</body>
</html>"""


def _report_fonts():
    candidates = [
        Path("/System/Library/Fonts/Supplemental/Arial.ttf"),
        Path("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"),
    ]
    font_path = next((path for path in candidates if path.exists()), None)
    if font_path is None:
        return {
            "title": ImageFont.load_default(size=44),
            "heading": ImageFont.load_default(size=28),
            "metric": ImageFont.load_default(size=34),
            "body": ImageFont.load_default(size=22),
            "small": ImageFont.load_default(size=18),
        }
    return {
        "title": ImageFont.truetype(str(font_path), 44),
        "heading": ImageFont.truetype(str(font_path), 28),
        "metric": ImageFont.truetype(str(font_path), 34),
        "body": ImageFont.truetype(str(font_path), 22),
        "small": ImageFont.truetype(str(font_path), 18),
    }


def _fit_text(draw, text, max_width, font):
    value = str(text)
    if draw.textlength(value, font=font) <= max_width:
        return value
    while value and draw.textlength(f"{value}…", font=font) > max_width:
        value = value[:-1]
    return f"{value}…"


def _draw_metric_row(draw, y, items, fonts):
    left = 80
    gap = 14
    card_width = (REPORT_WIDTH - 160 - gap * (len(items) - 1)) / len(items)
    for index, (label, value) in enumerate(items):
        x = left + index * (card_width + gap)
        draw.rounded_rectangle(
            (x, y, x + card_width, y + 126),
            radius=16,
            fill="#f8fafc",
            outline="#dbe4ee",
            width=2,
        )
        draw.text((x + 18, y + 17), label.upper(), fill="#64748b", font=fonts["small"])
        draw.text((x + 18, y + 56), value, fill="#0f172a", font=fonts["metric"])


def _draw_histogram(draw, box, values, title, fonts):
    x1, y1, x2, y2 = box
    draw.rounded_rectangle(box, radius=16, fill="#ffffff", outline="#dbe4ee", width=2)
    draw.text((x1 + 18, y1 + 14), title, fill="#0f172a", font=fonts["body"])
    values = [float(value) for value in values if value is not None and value >= 0]
    if not values:
        draw.text((x1 + 18, y1 + 88), "No successful samples", fill="#64748b", font=fonts["small"])
        return
    low = min(values)
    high = max(values)
    spread = high - low
    bins = min(16, max(1, int(len(values) ** 0.5)))
    counts = [0] * bins
    for value in values:
        index = 0 if spread == 0 else min(int((value - low) / spread * bins), bins - 1)
        counts[index] += 1
    plot_left = x1 + 24
    plot_right = x2 - 24
    plot_top = y1 + 62
    plot_bottom = y2 - 44
    gap = 5
    bar_width = (plot_right - plot_left - gap * (bins - 1)) / bins
    peak = max(counts)
    for index, count in enumerate(counts):
        height = count / peak * (plot_bottom - plot_top)
        bar_x = plot_left + index * (bar_width + gap)
        draw.rounded_rectangle(
            (bar_x, plot_bottom - height, bar_x + bar_width, plot_bottom),
            radius=3,
            fill="#2563eb",
        )
    draw.text((plot_left, plot_bottom + 10), f"{low:,.1f}", fill="#64748b", font=fonts["small"])
    high_label = f"{high:,.1f}"
    high_width = draw.textlength(high_label, font=fonts["small"])
    draw.text((plot_right - high_width, plot_bottom + 10), high_label, fill="#64748b", font=fonts["small"])


def _draw_series_chart(draw, box, points, title, fonts, mode="scatter"):
    x1, y1, x2, y2 = box
    draw.rounded_rectangle(box, radius=16, fill="#ffffff", outline="#dbe4ee", width=2)
    draw.text((x1 + 18, y1 + 14), title, fill="#0f172a", font=fonts["body"])
    if not points:
        draw.text((x1 + 18, y1 + 100), "No samples", fill="#64748b", font=fonts["small"])
        return

    plot_left = x1 + 58
    plot_right = x2 - 22
    plot_top = y1 + 58
    plot_bottom = y2 - 42
    x_values = [point[0] for point in points]
    y_values = [point[1] for point in points]
    x_low = min(x_values)
    x_high = max(x_values)
    y_low = min(y_values)
    y_high = max(y_values)
    x_spread = x_high - x_low
    y_spread = y_high - y_low
    scaled = []
    for x_value, y_value in points:
        x = plot_left if x_spread == 0 else plot_left + (x_value - x_low) / x_spread * (plot_right - plot_left)
        y = (plot_top + plot_bottom) / 2 if y_spread == 0 else plot_bottom - (y_value - y_low) / y_spread * (plot_bottom - plot_top)
        scaled.append((x, y))

    draw.line((plot_left, plot_top, plot_left, plot_bottom), fill="#cbd5e1", width=2)
    draw.line((plot_left, plot_bottom, plot_right, plot_bottom), fill="#cbd5e1", width=2)
    if mode == "line" and len(scaled) > 1:
        draw.line(scaled, fill="#2563eb", width=3)
    for x, y in scaled:
        draw.ellipse((x - 4, y - 4, x + 4, y + 4), fill="#2563eb")

    draw.text((plot_left, plot_bottom + 10), f"{x_low:,.2f}s", fill="#64748b", font=fonts["small"])
    high_x_label = f"{x_high:,.2f}s"
    high_x_width = draw.textlength(high_x_label, font=fonts["small"])
    draw.text((plot_right - high_x_width, plot_bottom + 10), high_x_label, fill="#64748b", font=fonts["small"])
    high_y_label = f"{y_high:,.1f}"
    high_y_width = draw.textlength(high_y_label, font=fonts["small"])
    draw.text((plot_left - high_y_width - 8, plot_top - 8), high_y_label, fill="#64748b", font=fonts["small"])
    low_y_label = f"{y_low:,.1f}"
    low_y_width = draw.textlength(low_y_label, font=fonts["small"])
    draw.text((plot_left - low_y_width - 8, plot_bottom - 10), low_y_label, fill="#64748b", font=fonts["small"])


def _draw_status_chart(draw, box, status_counts, fonts):
    x1, y1, x2, y2 = box
    draw.rounded_rectangle(box, radius=16, fill="#ffffff", outline="#dbe4ee", width=2)
    draw.text((x1 + 18, y1 + 14), "Status codes", fill="#0f172a", font=fonts["body"])
    if not status_counts:
        draw.text((x1 + 18, y1 + 100), "No samples", fill="#64748b", font=fonts["small"])
        return

    items = sorted(status_counts.items())
    peak = max(status_counts.values())
    bar_height = min(32, 130 / max(len(items), 1))
    for index, (status, count) in enumerate(items):
        y = y1 + 60 + index * (bar_height + 12)
        color = "#16a34a" if status == "200" else "#dc2626"
        draw.text((x1 + 20, y + 4), status, fill="#475569", font=fonts["small"])
        bar_left = x1 + 110
        bar_right = bar_left + count / peak * (x2 - bar_left - 80)
        draw.rounded_rectangle((bar_left, y, bar_right, y + bar_height), radius=5, fill=color)
        draw.text((bar_right + 10, y + 4), str(count), fill="#475569", font=fonts["small"])


def build_report_png(run_id, summary, metrics, results):
    image = Image.new("RGB", (REPORT_WIDTH, REPORT_HEIGHT), "#f1f5f9")
    draw = ImageDraw.Draw(image)
    fonts = _report_fonts()
    draw.rounded_rectangle((38, 38, REPORT_WIDTH - 38, REPORT_HEIGHT - 38), radius=28, fill="white")

    draw.text((80, 76), f"API Load Test Report #{run_id}", fill="#0f172a", font=fonts["title"])
    subtitle = f"{summary.get('model') or 'Unknown'}  ·  {summary.get('ts') or 'Unknown'}"
    draw.text((80, 138), _fit_text(draw, subtitle, 1440, fonts["body"]), fill="#475569", font=fonts["body"])
    draw.text((80, 180), _fit_text(draw, summary.get("url") or "Unknown", 1440, fonts["small"]), fill="#64748b", font=fonts["small"])

    health = [
        ("Success", f"{metrics['success_rate']:.1f}%"),
        ("Completed", f"{metrics['total']:,}"),
        ("Failures", f"{metrics['failures']:,}"),
        ("Request rate", f"{metrics['request_qps']:.1f}/s"),
        ("Peak in-flight", f"{metrics['peak_in_flight']:,}"),
        ("Elapsed", f"{metrics['elapsed']:.1f}s"),
    ]
    draw.text((80, 248), "Run health", fill="#0f172a", font=fonts["heading"])
    _draw_metric_row(draw, 296, health, fonts)

    throughput = [
        ("RPM", _number(metrics["rpm"])),
        ("Input TPM", _number(metrics["input_tpm"])),
        ("Output TPM", _number(metrics["output_tpm"])),
        ("Total TPM", _number(metrics["total_tpm"])),
        ("Generation TPS", _number(metrics["generation_tps"])),
        ("KV cache hit", f"{metrics['cache_rate']:.1f}%"),
    ]
    draw.text((80, 468), "Throughput and tokens", fill="#0f172a", font=fonts["heading"])
    _draw_metric_row(draw, 516, throughput, fonts)

    draw.text((80, 688), "Latency percentiles", fill="#0f172a", font=fonts["heading"])
    columns = ["Metric", "Average", "P50", "P90", "P95", "P99"]
    rows = [
        ("TTFT (ms)", metrics["ttft_avg"], metrics["ttft_p50"], metrics["ttft_p90"], metrics["ttft_p95"], metrics["ttft_p99"]),
        ("TPOT (ms/token)", metrics["tpot_avg"], metrics["tpot_p50"], metrics["tpot_p90"], metrics["tpot_p95"], metrics["tpot_p99"]),
        ("E2E (ms)", metrics["e2e_avg"], metrics["e2e_p50"], metrics["e2e_p90"], metrics["e2e_p95"], metrics["e2e_p99"]),
    ]
    table_left = 80
    table_top = 742
    col_widths = [330, 214, 214, 214, 214, 214]
    x = table_left
    for label, width in zip(columns, col_widths):
        draw.rectangle((x, table_top, x + width, table_top + 48), fill="#f8fafc")
        draw.text((x + 12, table_top + 13), label, fill="#475569", font=fonts["small"])
        x += width
    for row_index, row in enumerate(rows):
        row_y = table_top + 48 + row_index * 52
        x = table_left
        for col_index, (value, width) in enumerate(zip(row, col_widths)):
            text = str(value) if col_index == 0 else _number(value, 2 if row_index == 1 else 1)
            draw.line((x, row_y + 51, x + width, row_y + 51), fill="#e2e8f0", width=1)
            draw.text((x + 12, row_y + 14), text, fill="#0f172a", font=fonts["small"])
            x += width

    draw.text((80, 984), "Latency distributions", fill="#0f172a", font=fonts["heading"])
    chart_gap = 18
    chart_width = (REPORT_WIDTH - 160 - chart_gap * 2) / 3
    chart_values = [
        ([result.ttft_ms for result in results if result.status == 200 and result.ttft_ms > 0], "TTFT (ms)"),
        ([result.tpot_ms for result in results if result.status == 200 and result.tpot_ms > 0], "TPOT (ms/token)"),
        ([result.e2e_ms for result in results if result.status == 200], "E2E (ms)"),
    ]
    for index, (values, title) in enumerate(chart_values):
        chart_left = 80 + index * (chart_width + chart_gap)
        _draw_histogram(draw, (chart_left, 1034, chart_left + chart_width, 1340), values, title, fonts)

    timeline = build_timeline_data(results)
    draw.text((80, 1390), "Timelines", fill="#0f172a", font=fonts["heading"])
    timeline_gap = 18
    timeline_width = (REPORT_WIDTH - 160 - timeline_gap) / 2
    timeline_charts = [
        (timeline["ttft"], "TTFT (ms)", "scatter"),
        (timeline["e2e"], "E2E (ms)", "scatter"),
        (timeline["tpot"], "TPOT (ms/token)", "scatter"),
        (timeline["queue"], "Client queue (ms)", "scatter"),
        (timeline["concurrency"], "In-flight requests", "line"),
    ]
    for index, (points, title, mode) in enumerate(timeline_charts):
        row = index // 2
        column = index % 2
        left = 80 + column * (timeline_width + timeline_gap)
        top = 1440 + row * 270
        _draw_series_chart(
            draw,
            (left, top, left + timeline_width, top + 250),
            points,
            title,
            fonts,
            mode=mode,
        )
    status_left = 80 + timeline_width + timeline_gap
    _draw_status_chart(
        draw,
        (status_left, 1980, status_left + timeline_width, 2230),
        timeline["statuses"],
        fonts,
    )

    draw.text((80, 2280), "Status breakdown", fill="#0f172a", font=fonts["heading"])
    status_counts = Counter(status_label(result) for result in results)
    status_text = "  ·  ".join(f"{status}: {count:,}" for status, count in sorted(status_counts.items())) or "No results"
    draw.text((80, 2330), _fit_text(draw, status_text, 1440, fonts["body"]), fill="#1d4ed8", font=fonts["body"])

    failures = [result for result in results if result.status != 200]
    draw.text((80, 2404), "Failure samples", fill="#0f172a", font=fonts["heading"])
    if not failures:
        draw.text((80, 2454), "No failures recorded.", fill="#15803d", font=fonts["body"])
    else:
        for index, result in enumerate(failures[:5]):
            failure = f"#{result.id}  {status_label(result)}  {result.error or 'Unknown error'}"
            draw.text((80, 2454 + index * 49), _fit_text(draw, failure, 1440, fonts["small"]), fill="#b91c1c", font=fonts["small"])
        if len(failures) > 5:
            draw.text((80, 2454 + 5 * 49), f"+ {len(failures) - 5:,} more failures in the HTML and JSON exports", fill="#64748b", font=fonts["small"])

    draw.text((80, 2922), "Full per-request data is available in the HTML and JSON exports.", fill="#64748b", font=fonts["small"])
    buffer = BytesIO()
    image.save(buffer, format="PNG", optimize=True)
    return buffer.getvalue()


def build_report_pdf(png_data):
    report = Image.open(BytesIO(png_data)).convert("RGB")
    max_width = 1140
    max_height = 1654
    scale = max_width / report.width
    source_page_height = max(1, int(max_height / scale))
    pages = []
    for top in range(0, report.height, source_page_height):
        section = report.crop((0, top, report.width, min(top + source_page_height, report.height)))
        section = section.resize(
            (round(section.width * scale), round(section.height * scale)),
            Image.Resampling.LANCZOS,
        )
        page = Image.new("RGB", (1240, 1754), "white")
        page.paste(section, ((page.width - section.width) // 2, 50))
        pages.append(page)
    buffer = BytesIO()
    pages[0].save(
        buffer,
        format="PDF",
        resolution=150.0,
        save_all=True,
        append_images=pages[1:],
    )
    return buffer.getvalue()


def build_report_artifacts(run_id, summary, metrics, results):
    html = build_report_html(run_id, summary, metrics, results).encode("utf-8")
    png = build_report_png(run_id, summary, metrics, results)
    pdf = build_report_pdf(png)
    return ReportArtifacts(html=html, png=png, pdf=pdf)
