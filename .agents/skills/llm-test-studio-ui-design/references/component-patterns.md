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
| Bounded choices | `SearchableSelect` with searchable, keyboard-reachable items |
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
- Prefer readable option names. Include protocol, revision, or other metadata only when it helps distinguish choices or make a decision; keep stable identifiers in values rather than exposing them by default in labels.
- Group related short fields according to available container width; give long text and complex controls more space. Do not copy a fixed column count from another form. Avoid empty wrappers and overlapping legend/section spacing that leave unexplained gaps.
- Place validation next to the affected field and preserve entered values after failure.
- Implement applicable read-only, disabled, loading, success, warning, and error states.

## Navigation and selection

- Use global navigation for product destinations and tabs for peer views that share one workspace.
- Use a segmented or radio control only for a small closed set of peer modes.
- Use menus or selects for numerous or secondary options.
- Use `SearchableSelect` for closed dropdown choices, including short lists. Use `Autocomplete` when suggestions are optional and users may keep free-form text; this distinction does not replace tabs, checkboxes, or peer-mode controls.
- Make selection visible through both surface and text/icon treatment, not color alone.
- Preserve URL/hash navigation, focus, and keyboard activation for primary destinations.
- Do not leave enabled-looking controls without an action.

## Search and detail navigation

- Reuse the existing search input-group and filtering pattern. Distinguish initial empty from no matches, provide a clear/reset action, and keep result-count feedback from shifting controls. Counts of locally filtered rows describe the displayed collection; they must not replace Go-owned execution totals or conclusions.
- Preserve the query when returning from details. Keep selection and displayed details consistent with the visible collection, and handle a selected record disappearing after filtering or refresh.
- Choose row selection, explicit navigation, and contextual editing according to the workspace task. Make their effects distinguishable and keyboard reachable; do not make every table row open an editor merely because a matrix uses that interaction.

## Shell and workspaces

- Compose the application from fixed Header chrome plus a `min-height: 0` content root.
- Give the primary table/list the largest region. Use an optional inspector only when selection details aid the current decision.
- Keep the inspector narrow and stable; hide or recompose it at the compact breakpoint rather than crushing the main table.
- Keep each bounded region responsible for its own `ScrollArea`. Do not nest independent vertical scroll owners without a deliberate interaction reason.
- Avoid permanent cards for page chrome. Use open layout, alignment, spacing, and typography first; use a quiet divider only where a boundary is needed.

- Page content, standalone table scroll regions, navigation, and inspector content use 16 px horizontal insets. Assign the inset to one container; nested sections reuse it rather than adding repeated horizontal padding. Table cells keep 8 px internal padding.

## Tables, lists, and inspectors

- Model/channel matrices highlight both axes on cell hover or keyboard focus, with a deeper rounded intersection. Apply this to configured and unconfigured cells; keep selection distinguishable.
- When matrix cells are editing entry points, open the existing record or prefill a new record from the cell's axes. Reuse the catalog editor and its validation; respect pending/unavailable states and restore focus to the initiating cell on close. Empty cells must make the available action or unavailable state clear.

- Use tables for comparable records with stable columns and lists for heterogeneous records or prominent row actions.
- Render all tables without borders, including wrappers, headers, footers, rows, and columns. Header cells have a persistent quiet rounded surface; tables use zero border spacing to prevent initial sticky-header movement, and body rows use a continuous 6 px rounded background on hover or selection. Paint the background on cells and round only the first/last cell so internal columns remain joined; use separate borders with zero horizontal spacing. Selected backgrounds take precedence over hover. Preserve visible keyboard focus; do not wrap tables in cards.
- Use dot plus text for dense run statuses; avoid repeated outlined pills. Keep error, selection, and keyboard focus visible.
- Text wraps by default, including long IDs, names, URLs, and error messages. Use `min-width: 0` on flexible children and `overflow-wrap: anywhere` for unbroken values. Keep an explicit width constraint before opting into truncation.
- The shared TableHeader is sticky by default inside the table's single bounded scroll owner. Use an opaque rectangular background beneath the header cells to cover gaps and corner cutouts, extending 2 px below the header to separate partially occluded rows. Header cells have a distinct opaque `table-header` surface and 6 px first/last corners. Do not use translucent fills or backdrop blur for the visible header surface. Sticky columns also need an opaque backing beneath hover/selection fills.
- Align numeric columns, use tabular numerals, and keep IDs scan-friendly.
- Support loading, initial empty, filtered empty, partial, error, selected, and large-data states.
- Use a proven bounded strategy for long lists; do not render unbounded evidence payloads into the DOM. Do not use `content-visibility: auto` or estimated intrinsic sizes on native table rows: skipping cells can change column measurement and row heights during scrolling.
- Render inspector properties as `dl` rows: quiet `dt` label, then one `dd` value, without grid lines or boxed cells.

## Feedback, overlays, and async tasks

- Use a dialog for blocking decisions and a sheet/inspector for contextual editing that should preserve workspace context.
- Keep every overlay titled, closeable, keyboard reachable, theme synchronized, and focus restoring.
- Dropdowns inside sheets belong within the modal focus boundary but outside the scrolling form. Reuse the shared dropdown primitives and `apps/desktop/frontend/src/components/ui/use-floating-portal-container.ts`; retain their fixed positioning rather than switching coordinate systems or adding feature-specific animation workarounds.
- Selecting an option must not dismiss the editor. Escape closes the active popup before the enclosing sheet; verify focus return, scrolling while open, and closing/reopening the sheet when changing nested overlays.
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
| Focus-visible | Visible keyboard focus using the control's token/variant; input-like controls use the focus border, not an added outer ring |
| Active/selected | Persistent non-color-only distinction |
| Disabled | Blocked interaction and reduced emphasis |
| Loading | Stable geometry and duplicate prevention |
| Empty | Helpful distinction between no data and no matches |
| Error | Semantic treatment plus actionable text |
