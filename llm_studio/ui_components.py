"""Reusable Streamlit components shared by run and report pages."""

from __future__ import annotations

import base64
from collections import Counter

import pandas as pd
import plotly.graph_objects as go
from plotly.subplots import make_subplots
import streamlit as st

from loadtest import LoadTestProgress

from .metrics import compute_metrics, status_label
from .ui_theme import section_title


ACCENT = "#ff5a52"
BLUE = "#5596ff"
GRID = "#26364a"
MUTED = "#93a4ba"


def format_rate(value: float) -> str:
    if value >= 1_000_000:
        return f"{value / 1_000_000:.2f}M"
    if value >= 1_000:
        return f"{value / 1_000:.1f}K"
    return f"{value:.1f}"


def render_copy_png_button(png_data: bytes, run_id: int) -> None:
    encoded = base64.b64encode(png_data).decode("ascii")
    st.iframe(
        f"""
        <style>
          html, body {{ width: 100%; height: 66px; margin: 0; overflow: hidden; }}
          body {{ font-family: Inter, ui-sans-serif, system-ui, sans-serif; }}
          * {{ box-sizing: border-box; }}
          button {{
            width: 100%; height: 40px; border: 1px solid #33465e;
            border-radius: 8px; background: #0d1828; color: #f4f7fb;
            font-size: 13px; font-weight: 650; cursor: pointer;
          }}
          button:hover {{ border-color: {ACCENT}; color: {ACCENT}; }}
          button:disabled {{ cursor: wait; opacity: .65; }}
          #message {{
            height: 18px; margin-top: 4px; overflow: hidden; color: {MUTED};
            font-size: 11px; line-height: 18px; text-overflow: ellipsis;
            white-space: nowrap;
          }}
        </style>
        <button id="copy-png" type="button">Copy as PNG</button>
        <div id="message" role="status" aria-live="polite"></div>
        <script>
          const button = document.getElementById('copy-png');
          const message = document.getElementById('message');
          const dataUrl = 'data:image/png;base64,{encoded}';
          button.addEventListener('click', async () => {{
            button.disabled = true;
            message.textContent = 'Copying…';
            try {{
              const blob = await (await fetch(dataUrl)).blob();
              await navigator.clipboard.write([new ClipboardItem({{ 'image/png': blob }})]);
              message.textContent = 'PNG copied to the clipboard.';
            }} catch (error) {{
              const link = document.createElement('a');
              link.href = dataUrl;
              link.download = 'loadtest-run-{run_id}.png';
              link.click();
              message.textContent = 'Clipboard unavailable; PNG downloaded.';
            }} finally {{
              button.disabled = false;
            }}
          }});
        </script>
        """,
        height=66,
        width="stretch",
    )


def _style_chart(fig: go.Figure, height: int) -> None:
    fig.update_layout(
        height=height,
        paper_bgcolor="rgba(0,0,0,0)",
        plot_bgcolor="rgba(0,0,0,0)",
        font={"color": "#c7d2e1", "size": 12},
        margin={"l": 30, "r": 20, "t": 48, "b": 30},
        hoverlabel={"bgcolor": "#111e30", "bordercolor": GRID},
    )
    fig.update_xaxes(gridcolor=GRID, zerolinecolor=GRID)
    fig.update_yaxes(gridcolor=GRID, zerolinecolor=GRID)


