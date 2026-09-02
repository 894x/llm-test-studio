# LLM Test Studio design tokens and implementation rules

Use `doc/design/desktop-ui-system.md` as the normative token table and `apps/desktop/frontend/src/index.css` as the executable mapping. Read both before changing a stable token. Keep them synchronized in the same focused change.

## Semantic color roles

Use the established roles rather than literal theme colors:

- application background;
- elevated, glass, subtle, control, hover, and active surfaces;
- default and strong borders;
- primary, secondary, muted, and disabled text;
- accent, accent hover, and accent pressed;
- success, warning, error, and their soft/strong variants.

Consume the roles through CSS custom properties, Tailwind theme mappings, or existing component variants. Do not use `white`, `black`, arbitrary opacity utilities, raw hex values, or JSX `dark:` color branches for interface styling.

Use accent for action, focus, selection, and meaningful emphasis. Do not use success as a second accent. Do not add saturated blue, green, purple, or pink without domain meaning.

## Theme preference

- Keep preference values closed to `system`, `light`, and `dark`.
- Resolve the active theme to `light` or `dark` only.
- Default new users to `system` and persist explicit preference locally.
- Apply the resolved root class, `data-theme`, preference metadata, and `color-scheme` before theme-dependent UI renders.
- React to `prefers-color-scheme` changes while preference is `system`.
- Use one 28 px circular ghost theme trigger with Contrast, Sun, or Moon and a radio menu labeled 跟随系统、浅色、深色.
- Keep trigger name, menu state, focus restoration, and keyboard behavior accessible.

## Geometry and density

| Role | Stable value |
| --- | --- |
| Panel radius | `8px` |
| Control radius | `6px` |
| Compact badge/chip radius | `4px` to `6px` |
| Compact icon button | `28px` |
| Ordinary compact control | `32px` |
| Interface icon | `16px` to `18px` |
| Local gap | `8px` |
| Section rhythm | `12px` to `16px` |
| Page/panel padding | `16px` |
| Overlay scrollbar | `5px` rail and radius with `4px` edge inset |

Keep Header controls compact. Do not enlarge every desktop control to mobile touch dimensions. Give elevation only to floating or clearly separated surfaces; avoid nested shadows and nested borders.

## Typography

- Preserve the configured Geist/Segoe UI sans stack and Cascadia/SFMono/Consolas monospace stack.
- Use 20 px semibold for a primary page title when the available chrome supports it.
- Use 14 to 16 px medium or semibold for section titles.
- Use 12 to 14 px for body and controls.
- Use 11 to 12 px for metadata and helper text.
- Use tabular numerals for comparable counts, timings, rates, and revisions.
- Use monospace for IDs, paths, code, and protocol values where scanning benefits.
- Avoid decorative uppercase, broad letter spacing, and oversized marketing type.

## Motion

- Keep hover, selection, and panel transitions between 160 and 220 ms.
- Animate opacity, transform, color, or one positional property rather than multiple geometry properties.
- Do not use ripple or scale effects on dense controls.
- Respect `prefers-reduced-motion` globally and in local animations.
