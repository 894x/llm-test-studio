"""Model profile management page."""

from __future__ import annotations

from pathlib import Path

import pandas as pd
import streamlit as st

from llm_studio.catalog import Catalog, CatalogError, ModelProfile
from llm_studio.ui_theme import page_heading, resource_summary, section_title


PROJECT_DIR = Path(__file__).resolve().parents[2]


def _blank_model() -> dict:
    return {
        "schema_version": 1,
        "id": "",
        "display_name": "",
        "model": "",
        "protocol": "openai-chat",
        "endpoint": "",
        "suites": ["openai-chat"],
        "capabilities": ["streaming", "tools", "reasoning"],
        "enabled": True,
    }


@st.dialog("Model profile", width="large", on_dismiss="rerun")
def _model_editor(
    catalog: Catalog,
    available_suites: list[str],
    source: dict,
    *,
    is_new: bool,
) -> None:
    st.caption(
        "Create a reusable model definition. Credentials are supplied only when a test starts."
        if is_new
        else f"Editing {source['display_name']} · {source['id']}"
    )
    with st.form("model_editor_form", clear_on_submit=False):
        identity_columns = st.columns(2)
        display_name = identity_columns[0].text_input(
            "Display name", value=source["display_name"], placeholder="GLM 5.3"
        )
        profile_id = identity_columns[1].text_input(
            "Profile ID",
            value=source["id"],
            disabled=not is_new,
            placeholder="glm-5.3",
        )

        model_columns = st.columns(2)
        model_id = model_columns[0].text_input(
            "Model ID", value=source["model"], placeholder="glm-5.3"
        )
        protocol = model_columns[1].selectbox(
            "Protocol",
            available_suites or ["openai-chat"],
            index=(
                available_suites.index(source["protocol"])
                if source["protocol"] in available_suites
                else 0
            ),
        )
        endpoint = st.text_input(
            "Endpoint reference",
            value=source["endpoint"],
            placeholder="https://gateway.example/v1",
            help="Optional base URL or endpoint reference. API keys are never stored.",
        )
        suites = st.multiselect(
            "Assigned test suites",
            available_suites,
            default=[suite for suite in source["suites"] if suite in available_suites],
        )

        st.markdown("#### Capabilities")
        capabilities = set(source.get("capabilities", []))
        capability_columns = st.columns(4)
        streaming = capability_columns[0].checkbox(
            "Streaming", value="streaming" in capabilities
        )
        vision = capability_columns[1].checkbox("Vision", value="vision" in capabilities)
        tools = capability_columns[2].checkbox("Tools", value="tools" in capabilities)
        reasoning = capability_columns[3].checkbox(
            "Reasoning", value="reasoning" in capabilities
        )
        enabled = st.checkbox("Enabled for new test plans", value=source.get("enabled", True))

        action_columns = st.columns([1, 1, 2.2])
        save = action_columns[0].form_submit_button(
            "Save model", type="primary", width="stretch"
        )
        cancel = action_columns[1].form_submit_button("Cancel", width="stretch")

    if cancel:
        st.rerun()
    if not save:
        return

    try:
        catalog.save_model(
            ModelProfile.from_dict(
                {
                    "schema_version": 1,
                    "id": profile_id.strip(),
                    "display_name": display_name.strip(),
                    "model": model_id.strip(),
                    "protocol": protocol,
                    "endpoint": endpoint.strip(),
                    "suites": suites,
                    "capabilities": [
                        capability
                        for capability, selected in [
                            ("streaming", streaming),
                            ("vision", vision),
                            ("tools", tools),
                            ("reasoning", reasoning),
                        ]
                        if selected
                    ],
                    "enabled": enabled,
                }
            )
        )
    except CatalogError as error:
        st.error(str(error))
        return

    st.toast("Model profile saved", icon=":material/check_circle:")
    st.rerun()