def render_results(progress: LoadTestProgress, key_prefix: str) -> None:
    metrics = compute_metrics(progress)
    pct_done = progress.done / max(progress.total, 1) * 100
    st.progress(
        pct_done / 100,
        text=f"Progress: {progress.done}/{progress.total} ({pct_done:.0f}%)",
    )

    section_title("Run health")
    health_columns = st.columns(6)
    values = [
        ("Success rate", f"{metrics['success_rate']:.1f}%"),
        ("Completed", f"{metrics['total']}/{progress.total}"),
        ("Failures", str(metrics["failures"])),
        ("Request rate", f"{metrics['request_qps']:.1f}/s"),
        ("Peak in-flight", str(metrics["peak_in_flight"])),
        ("Elapsed", f"{metrics['elapsed']:.1f}s"),
    ]
    for column, (label, value) in zip(health_columns, values):
        column.metric(label, value)

    if metrics["failures"]:
        status_counts = Counter(
            status_label(result)
            for result in progress.results
            if result.status != 200
        )
        summary = ", ".join(
            f"{status}: {count}" for status, count in status_counts.items()
        )
        st.error(f"{metrics['failures']} requests failed — {summary}")

    section_title("Throughput and tokens")
    throughput_columns = st.columns(6)
    throughput_values = [
        ("RPM", f"{metrics['rpm']:.1f}"),
        ("Input TPM", format_rate(metrics["input_tpm"])),
        ("Output TPM", format_rate(metrics["output_tpm"])),
        ("Total TPM", format_rate(metrics["total_tpm"])),
        ("Generation TPS", format_rate(metrics["generation_tps"])),
        ("KV cache hit", f"{metrics['cache_rate']:.1f}%"),
    ]
    for column, (label, value) in zip(throughput_columns, throughput_values):
        column.metric(label, value)

    section_title("Latency distribution")
    latency_frame = pd.DataFrame(
        {
            "Metric": ["TTFT (ms)", "TPOT (ms/token)", "E2E (ms)", "Client queue (ms)"],
            "Average": [metrics["ttft_avg"], metrics["tpot_avg"], metrics["e2e_avg"], metrics["queue_avg"]],
            "P50": [metrics["ttft_p50"], metrics["tpot_p50"], metrics["e2e_p50"], metrics["queue_p50"]],
            "P90": [metrics["ttft_p90"], metrics["tpot_p90"], metrics["e2e_p90"], None],
            "P95": [metrics["ttft_p95"], metrics["tpot_p95"], metrics["e2e_p95"], metrics["queue_p95"]],
            "P99": [metrics["ttft_p99"], metrics["tpot_p99"], metrics["e2e_p99"], None],
        }
    )
    st.dataframe(
        latency_frame,
        width="stretch",
        hide_index=True,
        key=f"{key_prefix}_latency_table",
    )

    distribution_fig = make_subplots(
        rows=1,
        cols=3,
        subplot_titles=("TTFT (ms)", "TPOT (ms/token)", "E2E (ms)"),
    )
    series = [
        [result.ttft_ms for result in progress.results if result.status == 200 and result.ttft_ms > 0],
        [result.tpot_ms for result in progress.results if result.status == 200 and result.tpot_ms > 0],
        [result.e2e_ms for result in progress.results if result.status == 200],
    ]
    for index, values_for_chart in enumerate(series, start=1):
        distribution_fig.add_trace(
            go.Histogram(x=values_for_chart, nbinsx=30, marker_color=BLUE),
            row=1,
            col=index,
        )
    distribution_fig.update_layout(showlegend=False, bargap=0.08)
    _style_chart(distribution_fig, 340)
    st.plotly_chart(
        distribution_fig,
        width="stretch",
        key=f"{key_prefix}_latency_distributions",
    )

    failed_results = [result for result in progress.results if result.status != 200]
    if failed_results:
        section_title("Failure details")
        st.dataframe(
            pd.DataFrame(
                [
                    {
                        "Request": result.id,
                        "Status": status_label(result),
                        "Error": result.error or result.response_text or "Unknown error",
                        "E2E (ms)": round(result.e2e_ms, 1),
                        "Queue (ms)": round(result.queue_ms, 1),
                        "Started (s)": round(result.started_at_s, 3),
                    }
                    for result in failed_results
                ]
            ),
            width="stretch",
            hide_index=True,
            key=f"{key_prefix}_failure_table",
        )

    section_title("Timelines")
    results_frame = pd.DataFrame(
        [
            {
                "req": result.id,
                "started": result.started_at_s,
                "ttft": result.ttft_ms,
                "tpot": result.tpot_ms,
                "e2e": result.e2e_ms,
                "queue": result.queue_ms,
                "status": status_label(result),
            }
            for result in progress.results
        ]
    )
    timeline_fig = make_subplots(
        rows=3,
        cols=2,
        subplot_titles=("TTFT", "E2E", "TPOT", "Client queue", "In-flight requests", "Status codes"),
        specs=[[{"type": "scatter"}, {"type": "scatter"}], [{"type": "scatter"}, {"type": "scatter"}], [{"type": "scatter"}, {"type": "pie"}]],
    )
    for row, column, key, name, color in [
        (1, 1, "ttft", "TTFT", BLUE),
        (1, 2, "e2e", "E2E", ACCENT),
        (2, 1, "tpot", "TPOT", "#8b7cf6"),
        (2, 2, "queue", "Queue", "#f6b84a"),
    ]:
        timeline_fig.add_trace(
            go.Scatter(
                x=results_frame["started"],
                y=results_frame[key],
                mode="markers",
                name=name,
                marker={"size": 5, "color": color},
            ),
            row=row,
            col=column,
        )

    events = []
    for result in progress.results:
        events.extend([(result.started_at_s, 1), (result.completed_at_s, -1)])
    events.sort(key=lambda event: (event[0], event[1]))
    active = 0
    concurrency_x = []
    concurrency_y = []
    for event_time, change in events:
        active += change
        concurrency_x.append(event_time)
        concurrency_y.append(active)
    timeline_fig.add_trace(
        go.Scatter(
            x=concurrency_x,
            y=concurrency_y,
            mode="lines",
            line_shape="hv",
            line={"color": BLUE},
            name="In-flight",
        ),
        row=3,
        col=1,
    )
    status_counts = results_frame["status"].value_counts()
    timeline_fig.add_trace(
        go.Pie(
            labels=status_counts.index.astype(str),
            values=status_counts.values,
            marker={"colors": ["#5ed18c", ACCENT, "#f6b84a", BLUE]},
            name="Status",
        ),
        row=3,
        col=2,
    )
    timeline_fig.update_layout(showlegend=False)
    _style_chart(timeline_fig, 850)
    st.plotly_chart(timeline_fig, width="stretch", key=f"{key_prefix}_timelines")

    with st.expander("All request results"):
        st.dataframe(
            pd.DataFrame(
                [
                    {
                        "Request": result.id,
                        "Status": status_label(result),
                        "TTFT (ms)": round(result.ttft_ms, 1),
                        "TPOT (ms)": round(result.tpot_ms, 2),
                        "E2E (ms)": round(result.e2e_ms, 1),
                        "Queue (ms)": round(result.queue_ms, 1),
                        "Prompt tokens": result.prompt_tokens,
                        "Output tokens": result.completion_tokens,
                        "Cached tokens": result.cached_tokens,
                        "Error": result.error,
                    }
                    for result in progress.results
                ]
            ),
            width="stretch",
            hide_index=True,
            key=f"{key_prefix}_request_table",
        )

    with st.expander("Request / response inspector"):
        request_ids = [result.id for result in progress.results]
        selected_request_id = st.selectbox(
            "Request",
            request_ids,
            key=f"{key_prefix}_request_inspector",
        )
        selected_result = next(
            result for result in progress.results if result.id == selected_request_id
        )
        request_tab, response_tab, error_tab = st.tabs(
            ["Request body", "Response body", "Error"]
        )
        with request_tab:
            st.json(selected_result.request_body)
        with response_tab:
            st.json(selected_result.response_body)
            if selected_result.response_text:
                st.text_area(
                    "Raw response",
                    selected_result.response_text,
                    height=240,
                    disabled=True,
                    key=f"{key_prefix}_response_text_{selected_request_id}",
                )
        with error_tab:
            if selected_result.error:
                st.error(selected_result.error)
            else:
                st.success("No error recorded for this request.")
