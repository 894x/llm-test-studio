"""Application shell and page navigation."""

from __future__ import annotations

import streamlit as st

from .pages.cases import render_cases_page
from .pages.models import render_models_page
from .pages.overview import render_overview_page
from .pages.plans import render_plans_page
from .pages.runs import render_runs_page
from .ui_theme import apply_theme, render_top_navigation


def run_app() -> None:
    st.set_page_config(
        page_title="LLM Test Lab",
        page_icon="🧪",
        layout="wide",
        initial_sidebar_state="collapsed",
    )
    apply_theme()

    page_specs = [
        (render_overview_page, "Overview", ":material/dashboard:", True),
        (render_models_page, "Models", ":material/deployed_code:", False),
        (render_cases_page, "Test Cases", ":material/lab_profile:", False),
        (render_plans_page, "Test Plans", ":material/calendar_add_on:", False),
        (render_runs_page, "Runs", ":material/play_circle:", False),
    ]
    pages = [
        st.Page(page, title=title, icon=icon, default=default)
        for page, title, icon, default in page_specs
    ]
    navigation = st.navigation(pages, position="hidden")
    render_top_navigation(pages)
    navigation.run()
