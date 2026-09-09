# Responsive desktop layout

LLM Test Studio is currently a cross-platform desktop product with a minimum Wails window of `960 x 640`. Treat responsive work as desktop and compact-desktop recomposition unless the user explicitly expands scope to mobile or touch-first delivery.

## Layout ownership

- Resolve layout at the application shell or workspace boundary. Do not scatter unrelated raw breakpoint tests across feature components.
- Base decisions on available viewport/container space and content fit, not user-agent or operating-system detection.
- Preserve one semantic component tree and one command/data state where practical.
- Keep full-window chrome fixed and assign scrolling to explicit content regions.
- Do not make the whole page scroll horizontally.

## Modes

| Mode | Required behavior |
| --- | --- |
| Desktop (`1440 x 900`) | Show the full navigation, primary data region, and persistent inspector where useful. |
| Compact desktop (`1024 x 768`) | Protect the primary table/list, reduce secondary chrome, and recompose or hide the inspector. |
| Minimum window (`960 x 640`) | Keep primary navigation and actions reachable with no overlap, clipping, or page-level overflow. |

Do not claim mobile readiness from a narrow browser screenshot. If mobile becomes a requirement, define its composition, touch targets, safe areas, keyboard behavior, and acceptance anchors as a separate deliberate extension.

## Header

Prioritize Header content in this order:

1. Product identity.
2. Current destination.
3. Immediate primary action.
4. Theme and secondary actions.

- Keep labels on one line and truncate product metadata before removing current destination context.
- Allow only the navigation group to scroll horizontally when necessary; do not make the full Header scroll.
- Keep action groups fixed, compact, and nonoverlapping.
- Move low-frequency actions into a menu before shrinking icons or creating inaccessible targets.

## Workspace and inspector

- Give the main table/list the remaining flexible width and `min-width: 0`.
- Constrain every flexible ancestor with `min-width: 0`; use `minmax(0, 1fr)` for flexible grid tracks. Long content must not determine column widths.
- Wrap ordinary text by default, including unbroken Latin strings with `overflow-wrap: anywhere`. Do not mask overflow with clipping or ellipsis.
- Use `ScrollArea contentWidth="viewport"` for wrapping text panels; retain intrinsic measurement only for intentionally horizontally scrollable tables/code. Avoid nested vertical scroll owners.
- Use a 320 px fixed inspector on every desktop page. Below 1180 px, expose the details through a 340 px right-side sheet with 16 px horizontal content insets. Override Sheet width with the matching `data-[side=right]` variant so its default width cannot win.
- Keep toolbars and page headers shrink-free while the data region owns remaining height.
- Preserve selection when an inspector changes composition.
- Keep empty and error states inside the same region as their successful data surface.

- Quick Test keeps two columns across supported desktop sizes (960 px and above): inputs on the left and progress/history on the right, each with its own bounded ScrollArea below the fixed page header.

## Scroll ownership

- Keep the root at full viewport height with page-level overflow hidden.
- Give each scrollable region `min-height: 0` and exactly one `ScrollArea` owner.
- Use the shared 5 px overlay rails with 4 px inset for both axes.
- Preserve the container's measured geometry before and after overflow.
- Keep content padding visually symmetric; do not create a one-sided scrollbar gutter.
- Verify wheel, touchpad, scrollbar drag, and keyboard scrolling where relevant.

## Resize and state continuity

- Preserve active destination, selected record, filters, drafts, task progress, and errors while resizing.
- Release transient drag/pointer state before major recomposition.
- Restore focus only when the target remains visible and semantically equivalent.
- Respect reduced motion and keep nonessential layout transitions within 160 to 220 ms.

## Acceptance anchors

At `1440 x 900`, `1024 x 768`, and `960 x 640`, verify:

- no horizontal page scroll;
- no clipped or overlapping navigation/actions;
- table headers and inspectors remain readable;
- long Chinese/Latin labels, URLs, UUIDs, and errors wrap without widening their panel;
- loading, empty, filtered-empty, error, and large-data states preserve layout;
- primary commands remain reachable;
- light and dark themes keep equivalent hierarchy and contrast.

- In adjacent open workspace columns, the parent Flex/Grid owns the 16 px gap. Do not add another horizontal inset on both sides of that gap; keep navigation items' 8 px internal padding separate from panel spacing.
