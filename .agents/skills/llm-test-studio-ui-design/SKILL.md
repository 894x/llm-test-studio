---
name: llm-test-studio-ui-design
description: Apply and review LLM Test Studio's compact town-derived desktop product-interface language in the React, Tailwind CSS, shadcn/ui, Radix or Base UI, Lucide, and Wails application. Use when creating, changing, refactoring, or visually testing desktop pages, navigation, tables, forms, inspectors, dialogs, long-running task surfaces, themes, responsive layouts, empty/error states, or shared UI primitives under apps/desktop/frontend, and when checking whether an implementation matches the project's established visual, accessibility, data-boundary, and browser-acceptance conventions.
---

# LLM Test Studio UI Design

Apply the stable tool-oriented visual language adopted from `town-ui-design` without copying Animetown components or framework choices. Treat this skill as design and review policy; keep React components and CSS in the application as the executable implementation.

## Workflow

1. Identify the target route, primary user task, data authority, expected record volume, and required states.
2. Read `doc/design/desktop-ui-system.md` and inspect the nearby implementation before proposing a visual direction.
3. Read [design-tokens.md](references/design-tokens.md) before changing colors, typography, spacing, radii, elevation, theme behavior, or control dimensions.
4. Read [component-patterns.md](references/component-patterns.md) before creating or changing components, workspaces, tables, forms, navigation, inspectors, overlays, or asynchronous task states.
5. Read [responsive-layout.md](references/responsive-layout.md) before changing scroll ownership, window composition, compact-desktop behavior, minimum dimensions, or any narrow-screen layout.
6. Reuse local shadcn/ui components, Radix-backed primitives, Lucide icons, semantic CSS variables, and established shell/layout components.
7. Define and implement every applicable default, hover, focus-visible, active, selected, disabled, loading, empty, partial, success, warning, and error state.
8. Keep Go Application Core authoritative for validation, lifecycle, persistence, credentials, execution, and reports. Keep React limited to interaction and presentation state.
9. Run the relevant checks and follow [visual-review-checklist.md](references/visual-review-checklist.md) before declaring the surface complete.

## Core direction

- Build a compact local testing workspace, not a marketing page, generic dashboard, or card grid.
- Keep the current task visually dominant. Prefer stable navigation, tables, lists, toolbars, inspectors, sheets, and explicit scroll regions.
- Use the neutral blue-gray accent only for primary actions, focus, selection, and meaningful emphasis. Use semantic colors only for genuine status meaning.
- Keep light and dark themes equally supported. Theme preference is `system`, `light`, or `dark`; resolved theme is only `light` or `dark`.
- Create hierarchy in this order: alignment and spacing, typography, subtle surface differences, local dividers, then necessary borders. Ordinary content containers are borderless by default. Do not add gradients, saturated decorative panels, or nested framed cards.
- Preserve compact density: 28 px icon controls, 32 px ordinary controls, 6 px control radius, 8 px panel radius, and 16 to 18 px icons unless an existing primitive requires otherwise.
- Render inspector properties as an open, borderless, single-column description list with the quiet label first and value second.
- All tables are borderless: no outer frame, header/footer rule, row separator, or column grid. Headers have a default quiet 6 px rounded surface above an opaque backing. Body rows use a continuous 6 px rounded background only on hover or selection; selected state takes precedence. Keep keyboard focus visible.
- Let layout determine column widths; content must adapt. Text wraps by default, including unbroken IDs and URLs (`overflow-wrap: anywhere`). Never let content widen an inspector or create page-level horizontal overflow. Truncation and local horizontal scrolling require a specific content need; they are not the default overflow response.
- Use the shared overlay `ScrollArea`: 5 px rail and thumb, 4 px edge inset, no layout-consuming gutter, and explicit ownership for each bounded region.
- Keep icon-only controls square or circular as established, with an accessible name, Tooltip when meaning is not universal, and visible focus.
- Preserve stable geometry through loading, error, and selection changes.

## Component and styling boundary

- Prefer existing components in `apps/desktop/frontend/src/components/ui` before adding variants or raw controls.
- Use shadcn/ui and its established Radix- or Base UI-backed components for controls, menus, dialogs, sheets, tables, feedback, and form composition.
- Use the shared `SearchableSelect` (Combobox) for all closed dropdown choices, including short option lists, and a shared `Autocomplete` for suggestions that still allow arbitrary text. Never use HTML `datalist` on product surfaces because its browser-owned popup cannot follow the application's theme, geometry, or interaction states.
- Use Lucide icons through the project's direct icon imports. Do not introduce another icon family for ordinary interface actions.
- Use Tailwind utilities for layout, dimensions, and composition. Consume semantic variables and component variants for color and state styling.
- Do not put raw theme colors, status colors, or handwritten `dark:` color overrides in JSX.
- Do not introduce Naive UI, Animetown runtime packages, Animetown component source, or a shared cross-repository UI package through ordinary feature work.

## Data and security boundary

- Treat Wails snapshots and command results as versioned allow-listed DTOs. Reject unknown or malformed boundary data rather than passing it into React state.
- Never render credentials, provider secrets, raw internal errors, unrestricted request/response payloads, or filesystem details.
- Do not infer business conclusions, run status, counts, or report verdicts in React when Go Core owns them.
- Treat Vite fixtures only as browser-development samples. Never present fixture counts or behavior as evidence that Wails production SQLite data is correct.
- Preserve Model, Channel, Test Case, Suite, Plan, Run, Result, Evidence, and Report as distinct product concepts.

## Sources of truth

Use these files in order of relevance:

- `doc/design/desktop-ui-system.md`: normative project design and interaction baseline.
- `apps/desktop/frontend/src/index.css`: executable semantic tokens and global behavior.
- `apps/desktop/frontend/src/app/theme.tsx`: theme preference, persistence, and resolution.
- `apps/desktop/frontend/src/components/ui`: local shadcn/Radix primitive implementations.
- `apps/desktop/frontend/src/features/shell`: application shell, navigation, page frame, and inspector composition.
- Nearby feature components and tests: established local behavior that must remain compatible unless the task explicitly refactors it.

Keep the normative document and executable tokens synchronized when a task intentionally changes a stable value. Do not silently resolve a discrepancy by inventing a third value.

## Acceptance gate

- Validate `1440 x 900`, `1024 x 768`, and the application's `960 x 640` minimum window boundary.
- Confirm no page-level horizontal scrolling, clipped primary actions, overlapping fixed chrome, double scrollbar ownership, or layout shift when overflow appears.
- Verify light, dark, and system preference behavior, including first paint and system-theme changes.
- Verify keyboard operation, focus visibility/restoration, Escape/close behavior, accessible names, and meaningful ARIA state.
- Exercise real Wails data when validating production catalog, run, or report behavior; use Vite fixtures only for presentation development.
- Run frontend lint, tests, and build. Run the desktop Go tests whenever DTOs, bindings, error codes, lifecycle, or production composition change.

## Exclusions

- Do not make mobile support a current product claim. Add mobile-specific composition and acceptance only when the user explicitly expands the product scope.
- Do not use historical concept images as implementation specifications.
- Do not copy obsolete styles or add compatibility layers for superseded presentation.
- Do not create preview-only production routes or business logic solely to demonstrate a visual pattern.
