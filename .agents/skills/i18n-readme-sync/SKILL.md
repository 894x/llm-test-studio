---
name: i18n-readme-sync
description: Keep English README files and their corresponding Simplified Chinese README versions synchronized. Use whenever adding, editing, rewriting, reorganizing, renaming, or deleting README content, including root and nested README files. This skill covers README documentation localization only; do not apply it to source-code i18n, UI strings, locale catalogs, or other documentation unless the user explicitly expands the scope.
---

# README i18n Sync

Keep each changed README and its Simplified Chinese counterpart semantically equivalent in the same change.

## Resolve the README Pair

1. Inspect the repository status before editing. Preserve unrelated staged, unstaged, and untracked changes.
2. Resolve the pair within the same directory. Prefer an existing repository convention or reciprocal language links.
3. For an English `README.md`, use an existing Chinese sibling such as `README.zh-CN.md`, `README.zh.md`, `README_zh.md`, or `README-cn.md`. Do not create a duplicate under a different convention.
4. If no Chinese sibling or repository convention exists, create `README.zh-CN.md` next to `README.md`.
5. For a Chinese README change, resolve its English source to the same directory's `README.md`, then `README.en.md` or `README.en-US.md` when that is the established convention.
6. If pairing is genuinely ambiguous, inspect nearby README pairs and project instructions. Ask the user only when those sources cannot establish the intended pair.

Apply the same rules independently to every changed README in nested directories.

## Synchronize Content

- Apply additions, removals, moves, and corrections to both language versions in one working change.
- Keep the same information architecture: heading hierarchy, section order, tables, lists, notes, examples, warnings, and setup paths.
- Translate prose into natural Simplified Chinese. Preserve product names and terminology when translation would reduce precision.
- Preserve commands, code, identifiers, environment variables, URLs, relative link targets, filenames, version numbers, badges, and configuration keys unless the underlying technical content changed.
- Keep code fences and examples executable. Translate comments inside code only when doing so does not change the example's behavior or expected output.
- Preserve existing language-switch links and make reciprocal links consistent when creating a new counterpart.
- Match meaning and technical facts rather than sentence structure. Do not leave one version with newer behavior, requirements, caveats, or examples.

When deleting or renaming a README, update or remove its counterpart and repair incoming language-switch links in the same change.

## Validate Before Completion

1. Review the diff for both files together.
2. Compare heading hierarchy and verify that every substantive section exists in both versions.
3. Check that commands, code blocks, link targets, paths, identifiers, numbers, and version requirements agree.
4. Run `git diff --check` when Git is available.
5. Confirm that both members of every affected pair appear in the final diff, including newly created files.

Do not report the README update as complete when only one language version changed, unless the user explicitly requested a one-language exception. State that exception in the final response.

## Scope Boundary

Do not introduce or modify application localization frameworks, translation keys, locale resources, runtime language selection, or UI copy under this skill. Treat code i18n as separate work requiring explicit user direction.
