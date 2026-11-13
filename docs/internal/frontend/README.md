# Frontend guides

- [`design-system.md`](design-system.md) — the design system's rules in full: one mode, no lift, the
  colour roles, the component vocabulary, and which surface to reach for.
- [`shell-design.md`](shell-design.md) — routing, shell, layout primitives, accessibility, editors, and charts.
- [`features-terminal.md`](features-terminal.md) — feature-specific UI and the terminal workspace.
- [`feature-map.md`](feature-map.md) — all App Router feature areas and their component ownership.
- [`data-theming.md`](data-theming.md) — API/socket state, metrics state, confirmations, and updates.

Read [`design-system.md`](design-system.md) before adding a component that any other page could also
want. `eslint-rules/design-system.mjs` and `tests/browser/design-system.spec.ts` enforce the parts of
it that a machine can check; the rest is judgement and lives in that document.

`eslint.config.mjs` also holds every `src/**/*.tsx` to `jsx-a11y`'s recommended rules, as errors: a
field has a label, and what a pointer can press a keyboard can reach. The rules that are wrong for
this codebase are off or configured there with the reason beside them. Where a rule misreads one
place (a row that is the pointer's larger target for the link inside it), fix the markup if a
keyboard really is left out, and otherwise say why on the line's own disable comment.

`docs/design-system-audit-report.html` predates the unification pass and describes primitives
(`StatusBadge`, `card.tsx`, `ui/badge.tsx`) that no longer exist.
