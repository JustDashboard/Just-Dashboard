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

`docs/design-system-audit-report.html` predates the unification pass and describes primitives
(`StatusBadge`, `card.tsx`, `ui/badge.tsx`) that no longer exist.
