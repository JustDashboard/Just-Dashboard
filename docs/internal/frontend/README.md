# Frontend guides

## Build a page in five steps

1. **Pick the shape.** A page that reports is a reading page, a list of things you open is a list
   page, an item's detail is a page with a rail, settings are one column under one save bar, a
   page where the reader decides a sequence is a flow page, and a workbench is one surface held to
   the window, the sheet its frame. [`patterns/registers.md`](patterns/registers.md) has the test.
2. **Open its recipe** in [`recipes/`](recipes/README.md): the reference route and its shot, the
   order of the regions, a skeleton, the four states, the phone shape, a checklist and the spec to
   add.
3. **Take its components from the catalogue**, [`components/README.md`](components/README.md).
   A component another page could want is shared, and
   [`contributing-a-component.md`](contributing-a-component.md) says when to promote one and how.
4. **Declare the register** (`data-register` through `Page`), and hold the page to
   [`standard.md`](standard.md), the operator's decisions and D1 to D14, and to
   [`patterns/accessibility.md`](patterns/accessibility.md), what it owes a keyboard and a screen
   reader.
5. **Run the spec.** `scripts/test-changed.sh` runs `design-system.spec.ts` for any UI change, and
   `axe-gate.spec.ts` for a change to any page it opens, which is every route: it fails on a
   serious or critical axe result, less the routes listed in its `KNOWN` with their finding. It
   also runs the specs that open the page. Then look at it at 1440 and at 390 on a touch device.

## The pages

| Page | What it holds |
| --- | --- |
| [`standard.md`](standard.md) | The operator's decisions, D1 to D14, and the tensions' answers, each with what holds it |
| [`foundations/`](foundations/README.md) | Tokens and scales: colour, surfaces, type, spacing, motion, words, layout and the phone |
| [`components/`](components/README.md) | The catalogue: one row per shared export, one page per family |
| [`patterns/`](patterns/README.md) | Rules that span components, and the accessibility contract |
| [`recipes/`](recipes/README.md) | One recipe per page shape: reading, list, detail with a rail, settings, flow, workbench |
| [`decisions/`](decisions/README.md) | Why each page looks as it does: the dated stories, by section |
| [`design-system.md`](design-system.md) | The index from each `§n` cited in the code to its page |
| [`shell.md`](shell.md) | Routing, the shell, the rail, sign-in, the hub link, command search, the editor |
| [`features/`](features/README.md) | Every route area: what its pages do, the files that own them and their specs |

`eslint.config.mjs` also holds every `src/**/*.tsx` to `jsx-a11y`'s recommended rules, as errors: a
field has a label, and what a pointer can press a keyboard can reach. The rules that are wrong for
this codebase are off or configured there with the reason beside them. Where a rule misreads one
place (a row that is the pointer's larger target for the link inside it), fix the markup if a
keyboard really is left out, and otherwise say why on the line's own disable comment.

`docs/audits/2026-08-28-design-system-audit/report.html` predates the unification pass and describes
primitives that no longer exist.