def render_models_page() -> None:
    page_heading(
        "Models",
        "Maintain reusable model identities, capabilities, and suite assignments. Select a row to inspect it; edit only when needed.",
    )
    catalog = Catalog(PROJECT_DIR)
    models = catalog.list_models()
    all_cases = catalog.list_cases()
    available_suites = sorted({case["protocol"] for case in all_cases})
    assigned_counts = {
        model.id: sum(case["protocol"] in model.suites for case in all_cases)
        for model in models
    }

    resource_summary(
        [
            ("Profiles", str(len(models))),
            ("Enabled", str(sum(model.enabled for model in models))),
            ("Protocols", str(len({model.protocol for model in models}))),
            ("Configured endpoints", str(sum(bool(model.endpoint) for model in models))),
        ]
    )

    with st.container(key="model_toolbar", border=True):
        toolbar = st.columns([2.4, 1, 1, .85])
        search = toolbar[0].text_input(
            "Search models",
            placeholder="Name, model ID, or profile ID…",
            label_visibility="collapsed",
        )
        protocol_filter = toolbar[1].selectbox(
            "Protocol",
            ["All protocols", *sorted({model.protocol for model in models})],
            label_visibility="collapsed",
        )
        status_filter = toolbar[2].selectbox(
            "Status",
            ["All statuses", "Enabled", "Disabled"],
            label_visibility="collapsed",
        )
        if toolbar[3].button(
            "Add model", type="primary", width="stretch", icon=":material/add:"
        ):
            _model_editor(catalog, available_suites, _blank_model(), is_new=True)

    search_term = search.strip().lower()
    filtered_models = [
        model
        for model in models
        if (
            not search_term
            or search_term in model.display_name.lower()
            or search_term in model.model.lower()
            or search_term in model.id.lower()
        )
        and (protocol_filter == "All protocols" or model.protocol == protocol_filter)
        and (
            status_filter == "All statuses"
            or model.enabled == (status_filter == "Enabled")
        )
    ]

    if not filtered_models:
        st.info("No model profiles match these filters.")
        return

    model_frame = pd.DataFrame(
        [
            {
                "Model": model.display_name,
                "Model ID": model.model,
                "Protocol": model.protocol,
                "Test cases": assigned_counts[model.id],
                "Capabilities": " · ".join(model.capabilities) or "Not declared",
                "Endpoint": "Configured" if model.endpoint else "Not configured",
                "Status": "Enabled" if model.enabled else "Disabled",
            }
            for model in filtered_models
        ]
    )
    event = st.dataframe(
        model_frame,
        width="stretch",
        hide_index=True,
        height=min(380, 44 + len(filtered_models) * 40),
        row_height=40,
        on_select="rerun",
        selection_mode="single-row",
        key="model_catalog_table_v2",
        column_config={
            "Model": st.column_config.TextColumn(width="medium"),
            "Model ID": st.column_config.TextColumn(width="medium"),
            "Protocol": st.column_config.TextColumn(width="small"),
            "Test cases": st.column_config.NumberColumn(width="small"),
            "Capabilities": st.column_config.TextColumn(width="large"),
            "Endpoint": st.column_config.TextColumn(width="small"),
            "Status": st.column_config.TextColumn(width="small"),
        },
    )
    st.caption(
        f"{len(filtered_models)} of {len(models)} profiles · Select one row to inspect or edit it."
    )

    selected_rows = event.selection.rows
    if not selected_rows or selected_rows[0] >= len(filtered_models):
        return

    selected_model = filtered_models[selected_rows[0]]
    with st.container(key="model_selection_bar", border=True):
        details, edit_column = st.columns([5, 1])
        details.markdown(
            f"**{selected_model.display_name}**  \n"
            f"`{selected_model.model}` · {selected_model.protocol} · "
            f"{assigned_counts[selected_model.id]} assigned cases · "
            f"{'Enabled' if selected_model.enabled else 'Disabled'}"
        )
        if edit_column.button(
            "Edit model", type="primary", width="stretch", icon=":material/edit:"
        ):
            _model_editor(
                catalog,
                available_suites,
                selected_model.to_dict(),
                is_new=False,
            )

    with st.expander(
        f"Assigned cases · {assigned_counts[selected_model.id]}", expanded=False
    ):
        assigned_cases = [
            case for case in all_cases if case["protocol"] in selected_model.suites
        ]
        section_title("Suite coverage")
        st.dataframe(
            pd.DataFrame(
                [
                    {
                        "ID": case["id"],
                        "Name": case["name"],
                        "Protocol": case["protocol"],
                        "Dimension": case["dimension"],
                        "Severity": case.get("severity", "normal"),
                    }
                    for case in assigned_cases
                ]
            ),
            width="stretch",
            hide_index=True,
            height=min(420, 44 + len(assigned_cases) * 35),
        )
