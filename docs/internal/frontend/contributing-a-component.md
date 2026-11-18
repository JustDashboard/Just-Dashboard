# Contributing a component

How a shared component is added, named, documented and retired. Read
[`components/README.md`](components/README.md) first: the component you want may exist.

## When it is shared

- **A component starts in its area**, in `components/<area>/`, while one area uses it.
- **It is promoted on the second area.** The day a second section imports it, it moves to
  `components/` (or a folder there such as `components/wire/` or `components/settings/` when it is a
  kit of several files). A component another area imports from `components/<area>/` is a component
  hiding in the wrong folder; the lint rules and the catalogue cannot see it there.
- **A shadcn or Radix primitive** lives in `components/ui/`, and product-wide behaviour of a control
  (sizes, focus, the touch step) changes there, never at a call site.
- **Before you add one, look for the same job.** Two components that do one job are the defect the
  0.9.0 pass spent most of its effort undoing. Extend the existing one with a variant rather than
  adding a sibling.

## How it is made

- **One file per component, named after its export**: `RankList` is `rank-list.tsx`. Its pure logic
  goes in a `-model.ts` beside it with a `bun test` (`bar-reading-model.test.js`), not in a browser
  spec.
- **It carries `data-slot`** with its name in kebab case (`data-slot="rank-list"`), so specs and the
  probes can find it without a class.
- **It takes tokens and roles, not values**: the type roles, spacing roles, radius, layers and
  motion in [`foundations/`](foundations/README.md). No `description` prop on anything with a title.
- **It holds still**: it renders at its loaded height while it loads (`Bone`, `LoadingRows`), and a
  row is one height in every state.
- **It works by touch and keyboard**: a 24px target (44 on touch), a name for every icon-only
  control, the one focus ring, nothing that only a hover reveals.
- **Comments say why.** The doc comment says what job it does and what it replaced.

## How it is documented

In the same pull request:

1. a row in the catalogue, [`components/README.md`](components/README.md): export, family, file,
   one line, status;
2. an entry on its family's page: purpose, anatomy, use when and when not (and what instead),
   props, states, the phone, accessibility, an example call site, what enforces it, related;
3. if it changes a rule, the rule's page in [`foundations/`](foundations/README.md),
   [`patterns/`](patterns/README.md) or [`standard.md`](standard.md).

`src/docs-catalogue.test.js` fails when a shared export has no catalogue row, when a row names
something that is not an export, and when a doc names a component the code does not have. Its
baseline is empty: a new export arrives with its row. `scripts/doc-names.py`, run by
`scripts/test-changed.sh` on every changed page here, also checks the files and routes a changed
line names.

## How it is retired

In steps, so no pull request has to change every call site at once:

1. Keep the old name working as a re-export or a thin wrapper with `/** @deprecated use X */`.
2. Mark its catalogue row *deprecated* and point it at the replacement.
3. Move the call sites, area by area.
4. Delete the old name, and add a lint rule that refuses it, so it cannot come back. The catalogue
   row goes, and the story of why moves to [`decisions/`](decisions/README.md).

**Codemods.** The mechanical part of a migration is `frontend/scripts/codemods/run.ts`. Run it on
the paths you own, in the order its README gives: `--dry-run` first, then a write, then `--check`
before you hand in. It rewrites only spellings with one right answer and lists the rest as review
lines. Each one waits until its foundation has landed.
