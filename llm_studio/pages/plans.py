"""Test plan creation and performance execution page."""

from __future__ import annotations

import asyncio
import base64
from datetime import datetime
from pathlib import Path

import streamlit as st

from loadtest import (
    DEFAULT_IMAGE_PROMPT,
    DEFAULT_VIDEO_PROMPT,
    MAX_CONCURRENT_CONNECTIONS,
    LoadTestConfig,
    LoadTestProgress,
    run_load_test,
)
from llm_studio.catalog import Catalog
from llm_studio.audit_storage import save_audit_run
from llm_studio.engine_client import EngineClient, EngineError
from llm_studio.metrics import compute_metrics, status_label
from llm_studio.storage import save_run
from llm_studio.ui_components import format_rate, render_results
from llm_studio.ui_theme import page_heading


PROJECT_DIR = Path(__file__).resolve().parents[2]
FIXTURE_DIR = PROJECT_DIR / "fixtures"


def _fixture_data_url(filename: str, mime_type: str) -> str:
    encoded = base64.b64encode((FIXTURE_DIR / filename).read_bytes()).decode("ascii")
    return f"data:{mime_type};base64,{encoded}"


def _render_performance_plan() -> None:
    catalog = Catalog(PROJECT_DIR)
    profiles = [model for model in catalog.list_models(enabled=True)]
    profile_labels = {
        f"{model.display_name} · {model.protocol}": model for model in profiles
    }
    if not profiles:
        st.warning("Add and enable a model profile before creating a performance run.")
        return

    with st.form("performance_plan_form"):
        top_left, top_middle, top_right = st.columns([1.2, 1.4, 1])
        selected_label = top_left.selectbox("Model profile", list(profile_labels))
        selected_profile = profile_labels[selected_label]
        endpoint = top_middle.text_input(
            "API endpoint",
            value=selected_profile.endpoint,
            placeholder="https://gateway.example/v1/chat/completions",
        )
        api_key = top_right.text_input(
            "API key",
            type="password",
            help="Used only for this run and never persisted.",
        )

        st.markdown("#### Workload")
        row_a = st.columns(4)
        total_requests = row_a[0].number_input("Requests", min_value=1, value=20, step=1)
        duration_s = row_a[1].number_input("Dispatch window (s)", min_value=1, value=60, step=1)
        input_tokens = row_a[2].number_input("Input tokens", min_value=10, value=1000, step=100)
        max_tokens = row_a[3].number_input("Output tokens", min_value=1, value=100, step=1)

        row_b = st.columns(4)
        max_connections = row_b[0].number_input(
            "Connections",
            min_value=1,
            max_value=MAX_CONCURRENT_CONNECTIONS,
            value=min(200, MAX_CONCURRENT_CONNECTIONS),
            step=10,
        )
        timeout_s = row_b[1].number_input(
            "Timeout (s)", min_value=1, max_value=3600, value=120, step=30
        )
        stream = row_b[2].checkbox("Streaming", value=True)
        random_distribution = row_b[3].checkbox("Random distribution", value=True)

        with st.expander("Capture and multimodal options"):
            capture_columns = st.columns(2)
            capture_policy = capture_columns[0].selectbox(
                "Response capture",
                ["errors_and_sample", "errors_only", "none", "all"],
                help="Request metrics are always stored. This controls raw response capture.",
            )
            sample_limit = capture_columns[1].number_input(
                "Successful response samples",
                min_value=0,
                max_value=100,
                value=10,
                disabled=capture_policy != "errors_and_sample",
            )
            media_columns = st.columns(2)
            include_image = media_columns[0].checkbox("Include image fixture", value=False)
            include_video = media_columns[1].checkbox("Include video fixture", value=False)
            image_prompt = DEFAULT_IMAGE_PROMPT
            video_prompt = DEFAULT_VIDEO_PROMPT
            if include_image:
                image_prompt = st.text_area("Image prompt", value=DEFAULT_IMAGE_PROMPT, height=88)
            if include_video:
                video_prompt = st.text_area("Video prompt", value=DEFAULT_VIDEO_PROMPT, height=88)

        submitted = st.form_submit_button(
            "Run performance test",
            type="primary",
            width="stretch",
            icon=":material/play_arrow:",
        )

    if submitted:
        if not endpoint.strip():
            st.error("Enter an API endpoint before starting the test.")
            return
        if not api_key:
            st.error("Enter an API key before starting the test.")
            return
        if include_image and not image_prompt.strip():
            st.error("Enter an image prompt or disable the image fixture.")
            return
        if include_video and not video_prompt.strip():
            st.error("Enter a video prompt or disable the video fixture.")
            return

        config = LoadTestConfig(
            url=endpoint.strip(),
            key=api_key,
            model=selected_profile.model,
            total_requests=int(total_requests),
            duration_s=int(duration_s),
            max_tokens=int(max_tokens),
            input_tokens=int(input_tokens),
            stream=stream,
            random=random_distribution,
            max_connections=int(max_connections),
            request_timeout_s=int(timeout_s),
            image_data_url=(
                _fixture_data_url("number-7.png", "image/png") if include_image else ""
            ),
            image_prompt=image_prompt,
            video_data_url=(
                _fixture_data_url("number-sequence-3-7-4.mp4", "video/mp4")
                if include_video
                else ""
            ),
            video_prompt=video_prompt,
        )
        progress = LoadTestProgress()
        status = st.status("Running performance test…", expanded=True)
        live = st.empty()

        def update_live(current: LoadTestProgress) -> None:
            with live.container():
                metrics = compute_metrics(current)
                st.progress(
                    current.done / max(current.total, 1),
                    text=f"{current.done}/{current.total} requests completed",
                )
                columns = st.columns(5)
                columns[0].metric("OK", f"{metrics['success']}/{metrics['total']}")
                columns[1].metric("Failed", metrics["failures"])
                columns[2].metric("In-flight", metrics["in_flight"])
                columns[3].metric("QPS", f"{metrics['request_qps']:.1f}")
                columns[4].metric("Total TPM", format_rate(metrics["total_tpm"]))
                if metrics["failures"]:
                    latest = next(result for result in reversed(current.results) if result.status != 200)
                    st.error(
                        f"Latest failure #{latest.id}: {status_label(latest)} — "
                        f"{latest.error or latest.response_text or 'Unknown error'}"
                    )

        loop = asyncio.new_event_loop()
        try:
            loop.run_until_complete(run_load_test(config, progress, update_live))
        finally:
            loop.close()
        status.update(label="Saving run data…", state="running")
        run_id = save_run(
            config,
            progress,
            capture_policy=capture_policy,
            capture_sample_limit=int(sample_limit),
        )
        status.update(label=f"Run #{run_id} complete", state="complete", expanded=False)
        st.session_state.last_performance_run = progress
        st.session_state.last_performance_run_id = run_id
        st.rerun()

    if "last_performance_run" in st.session_state:
        st.divider()
        st.subheader(f"Latest performance run · #{st.session_state.last_performance_run_id}")
        render_results(st.session_state.last_performance_run, "latest_performance")


