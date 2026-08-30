"""Compatibility case browser and editor."""

from __future__ import annotations

import json
from pathlib import Path

import pandas as pd
import streamlit as st

from llm_studio.catalog import Catalog, CatalogError
from llm_studio.ui_theme import page_heading, resource_summary


PROJECT_DIR = Path(__file__).resolve().parents[2]


def _blank_case(protocol: str) -> dict:
    return {
        "id": "",
        "name": "",
        "dimension": "compatibility",
        "protocol": protocol,
        "kind": "chat_sync" if protocol != "seedance" else "seedance_task",
        "default": False,
        "disabled": False,
        "severity": "normal",
        "request": {"method": "POST", "path": "/v1/chat/completions", "body": {}},
        "options": {},
    }


@st.dialog("Test case", width="large", on_dismiss="rerun")
def _case_editor(
    catalog: Catalog,
    protocols: list[str],
    source: dict,
    *,
    is_new: bool,
) -> None:
    st.caption(
        "Create a versioned, protocol-specific definition. JSON is validated before saving."
        if is_new
        else f"Editing {source['id']} · {source['protocol']}"
    )
    with st.form("case_editor_form", clear_on_submit=False):
        identity_columns = st.columns([1, 1.5])
        case_id = identity_columns[0].text_input(
            "Case ID", value=source["id"], disabled=not is_new, placeholder="T999"
        )
        name = identity_columns[1].text_input("Name", value=source["name"])

        metadata_columns = st.columns(4)
        protocol = metadata_columns[0].selectbox(
            "Protocol",
            protocols,
            index=protocols.index(source["protocol"]) if source["protocol"] in protocols else 0,
            disabled=not is_new,
        )
        dimension = metadata_columns[1].text_input("Dimension", value=source["dimension"])
        severity = metadata_columns[2].selectbox(
            "Severity",
            ["normal", "critical"],
            index=1 if source.get("severity") == "critical" else 0,
        )
        kind = metadata_columns[3].text_input("Runner kind", value=source["kind"])

        flag_columns = st.columns(4)
        default = flag_columns[0].checkbox(
            "Selected by default", value=bool(source.get("default"))
        )
        disabled = flag_columns[1].checkbox(
            "Disable this case", value=bool(source.get("disabled"))
        )

        json_columns = st.columns([1.55, 1])
        request_json = json_columns[0].text_area(
            "Request definition",
            value=json.dumps(source["request"], ensure_ascii=False, indent=2),
            height=310,
        )
        options_json = json_columns[1].text_area(
            "Runner options",
            value=json.dumps(source.get("options", {}), ensure_ascii=False, indent=2),
            height=310,
        )

        relative_path = ""
        if is_new:
            relative_path = st.text_input(
                "Case file",
                placeholder=f"{protocol}/T999-new-case/case.json",
                help="Path relative to the cases directory.",
            )

        action_columns = st.columns([1, 1, 2.2])
        save = action_columns[0].form_submit_button(
            "Save case", type="primary", width="stretch"
        )
        cancel = action_columns[1].form_submit_button("Cancel", width="stretch")

    if cancel:
        st.rerun()
    if not save:
        return

    try:
        payload = {
            "id": case_id.strip(),
            "name": name.strip(),
            "dimension": dimension.strip(),
            "protocol": protocol,
            "kind": kind.strip(),
            "default": default,
            "disabled": disabled,
            "severity": severity,
            "request": json.loads(request_json),
            "options": json.loads(options_json),
        }
        catalog.save_case(payload, path=relative_path or None)
    except (CatalogError, json.JSONDecodeError) as error:
        st.error(str(error))
        return

    st.toast("Test case saved", icon=":material/check_circle:")
    st.rerun()


