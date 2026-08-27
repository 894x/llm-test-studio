"""Run history, detailed report, and comparison page."""

from __future__ import annotations

import json
from pathlib import Path

import pandas as pd
import plotly.graph_objects as go
import streamlit as st

from llm_test.metrics import compute_metrics
from llm_test.audit_storage import load_audit_history, load_audit_run
from llm_test.storage import load_history, load_saved_run
from llm_test.ui_components import render_copy_png_button, render_results
from llm_test.ui_theme import page_heading, section_title
from report_export import build_report_artifacts


PROJECT_DIR = Path(__file__).resolve().parents[2]


def _render_performance_runs() -> None:
    history = load_history()
    if history.empty:
        st.info("No test history yet. Open Test Plans to start a performance run.")
        return

    display_columns = [
        "id", "ts", "model", "requests", "concurrency", "peak_in_flight",
        "success", "failures", "total", "qps", "total_tpm", "elapsed",
        "ttft_p50", "tpot_p50", "e2e_p50",
    ]
    available = [column for column in display_columns if column in history.columns]
    display = history[available].copy()
    display = display.rename(
        columns={
            "id": "Run", "ts": "Started", "model": "Model", "requests": "Requests",
            "concurrency": "Connection cap", "peak_in_flight": "Peak in-flight",
            "success": "OK", "failures": "Failed", "total": "Completed",
            "qps": "QPS", "total_tpm": "Total TPM", "elapsed": "Elapsed (s)",
            "ttft_p50": "TTFT P50", "tpot_p50": "TPOT P50", "e2e_p50": "E2E P50",
        }
    )
    display["Run"] = display["Run"].map(lambda value: f"RUN-{int(value):04d}")
    st.dataframe(display, width="stretch", hide_index=True, height=360)

    section_title("Detailed report")
    labels = {
        f"RUN-{int(row['id']):04d} · {row['ts']} · {row['model']} · "
        f"{int(row['success'])}/{int(row['total'])} OK": int(row["id"])
        for _, row in history.iterrows()
    }
    selected_label = st.selectbox("Saved run", labels, label_visibility="collapsed")
    run_id = labels[selected_label]
    summary, progress, export_data = load_saved_run(run_id)
    if summary is None:
        st.error(f"Saved run #{run_id} no longer exists.")
    elif not progress.results:
        st.warning("This run predates per-request recording, so only its summary is available.")
    else:
        report = build_report_artifacts(
            run_id,
            summary,
            compute_metrics(progress),
            progress.results,
        )
        actions = st.columns(4)
        actions[0].download_button(
            "Download JSON",
            data=json.dumps(export_data, ensure_ascii=False, indent=2),
            file_name=f"loadtest-run-{run_id}.json",
            mime="application/json",
            width="stretch",
        )
        actions[1].download_button(
            "Download HTML",
            data=report.html,
            file_name=f"loadtest-run-{run_id}.html",
            mime="text/html",
            width="stretch",
        )
        actions[2].download_button(
            "Download PDF",
            data=report.pdf,
            file_name=f"loadtest-run-{run_id}.pdf",
            mime="application/pdf",
            width="stretch",
        )
        with actions[3]:
            render_copy_png_button(report.png, run_id)
        render_results(progress, f"history_run_{run_id}")

    if len(history) >= 2:
        section_title("Run comparison")
        trend = history.sort_values("id")
        figure = go.Figure()
        for column, name, color in [
            ("ttft_p50", "TTFT P50", "#5596ff"),
            ("e2e_p50", "E2E P50", "#ff5a52"),
            ("tpot_p50", "TPOT P50", "#8b7cf6"),
        ]:
            figure.add_trace(
                go.Scatter(
                    x=trend["ts"], y=trend[column], name=name,
                    mode="lines+markers", line={"color": color, "width": 2},
                )
            )
        figure.update_layout(
            height=380,
            paper_bgcolor="rgba(0,0,0,0)",
            plot_bgcolor="rgba(0,0,0,0)",
            font={"color": "#c7d2e1", "size": 11},
            legend={"orientation": "h", "y": 1.12},
            margin={"l": 30, "r": 10, "t": 42, "b": 30},
        )
        figure.update_xaxes(gridcolor="#26364a")
        figure.update_yaxes(gridcolor="#26364a", title="ms")
        st.plotly_chart(figure, width="stretch", key="runs_comparison")


def _resolve_artifact(relative_path: str) -> Path:
    path = Path(relative_path)
    return path if path.is_absolute() else PROJECT_DIR / path


def _render_audit_runs() -> None:
    history = load_audit_history()
    if not history:
        st.info("No compatibility or Seedance audits have been recorded yet.")
        return

    history_frame = pd.DataFrame(
        [
            {
                "Audit": f"AUDIT-{run['id']:04d}",
                "Started": run["ts"][:19],
                "Suite": run["suite"],
                "Model": run["model"] or "Engine default",
                "Overall": run["overall"],
                "Passed": run["summary"].get("pass", 0),
                "Warnings": run["summary"].get("warning", 0),
                "Failed": run["summary"].get("fail", 0),
                "Unknown": run["summary"].get("unknown", 0),
            }
            for run in history
        ]
    )
    st.dataframe(history_frame, width="stretch", hide_index=True, height=330)

    section_title("Audit detail")
    labels = {
        f"AUDIT-{run['id']:04d} · {run['suite']} · {run['model'] or 'Engine default'} · {run['overall']}": run["id"]
        for run in history
    }
    selected = st.selectbox("Saved audit", labels, label_visibility="collapsed")
    audit = load_audit_run(labels[selected])
    if audit is None:
        st.error("The selected audit no longer exists.")
        return

    st.markdown(f"**{audit['verdict']}**")
    summary_columns = st.columns(4)
    for column, key, label in zip(
        summary_columns,
        ["pass", "warning", "fail", "unknown"],
        ["Passed", "Warnings", "Failed", "Unknown"],
    ):
        column.metric(label, audit["summary"].get(key, 0))

    result_frame = pd.DataFrame(
        [
            {
                "Case": result.get("id", ""),
                "Name": result.get("name", ""),
                "Dimension": result.get("dimension", ""),
                "Model": result.get("model", ""),
                "Status": result.get("status", ""),
                "Severity": result.get("severity", ""),
                "Elapsed (ms)": result.get("elapsed_ms", 0),
                "Evidence": result.get("evidence", ""),
            }
            for result in audit["results"]
        ]
    )
    st.dataframe(result_frame, width="stretch", hide_index=True, height=430)

    downloads = st.columns(2)
    html_path = _resolve_artifact(audit["report_html"])
    json_path = _resolve_artifact(audit["report_json"])
    if html_path.is_file():
        downloads[0].download_button(
            "Download audit HTML",
            html_path.read_bytes(),
            file_name=f"audit-{audit['id']}.html",
            mime="text/html",
            width="stretch",
        )
    if json_path.is_file():
        downloads[1].download_button(
            "Download audit JSON",
            json_path.read_bytes(),
            file_name=f"audit-{audit['id']}.json",
            mime="application/json",
            width="stretch",
        )


def render_runs_page() -> None:
    page_heading(
        "Runs",
        "Inspect performance samples and compatibility evidence, compare trends, and export complete reports.",
    )
    performance_tab, audit_tab = st.tabs(["Performance", "Compatibility & Seedance"])
    with performance_tab:
        _render_performance_runs()
    with audit_tab:
        _render_audit_runs()
