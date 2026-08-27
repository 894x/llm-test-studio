"""Overview dashboard."""

from __future__ import annotations

from pathlib import Path

import pandas as pd
import plotly.graph_objects as go
import streamlit as st

from llm_test.catalog import Catalog
from llm_test.audit_storage import count_audit_runs
from llm_test.storage import DB_PATH, count_runs, load_history
from llm_test.ui_theme import page_heading, section_title, stat_card


PROJECT_DIR = Path(__file__).resolve().parents[2]


def _database_size() -> str:
    if not DB_PATH.exists():
        return "0 MB"
    size_mb = DB_PATH.stat().st_size / 1024 / 1024
    return f"{size_mb:.0f} MB" if size_mb >= 10 else f"{size_mb:.1f} MB"


def _run_status(row: pd.Series) -> str:
    return "Passed" if int(row.get("failures", 0) or 0) == 0 else "Failed"


def render_overview_page() -> None:
    page_heading(
        "Overview",
        "Monitor model coverage, recent test activity, and performance changes from one place.",
    )

    catalog = Catalog(PROJECT_DIR)
    models = catalog.list_models()
    cases = catalog.list_cases()
    history = load_history()

    stat_columns = st.columns(4)
    values = [
        ("Models", str(len(models)), "Configured model profiles"),
        ("Test cases", str(len(cases)), "Versioned compatibility cases"),
        ("Runs", str(count_runs() + count_audit_runs()), "Performance and compatibility"),
        ("Database", _database_size(), "SQLite run history"),
    ]
    for column, (label, value, meta) in zip(stat_columns, values):
        with column:
            stat_card(label, value, meta)

    left, right = st.columns([1.35, 1], gap="large")
    with left:
        section_title("Recent runs")
        if history.empty:
            st.info("No runs have been recorded yet. Open Test Plans to start one.")
        else:
            recent = history.head(7).copy()
            recent_display = pd.DataFrame(
                {
                    "Run": [f"RUN-{int(value):04d}" for value in recent["id"]],
                    "Type": "Performance",
                    "Model": recent["model"],
                    "Status": recent.apply(_run_status, axis=1),
                    "Started": recent["ts"].astype(str).str.slice(0, 19),
                    "Duration": recent.get("elapsed", pd.Series([0] * len(recent))).fillna(0).map(lambda value: f"{float(value):.1f}s"),
                }
            )
            st.dataframe(recent_display, width="stretch", hide_index=True, height=320)

    with right:
        section_title("Performance trend")
        if len(history) < 2:
            st.info("At least two runs are needed to draw a trend.")
        else:
            trend = history.sort_values("id").tail(14)
            figure = go.Figure()
            figure.add_trace(
                go.Scatter(
                    x=trend["ts"],
                    y=trend["ttft_p50"],
                    name="TTFT P50",
                    mode="lines+markers",
                    line={"color": "#5596ff", "width": 2},
                )
            )
            figure.add_trace(
                go.Scatter(
                    x=trend["ts"],
                    y=trend["e2e_p50"],
                    name="E2E P50",
                    mode="lines+markers",
                    line={"color": "#ff5a52", "width": 2},
                )
            )
            figure.update_layout(
                height=320,
                paper_bgcolor="rgba(0,0,0,0)",
                plot_bgcolor="rgba(0,0,0,0)",
                font={"color": "#c7d2e1", "size": 11},
                legend={"orientation": "h", "y": 1.12},
                margin={"l": 20, "r": 10, "t": 36, "b": 20},
                hovermode="x unified",
            )
            figure.update_xaxes(gridcolor="#26364a", title=None)
            figure.update_yaxes(gridcolor="#26364a", title="ms")
            st.plotly_chart(figure, width="stretch", key="overview_performance_trend")

    section_title("Test coverage")
    protocol_columns = st.columns(3)
    protocols = ["openai-chat", "kimi-k3", "seedance"]
    for column, protocol in zip(protocol_columns, protocols):
        protocol_cases = [case for case in cases if case["protocol"] == protocol]
        default_count = sum(bool(case.get("default")) for case in protocol_cases)
        dimensions = len({case["dimension"] for case in protocol_cases})
        with column:
            st.markdown(
                '<div class="lab-panel">'
                f'<div class="lab-panel-title">{protocol}</div>'
                f'<div class="lab-stat-value">{len(protocol_cases)}</div>'
                f'<div class="lab-stat-meta">{default_count} default · {dimensions} dimensions</div>'
                "</div>",
                unsafe_allow_html=True,
            )
