"""Shared visual system for the Streamlit application."""

from __future__ import annotations

import streamlit as st


APP_CSS = """
<style>
:root {
  --lab-bg: #0b0d12;
  --lab-rail: #090b0f;
  --lab-surface: #11141a;
  --lab-surface-strong: #171b23;
  --lab-border: #2a303a;
  --lab-border-soft: #20252e;
  --lab-text: #f5f6f8;
  --lab-muted: #98a1af;
  --lab-accent: #f06a61;
  --lab-blue: #78a9ff;
  --lab-green: #65c58f;
  --lab-amber: #dda84c;
}

html, body, [class*="css"] {
  font-family: Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}

[data-testid="stAppViewContainer"], .stApp {
  background: var(--lab-bg);
}

[data-testid="stHeader"] {
  background: rgba(11, 13, 18, .94);
  border-bottom: 1px solid var(--lab-border-soft);
}

[data-testid="stSidebar"],
[data-testid="collapsedControl"] { display: none; }

[data-testid="stMainBlockContainer"] {
  width: 100%;
  max-width: none;
  padding: 1.35rem 1.5rem 2.5rem;
}

h1, h2, h3 {
  color: var(--lab-text);
  letter-spacing: -.025em;
}

h1 { font-size: 2rem !important; font-weight: 720 !important; }
h2 { font-size: 1.35rem !important; font-weight: 680 !important; }
h3 { font-size: 1.05rem !important; font-weight: 650 !important; }

.lab-header-brand {
  position: fixed;
  z-index: 1000001;
  top: 10px;
  left: 22px;
  display: flex;
  align-items: center;
  gap: .7rem;
  height: 38px;
  color: var(--lab-text);
  font-size: 1.18rem;
  font-weight: 730;
  letter-spacing: -.02em;
}

.lab-brand-mark {
  display: grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border: 1px solid rgba(240,106,97,.7);
  border-radius: 8px;
  color: var(--lab-accent);
  font-family: "Material Symbols Rounded";
  font-size: 18px;
  font-variation-settings: 'FILL' 0, 'wght' 450, 'GRAD' 0, 'opsz' 20;
}

.st-key-top_navigation {
  position: fixed;
  z-index: 1000001;
  top: 9px;
  left: 50%;
  width: 230px;
  transform: translateX(-50%);
}

.st-key-top_navigation [data-testid="stHorizontalBlock"] {
  flex-wrap: nowrap !important;
  gap: 4px;
  padding: 3px;
  border: 1px solid var(--lab-border-soft);
  border-radius: 10px;
  background: rgba(17, 20, 26, .9);
}

.st-key-top_navigation [data-testid="column"] {
  flex: 0 0 36px !important;
  min-width: 36px !important;
  width: 36px !important;
}

.st-key-top_navigation [data-testid="stPageLink-NavLink"] {
  position: relative;
  display: flex !important;
  width: 36px;
  height: 34px;
  align-items: center !important;
  justify-content: center !important;
  gap: 0 !important;
  padding: 0;
  border-radius: 7px;
  color: var(--lab-muted);
  text-decoration: none;
  transition: background-color .14s ease, color .14s ease;
}

.st-key-top_navigation [data-testid="stPageLink-NavLink"] > span:last-child {
  display: none !important;
}

.st-key-top_navigation [data-testid="stPageLink-NavLink"] > span:first-child {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin: 0 !important;
}

.st-key-top_navigation [data-testid="stPageLink-NavLink"]:hover,
.st-key-top_navigation [data-testid="stPageLink-NavLink"]:focus-visible {
  background: var(--lab-surface-strong);
  color: var(--lab-text);
  outline: none;
}

.st-key-top_navigation [data-testid="stPageLink-NavLink"]::after {
  position: absolute;
  top: 42px;
  left: 50%;
  z-index: 1000002;
  padding: .35rem .5rem;
  border: 1px solid var(--lab-border);
  border-radius: 6px;
  background: #171b23;
  color: var(--lab-text);
  font-family: Inter, ui-sans-serif, sans-serif;
  font-size: 11px;
  line-height: 1;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transform: translate(-50%, -4px);
  transition: opacity .12s ease, transform .12s ease;
}

.st-key-top_navigation a[href=""]::after { content: "Overview"; }
.st-key-top_navigation a[href="render_models_page"]::after { content: "Models"; }
.st-key-top_navigation a[href="render_cases_page"]::after { content: "Test Cases"; }
.st-key-top_navigation a[href="render_plans_page"]::after { content: "Test Plans"; }
.st-key-top_navigation a[href="render_runs_page"]::after { content: "Runs"; }

.st-key-top_navigation [data-testid="stPageLink-NavLink"]:hover::after,
.st-key-top_navigation [data-testid="stPageLink-NavLink"]:focus-visible::after {
  opacity: 1;
  transform: translate(-50%, 0);
}

.st-key-top_navigation [data-testid="stIconMaterial"] {
  display: inline-flex !important;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  width: 21px;
  height: 21px;
  margin: 0 !important;
  font-size: 19px;
  line-height: 1;
  white-space: nowrap;
}

.lab-page-heading {
  margin: 0 0 1.05rem;
}

.lab-page-heading h1 {
  margin: 0;
  padding: 0;
}

.lab-page-heading p {
  max-width: 760px;
  margin: .35rem 0 0;
  color: var(--lab-muted);
  font-size: 14px;
  line-height: 1.55;
}

.lab-section-title {
  margin: 1.25rem 0 .65rem;
  color: var(--lab-text);
  font-size: 1.08rem;
  font-weight: 680;
  letter-spacing: -.012em;
}

.lab-stat {
  min-height: 92px;
  padding: .85rem 1rem;
  border: 1px solid var(--lab-border);
  border-radius: 10px;
  background: var(--lab-surface);
}

.lab-resource-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin: -.25rem 0 .8rem;
  border: 1px solid var(--lab-border-soft);
  border-radius: 9px;
  background: var(--lab-surface);
}

.lab-summary-item {
  min-width: 0;
  padding: .58rem .8rem;
  border-right: 1px solid var(--lab-border-soft);
}

.lab-summary-item:last-child { border-right: 0; }
.lab-summary-value { color: var(--lab-text); font-size: 1rem; font-weight: 710; }
.lab-summary-label { margin-top: .12rem; color: var(--lab-muted); font-size: 11px; }

.lab-stat-label {
  color: #b6c2d2;
  font-size: 13px;
  font-weight: 560;
}

.lab-stat-value {
  margin-top: .35rem;
  color: var(--lab-text);
  font-size: 1.75rem;
  font-weight: 730;
  letter-spacing: -.035em;
}

.lab-stat-meta {
  margin-top: .18rem;
  color: var(--lab-muted);
  font-size: 12px;
}

.lab-panel {
  padding: .8rem .9rem;
  border: 1px solid var(--lab-border);
  border-radius: 10px;
  background: var(--lab-surface);
}

.lab-panel-title {
  margin-bottom: .85rem;
  color: var(--lab-text);
  font-size: 15px;
  font-weight: 680;
}

.lab-status {
  display: inline-flex;
  align-items: center;
  gap: .38rem;
  color: var(--lab-muted);
  font-size: 12px;
}

.lab-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: currentColor;
}

.lab-status--pass { color: var(--lab-green); }
.lab-status--review { color: var(--lab-amber); }
.lab-status--fail { color: var(--lab-accent); }
.lab-status--running { color: var(--lab-blue); }

.lab-suite {
  padding: .8rem 0;
  border-bottom: 1px solid var(--lab-border-soft);
}

.lab-suite:last-child { border-bottom: 0; }
.lab-suite-name { color: var(--lab-text); font-size: 14px; font-weight: 620; }
.lab-suite-meta { margin-top: .2rem; color: var(--lab-muted); font-size: 12px; }

div[data-testid="stMetric"] {
  min-height: 88px;
  padding: .75rem .9rem;
  border: 1px solid var(--lab-border);
  border-radius: 10px;
  background: var(--lab-surface);
}

div[data-testid="stMetric"] label { color: #b6c2d2; font-size: 13px; }
div[data-testid="stMetricValue"] { font-size: 1.55rem; font-weight: 720; }

[data-testid="stDataFrame"], [data-testid="stTable"] {
  border: 1px solid var(--lab-border);
  border-radius: 10px;
  overflow: hidden;
}

[data-testid="stForm"] {
  padding: .85rem;
  border: 1px solid var(--lab-border);
  border-radius: 10px;
  background: var(--lab-surface);
}

[role="dialog"] [data-testid="stForm"] {
  border: 0;
  background: transparent;
  padding: .25rem 0 0;
}

[role="dialog"] > div {
  border: 1px solid var(--lab-border);
  border-radius: 12px;
  background: var(--lab-surface-strong);
}

.st-key-model_toolbar,
.st-key-case_toolbar {
  margin-bottom: .8rem;
  padding: .62rem .72rem !important;
  background: var(--lab-surface);
}

.st-key-model_toolbar [data-testid="stVerticalBlock"],
.st-key-case_toolbar [data-testid="stVerticalBlock"] {
  gap: .4rem;
}

.st-key-model_selection_bar,
.st-key-case_selection_bar {
  margin-top: .8rem;
  padding: .65rem .75rem !important;
  border-left: 2px solid var(--lab-accent) !important;
  background: var(--lab-surface-strong);
}

.stButton > button,
.stDownloadButton > button,
button[kind="secondary"] {
  min-height: 40px;
  border-color: var(--lab-border);
  border-radius: 8px;
  font-size: 13px;
  font-weight: 630;
}

.stButton > button[kind="primary"],
.stFormSubmitButton > button[kind="primary"] {
  border-color: var(--lab-accent);
  background: var(--lab-accent);
  color: #fff;
}

.stButton > button[kind="primary"]:hover,
.stFormSubmitButton > button[kind="primary"]:hover {
  border-color: #ff726b;
  background: #ff655e;
}

[data-baseweb="input"] > div,
[data-baseweb="select"] > div,
[data-baseweb="textarea"] > div {
  border-color: var(--lab-border) !important;
  border-radius: 8px !important;
  background: #0d1016 !important;
}

[data-testid="stTabs"] [data-baseweb="tab-list"] {
  gap: .25rem;
  border-bottom: 1px solid var(--lab-border-soft);
}

[data-testid="stTabs"] [data-baseweb="tab"] {
  min-height: 42px;
  padding: 0 1rem;
  font-size: 13px;
}

[data-testid="stExpander"] {
  border-color: var(--lab-border);
  border-radius: 8px;
  background: var(--lab-surface);
}

@media (max-width: 900px) {
  [data-testid="stMainBlockContainer"] { padding: .9rem .8rem 2rem; }
  .lab-page-heading { margin-bottom: 1.1rem; }
  .lab-stat { min-height: 92px; }
  .lab-header-brand span:last-child { display: none; }
  .lab-header-brand { left: 12px; }
  .lab-resource-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .lab-summary-item:nth-child(2) { border-right: 0; }
  .lab-summary-item:nth-child(-n+2) { border-bottom: 1px solid var(--lab-border-soft); }
}

@media (max-width: 600px) {
  .st-key-top_navigation { width: 190px; }
  .st-key-top_navigation [data-testid="stHorizontalBlock"] { gap: 2px; padding: 2px; }
  .st-key-top_navigation [data-testid="column"] { flex-basis: 30px !important; min-width: 30px !important; width: 30px !important; }
  .st-key-top_navigation [data-testid="stPageLink-NavLink"] { width: 30px; height: 32px; }
  .st-key-top_navigation [data-testid="stIconMaterial"] { width: 19px; font-size: 18px; }
  .lab-brand-mark { width: 28px; height: 28px; border-radius: 7px; }
  .lab-header-brand { top: 12px; }
}
</style>
"""


