# LLM Test Studio component patterns

## Component choice

Prefer existing project components and shadcn/ui composition:

| Need | Preferred implementation |
| --- | --- |
| Primary destinations | Existing shell navigation; selected item changes surface and text |
| Theme preference | `DropdownMenu` radio group plus 28 px circular ghost `Button` |
| Comparable records | `Table` inside one bounded `ScrollArea` |
| Status | Semantic `Badge`; put detail in Tooltip or inspector |
| Search | Existing input-group primitives, with visible label or accessible name |
| Bounded choices | `Select` with grouped, keyboard-reachable items |
| Filterable bounded choices | `Combobox`; the committed value must resolve to an available item |
| Suggestions plus free text | Shared `Autocomplete`; suggestions may complete the input but must not reject an arbitrary value |
| Create/edit workflow | `Sheet` or dialog with an accessible title and description |
| Forms | `FieldGroup` and `Field`; pair invalid styling with `aria-invalid` |
| Measurable work | `Progress`; use `Spinner` or `Skeleton` when progress is unknown |
| Persistent problem | `Alert`; use a toast only for brief operation feedback |
| Empty data | `Empty`; distinguish initial empty from filtered empty |
| Separation | `Separator`, spacing, or a subtle surface shift |
| Contextual details | Borderless description list or inspector |

Do not replace established primitives with raw buttons, inputs, selects, radio groups, dialogs, or hand-built accessibility behavior.

Do not use HTML `datalist` for suggestions in the desktop product. Its browser-owned popup bypasses semantic theme tokens and cannot reliably match the application's density, focus, selected, empty, or overlay states. Use the shared `Autocomplete` primitive, keep its popup aligned to the input width, and render options inside its bounded list.

## Actions and forms

- Use one primary action per local decision group.
- Use secondary/default actions for reversible alternatives and destructive styling only for genuinely destructive commands.
- Show loading on the initiating control, keep its geometry stable, and block duplicate submission.
- Put labels above fields in inspectors and narrow forms.
- Keep helper text only when it changes a decision.
- Place validation next to the affected field and preserve entered values after failure.
- Implement applicable read-only, disabled, loading, success, warning, and error states.

## Navigation and selection

- Use global navigation for product destinations and tabs for peer views that share one workspace.
- Use a segmented or radio control only for a small closed set of peer modes.
- Use menus or selects for numerous or secondary options.
- Use `Combobox` only when filtering helps but the value is still restricted to the supplied items. Use `Autocomplete` when suggestions are optional and users may keep free-form text.
- Make selection visible through both surface and text/icon treatment, not color alone.
- Preserve URL/hash navigation, focus, and keyboard activation for primary destinations.
- Do not leave enabled-looking controls without an action.

## Shell and workspaces

- Compose the application from fixed Header chrome plus a `min-height: 0` content root.
- Give the primary table/list the largest region. Use an optional inspector only when selection details aid the current decision.
- Keep the inspector narrow and stable; hide or recompose it at the compact breakpoint rather than crushing the main table.
- Keep each bounded region responsible for its own `ScrollArea`. Do not nest independent vertical scroll owners without a deliberate interaction reason.
- Avoid permanent cards for page chrome. Use open layout, borders, dividers, and restrained surface changes.

## Tables, lists, and inspectors

- Use tables for comparable records with stable columns and lists for heterogeneous records or prominent row actions.
- Give a standalone dense table one rounded border/frame and clip its header and rows to that frame.
- Keep the header sticky only inside the table's single bounded scroll owner.
- Align numeric columns, use tabular numerals, and keep IDs scan-friendly.
- Support loading, initial empty, filtered empty, partial, error, selected, and large-data states.
- Use `content-visibility` or a proven bounded strategy for long lists; do not render unbounded evidence payloads into the DOM.
- Render inspector properties as `dl` rows: quiet `dt` label, then one `dd` value, without grid lines or boxed cells.

## Feedback, overlays, and async tasks

- Use a dialog for blocking decisions and a sheet/inspector for contextual editing that should preserve workspace context.
- Keep every overlay titled, closeable, keyboard reachable, theme synchronized, and focus restoring.
- Keep a long-running task's start action, stage, progress, sent/in-flight/completed counts, stop-sending command, cancel command, error, and recovery in one stable surface.
- Distinguish queued, sending, draining, completed, failed, and cancelled without changing task geometry.
- Preserve useful logs and evidence after failure. Put long JSON or traces in bounded monospace regions.

## Wails and React boundary

- Parse Wails results into closed, allow-listed TypeScript DTOs before storing them in React state.
- Keep schema versions, IDs, revisions, protocols, execution policies, run states, conclusions, and counts authoritative from Go.
- Map stable public error codes to user messages. Never display raw Go/provider errors.
- Keep secrets and unrestricted payloads outside UI DTOs and fixtures.
- When a command succeeds, render the authoritative snapshot returned or re-read from Core rather than predicting the new state.
- When testing `localhost:5173`, state that the surface uses development fixtures. Validate production data through Wails bindings and SQLite-backed services.

## Required state matrix

For each interactive component, deliberately decide whether these states apply:

| State | Required evidence |
| --- | --- |
| Default | Clear purpose and hierarchy |
| Hover | Surface, border, icon, or text response |
| Focus-visible | Visible keyboard focus ring |
| Active/selected | Persistent non-color-only distinction |
| Disabled | Blocked interaction and reduced emphasis |
| Loading | Stable geometry and duplicate prevention |
| Empty | Helpful distinction between no data and no matches |
| Error | Semantic treatment plus actionable text |
