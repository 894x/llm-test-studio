# LLM Test Studio visual review checklist

## Before implementation

- Read `doc/design/desktop-ui-system.md`, `apps/desktop/frontend/src/index.css`, and the nearby component/tests.
- State the target route, primary task, data source, expected record volume, and required states.
- Identify which existing shadcn/Radix primitive owns every control.
- Audit dropdown-like controls for raw `<select>`, HTML `datalist`, and hand-built listbox/menu behavior before accepting the implementation.
- Identify the root, table/list, and inspector scroll owners.
- Decide whether the validation surface is Vite fixture mode or Wails production data.

## During implementation

- Consume semantic tokens rather than adding literal colors or JSX theme branches.
- Keep primary, secondary, semantic, selected, disabled, and loading emphasis distinct.
- Check label/value alignment, default text wrapping, numeric alignment, and long IDs. Inject long unbroken IDs, URLs, names, and errors; ensure inspector width is unchanged and all text remains readable.
- Check inspector rows remain borderless and two-line: label first, value second.
- Check all tables and their wrappers are borderless, including sticky columns and report/request tables. Verify hover and selected row backgrounds have 6 px end corners, joined interior cells, no rectangular row background behind the corners, and no geometry shift. Selection must remain visible on hover.
- Scroll long tables vertically and horizontally, with a selected row passing behind the header. Headers must keep their default rounded surface above a full opaque backing, without text/background bleed through corners or gaps; column widths and row heights must stay stable.
- Check icon-only actions have accessible names, Tooltips where needed, and visible focus.
- Check the 28 px circular theme trigger, three radio choices, focus restoration, persistence, and system-theme updates.
- Check every overlay uses the resolved theme and has an accessible title and close path.
- Check Select, Combobox, Autocomplete, and menu popups match their trigger width where appropriate, use semantic popover tokens, stay inside the viewport, and expose keyboard-reachable highlighted/selected/empty states.
- Check running tasks through queued, sending, draining, completed, failed, cancelled, stop-sending, and cancellation behavior where applicable.
- Force vertical and horizontal overflow and confirm 5 px rails with 4 px inset do not change container geometry.
- Remove nested cards, decorative accent usage, duplicate explanatory text, fake controls, and presentation-side business inference.

## Rendered acceptance

Verify at minimum:

1. `1440 x 900` desktop.
2. `1024 x 768` compact desktop.
3. `960 x 640` minimum Wails window.
4. Light, dark, and system preference resolution.
5. Default, hover, focus-visible, pressed, selected, disabled, loading, empty, partial, warning, and error examples that apply.
6. Long Chinese and Latin labels, UUIDs, paths, validation text, and large counts.
7. Page identity, meaningful nonblank content, no framework error overlay, and no relevant console errors.
8. At least one real interaction with a visible state/URL/focus result.
9. Wails-backed catalog/run/report data for production claims; never use fixture behavior as production evidence.

For a browser-preview task, use the available Browser integration and collect a DOM/state check, console health, interaction proof, and screenshot. For a Wails-only lifecycle or binding claim, verify the native application plus its Go tests and production SQLite path.

## Commands

Run from `apps/desktop/frontend`:

```powershell
pnpm lint
pnpm test -- --run
pnpm build
```

When Wails DTOs, bindings, public error codes, lifecycle, or production composition change, also run from the repository root:

```powershell
go test ./apps/desktop
go vet ./apps/desktop
go build -trimpath -o NUL ./apps/desktop
```

Run `git diff --check` and confirm generated `dist`, `wailsjs`, output, databases, and temporary screenshots are not accidentally staged.

## Acceptance questions

- Does this look like a compact local testing tool instead of a generic dashboard?
- Is one primary task obvious?
- Are surfaces separated with the least framing necessary?
- Are accent and semantic colors used only for meaning?
- Are density, radii, borders, typography, and icons consistent with the existing shell?
- Are keyboard, focus, scroll, and error paths usable?
- Is every displayed business fact authoritative from Go rather than inferred in React?
- Would the next feature author know which local pattern to reuse?

Do not approve the interface while a visible or behavioral mismatch remains fixable.