def render_cases_page() -> None:
    page_heading(
        "Test Cases",
        "Browse versioned protocol definitions at full width. Select a case to inspect its request, then open the editor only when needed.",
    )
    catalog = Catalog(PROJECT_DIR)
    all_cases = catalog.list_cases()
    protocols = sorted({case["protocol"] for case in all_cases})
    dimensions = sorted({case["dimension"] for case in all_cases})
    severities = sorted({case.get("severity", "normal") for case in all_cases})

    resource_summary(
        [
            ("Definitions", str(len(all_cases))),
            ("Protocols", str(len(protocols))),
            ("Default cases", str(sum(bool(case.get("default")) for case in all_cases))),
            ("Dimensions", str(len(dimensions))),
        ]
    )

    with st.container(key="case_toolbar", border=True):
        filters = st.columns([2.2, 1, 1, 1, .75])
        search = filters[0].text_input(
            "Search cases",
            placeholder="ID, name, kind, or dimension…",
            label_visibility="collapsed",
        )
        protocol_filter = filters[1].selectbox(
            "Protocol", ["All protocols", *protocols], label_visibility="collapsed"
        )
        dimension_filter = filters[2].selectbox(
            "Dimension", ["All dimensions", *dimensions], label_visibility="collapsed"
        )
        severity_filter = filters[3].selectbox(
            "Severity", ["All severities", *severities], label_visibility="collapsed"
        )
        if filters[4].button(
            "Add case", type="primary", width="stretch", icon=":material/add:"
        ):
            _case_editor(
                catalog,
                protocols,
                _blank_case(protocols[0] if protocols else "openai-chat"),
                is_new=True,
            )

    filtered = catalog.list_cases(
        protocol=None if protocol_filter == "All protocols" else protocol_filter,
        dimension=None if dimension_filter == "All dimensions" else dimension_filter,
        search=search or None,
    )
    if severity_filter != "All severities":
        filtered = [
            case for case in filtered if case.get("severity", "normal") == severity_filter
        ]

    if not filtered:
        st.info("No test cases match these filters.")
        return

    case_frame = pd.DataFrame(
        [
            {
                "ID": case["id"],
                "Name": case["name"],
                "Protocol": case["protocol"],
                "Dimension": case["dimension"],
                "Runner": case["kind"],
                "Severity": case.get("severity", "normal"),
                "Default": bool(case.get("default")),
                "Status": "Disabled" if case.get("disabled") else "Active",
            }
            for case in filtered
        ]
    )
    event = st.dataframe(
        case_frame,
        width="stretch",
        hide_index=True,
        height=500,
        row_height=40,
        on_select="rerun",
        selection_mode="single-row",
        key="case_catalog_table_v2",
        column_config={
            "ID": st.column_config.TextColumn(width="medium"),
            "Name": st.column_config.TextColumn(width="large"),
            "Protocol": st.column_config.TextColumn(width="small"),
            "Dimension": st.column_config.TextColumn(width="small"),
            "Runner": st.column_config.TextColumn(width="medium"),
            "Severity": st.column_config.TextColumn(width="small"),
            "Default": st.column_config.CheckboxColumn(width="small"),
            "Status": st.column_config.TextColumn(width="small"),
        },
    )
    st.caption(f"{len(filtered)} of {len(all_cases)} definitions · Select one row to inspect it.")

    selected_rows = event.selection.rows
    if not selected_rows or selected_rows[0] >= len(filtered):
        return

    selected = filtered[selected_rows[0]]
    with st.container(key="case_selection_bar", border=True):
        details, edit_column = st.columns([5, 1])
        details.markdown(
            f"**{selected['name']}**  \n"
            f"`{selected['id']}` · {selected['protocol']} · {selected['dimension']} · "
            f"{selected['kind']} · {selected.get('severity', 'normal')}"
        )
        if edit_column.button(
            "Edit case", type="primary", width="stretch", icon=":material/edit:"
        ):
            _case_editor(catalog, protocols, selected, is_new=False)

    with st.expander("Request & runner options", expanded=False):
        request_column, options_column = st.columns([1.55, 1])
        request_column.code(
            json.dumps(selected["request"], ensure_ascii=False, indent=2),
            language="json",
        )
        options_column.code(
            json.dumps(selected.get("options", {}), ensure_ascii=False, indent=2),
            language="json",
        )