def _render_engine_plan(kind: str) -> None:
    catalog = Catalog(PROJECT_DIR)
    client = EngineClient(PROJECT_DIR)
    try:
        if kind == "seedance":
            suite = "seedance"
        else:
            suite = st.selectbox(
                "Compatibility suite",
                ["openai-chat", "kimi-k3"],
                key="compatibility_suite",
            )
        engine_cases = client.list_cases(suite)
    except EngineError as error:
        st.error(f"Compatibility engine is unavailable: {error}")
        return

    profiles = [
        profile
        for profile in catalog.list_models(enabled=True)
        if suite in profile.suites
    ]
    case_labels = {
        f"{case['id']} · {case['name']} · {case['dimension']}": case["id"]
        for case in engine_cases
    }

    with st.form(f"{kind}_plan_form"):
        if kind == "compatibility":
            profile_labels = {
                f"{profile.display_name} · {profile.model}": profile for profile in profiles
            }
            selected_profile_labels = st.multiselect(
                "Models",
                list(profile_labels),
                default=list(profile_labels)[:1],
                help="Models are executed sequentially against the same gateway and case selection.",
            )
            selected_models = [profile_labels[label] for label in selected_profile_labels]
        else:
            seedance_model = st.text_input(
                "Seedance model",
                value="",
                placeholder="Leave empty to use the engine default",
            )
            selected_models = []

        connection_columns = st.columns([1.45, 1, .7])
        base_url = connection_columns[0].text_input(
            "Gateway base URL",
            placeholder="https://gateway.example",
            help="Use the API origin/base path; each case appends its own request path.",
        )
        api_key = connection_columns[1].text_input(
            "API key",
            type="password",
            help="Passed to the engine through an environment variable and never persisted.",
        )
        dry_run = connection_columns[2].checkbox("Dry run", value=True)

        selection_columns = st.columns([1, 2])
        selection_mode = selection_columns[0].selectbox(
            "Case selection",
            ["Default cases", "Selected cases", "All cases"],
        )
        selected_case_labels = selection_columns[1].multiselect(
            "Cases",
            list(case_labels),
            disabled=selection_mode != "Selected cases",
        )

        options_columns = st.columns(3)
        concurrency = options_columns[0].number_input(
            "Case concurrency",
            min_value=1,
            max_value=32,
            value=1,
            disabled=kind == "seedance",
        )
        timeout = options_columns[1].number_input(
            "Per-case timeout (s)", min_value=1, max_value=3600, value=600, step=30
        )
        no_wait = options_columns[2].checkbox(
            "Do not wait for completion",
            value=False,
            disabled=kind != "seedance",
        )
        confirm_paid = False
        all_models = False
        if kind == "seedance":
            safety_columns = st.columns(2)
            all_models = safety_columns[0].checkbox("Run all configured Seedance models")
            confirm_paid = safety_columns[1].checkbox(
                "I confirm this may create paid video tasks",
                help="Required for multi-task live Seedance plans.",
            )

        submitted = st.form_submit_button(
            "Preview plan" if dry_run else f"Run {kind} test",
            type="primary",
            width="stretch",
            icon=":material/play_arrow:",
        )

    case_ids = [case_labels[label] for label in selected_case_labels]
    if submitted:
        if not base_url.strip():
            st.error("Enter a gateway base URL.")
            return
        if not dry_run and not api_key:
            st.error("Enter an API key for a live run.")
            return
        if kind == "compatibility" and not selected_models:
            st.error("Select at least one model.")
            return
        if selection_mode == "Selected cases" and not case_ids:
            st.error("Select at least one case.")
            return

        run_models = selected_models if kind == "compatibility" else [None]
        completed_run_ids = []
        for model_index, profile in enumerate(run_models, start=1):
            model_name = profile.model if profile is not None else seedance_model.strip()
            model_label = profile.display_name if profile is not None else (model_name or "Seedance default")
            status = st.status(
                f"Running {suite} for {model_label} ({model_index}/{len(run_models)})…",
                expanded=True,
            )
            progress_box = st.empty()
            artifact_name = (
                f"{datetime.now().strftime('%Y%m%d-%H%M%S-%f')}-{suite}-{model_name or 'default'}"
            )
            output_dir = PROJECT_DIR / "data" / "artifacts" / artifact_name
            events = []
            try:
                for event in client.stream_run(
                    suite=suite,
                    base_url=base_url.strip(),
                    model=model_name,
                    api_key=api_key or None,
                    case_ids=case_ids,
                    all_cases=selection_mode == "All cases",
                    all_models=all_models,
                    dry_run=dry_run,
                    confirm_paid_suite=confirm_paid,
                    no_wait=no_wait,
                    output=output_dir,
                    timeout=int(timeout),
                    concurrency=1 if kind == "seedance" else int(concurrency),
                ):
                    events.append(event)
                    if event["type"] == "plan":
                        progress_box.progress(0.0, text=f"0/{event['total']} cases completed")
                    elif event["type"] == "progress":
                        progress_box.progress(
                            event["completed"] / max(event["total"], 1),
                            text=(
                                f"{event['completed']}/{event['total']} · "
                                f"{event['result']['id']} · {event['result']['status']}"
                            ),
                        )
                final = client.final_event(events)
                audit_run_id = save_audit_run(
                    suite,
                    base_url.strip(),
                    model_name,
                    events,
                )
            except (EngineError, ValueError) as error:
                status.update(label=f"{model_label} failed", state="error", expanded=True)
                st.error(str(error))
                continue

            completed_run_ids.append(audit_run_id)
            summary = final.get("summary", {})
            state = "error" if summary.get("fail", 0) else "complete"
            status.update(
                label=f"Audit #{audit_run_id} · {final.get('verdict', 'Complete')}",
                state=state,
                expanded=False,
            )
            summary_columns = st.columns(4)
            for column, key, label in zip(
                summary_columns,
                ["pass", "warning", "fail", "unknown"],
                ["Passed", "Warnings", "Failed", "Unknown"],
            ):
                column.metric(label, summary.get(key, 0))
            report_html = Path(str(final.get("report_html", "")))
            report_json = Path(str(final.get("report_json", "")))
            downloads = st.columns(2)
            if report_html.is_file():
                downloads[0].download_button(
                    "Download audit HTML",
                    report_html.read_bytes(),
                    file_name=f"audit-{audit_run_id}.html",
                    mime="text/html",
                    width="stretch",
                )
            if report_json.is_file():
                downloads[1].download_button(
                    "Download audit JSON",
                    report_json.read_bytes(),
                    file_name=f"audit-{audit_run_id}.json",
                    mime="application/json",
                    width="stretch",
                )
        if completed_run_ids:
            st.session_state.last_audit_run_ids = completed_run_ids

    st.caption(
        f"{len(engine_cases)} engine-supported {suite} cases · "
        f"{sum(bool(case.get('default')) for case in engine_cases)} selected by default"
    )
    st.dataframe(
        [
            {
                "ID": case["id"],
                "Name": case["name"],
                "Dimension": case["dimension"],
                "Kind": case["kind"],
                "Default": bool(case.get("default")),
                "Severity": case.get("severity", "normal"),
            }
            for case in engine_cases
        ],
        width="stretch",
        hide_index=True,
        height=360,
    )


def render_plans_page() -> None:
    page_heading(
        "Test Plans",
        "Configure repeatable compatibility, Seedance, and performance test runs without storing credentials.",
    )
    performance_tab, compatibility_tab, seedance_tab = st.tabs(
        ["Performance", "Compatibility", "Seedance"]
    )
    with performance_tab:
        _render_performance_plan()
    with compatibility_tab:
        _render_engine_plan("compatibility")
    with seedance_tab:
        _render_engine_plan("seedance")