def apply_theme() -> None:
    st.markdown(APP_CSS, unsafe_allow_html=True)


def render_top_navigation(pages: list) -> None:
    st.markdown(
        '<div class="lab-header-brand"><span class="lab-brand-mark">science</span>'
        '<span>LLM Test Lab</span></div>',
        unsafe_allow_html=True,
    )
    links = [
        ("Overview", ":material/dashboard:"),
        ("Models", ":material/deployed_code:"),
        ("Test Cases", ":material/lab_profile:"),
        ("Test Plans", ":material/calendar_add_on:"),
        ("Runs", ":material/play_circle:"),
    ]
    with st.container(key="top_navigation"):
        columns = st.columns(5, gap=None)
        for column, page, (label, icon) in zip(columns, pages, links):
            column.page_link(page, label=label, icon=icon)


def page_heading(title: str, description: str) -> None:
    routes = {
        "Overview": "",
        "Models": "render_models_page",
        "Test Cases": "render_cases_page",
        "Test Plans": "render_plans_page",
        "Runs": "render_runs_page",
    }
    route = routes.get(title)
    if route is not None:
        st.markdown(
            "<style>"
            f'.st-key-top_navigation a[href="{route}"] {{ background: rgba(240, 106, 97, .16); '
            "color: #f3827a; }}"
            "</style>",
            unsafe_allow_html=True,
        )
    st.markdown(
        f'<div class="lab-page-heading"><h1>{title}</h1><p>{description}</p></div>',
        unsafe_allow_html=True,
    )


def section_title(title: str) -> None:
    st.markdown(f'<div class="lab-section-title">{title}</div>', unsafe_allow_html=True)


def resource_summary(items: list[tuple[str, str]]) -> None:
    cells = "".join(
        f'<div class="lab-summary-item"><div class="lab-summary-value">{value}</div>'
        f'<div class="lab-summary-label">{label}</div></div>'
        for label, value in items
    )
    st.markdown(
        f'<div class="lab-resource-summary">{cells}</div>',
        unsafe_allow_html=True,
    )


def stat_card(label: str, value: str, meta: str = "") -> None:
    meta_html = f'<div class="lab-stat-meta">{meta}</div>' if meta else ""
    st.markdown(
        f'<div class="lab-stat"><div class="lab-stat-label">{label}</div>'
        f'<div class="lab-stat-value">{value}</div>{meta_html}</div>',
        unsafe_allow_html=True,
    )
