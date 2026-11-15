# Codemods for the 0.9.0 UI-system pass

Mechanical migrations, so a package spends its time on the decisions and not on retyping
classes. Each one rewrites only the spellings that have exactly one right answer. Anything with
more than one answer is reported as a **review** finding, often with a suggestion, for a person to
decide.

```bash
cd frontend
bun scripts/codemods/run.ts <transform|all> <paths...> [flags]
```

| Flag             | What it does                                                                                                         |
| ---------------- | -------------------------------------------------------------------------------------------------------------------- |
| `--check`        | Changes nothing. Exits 1 while an old spelling it can rewrite is still there.                                        |
| `--dry-run`      | Changes nothing. Prints each changed region and every finding.                                                       |
| `--suggested`    | Also applies the suggestions of review findings. Read them first.                                                    |
| `--strict`       | With `--check`, review findings fail too.                                                                            |
| `--json`         | Prints findings as JSON lines.                                                                                       |
| `--no-format`    | Skips prettier on the changed files. Prettier normally runs after a write, so class order and line length are right. |
| `--no-guard`     | Runs a transform before its foundations are in the tree. Use it only in tests.                                       |
| `--moves <file>` | The moves table for `imports`. The default is `scripts/moves.json`.                                                  |
| `--src <dir>`    | The folder that `@/` and the moves table's paths resolve against. The default is `src`.                              |

Paths are required. A package runs the codemods only on the paths it owns, for example:

```bash
bun scripts/codemods/run.ts all src/components/docker src/app/\(dashboard\)/docker --dry-run
bun scripts/codemods/run.ts all src/components/docker src/app/\(dashboard\)/docker
bun scripts/codemods/run.ts all src/components/docker src/app/\(dashboard\)/docker --check
```

The `scripts/codemods/` folder is never read, because its tables and fixtures are full of the old
spellings on purpose.

## The order

`all` runs the transforms in this order. Run them one at a time in the same order when you want
to review each diff separately.

| Transform         | Rewrites                                                                                                                                                                                                                                                                                            | Reports for review                                                                                                                                                                                                                                                                                                                                                          | Finding        | Needs                                                            |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- | ---------------------------------------------------------------- |
| `imports`         | An import of a moved file goes to its new path. An import of names that left a file (`Section` from `components/page`) is split.                                                                                                                                                                    | A namespace import, `export *`, or `import()` of a file that some exports left.                                                                                                                                                                                                                                                                                             | WP-04          | `scripts/moves.json` (WP-04)                                     |
| `ink-opacity`     | `text-muted-foreground/40` to `/70` becomes `text-faint`, keeping its variants (`placeholder:`, `hover:`).                                                                                                                                                                                          | Every other faded text ink, with the nearest ink as the suggestion: `foreground/85` and up go to `text-foreground`, below that to muted, and `muted/80` goes to faint. A legacy fade in a list outside JSX that also sizes something, which may be an icon's. Fades on an svg, an icon component or an `[&_svg]:` variant are glyph fades. They stay, and are not reported. | FND-07         | none                                                             |
| `colour-vars`     | `text-[var(--tag-pink)]`, `text-(--git-added)` and `bg-[color:var(--x)]/15` become `text-tag-pink`, `text-git-added` and `bg-x/15`. This only applies to colours that globals.css registers as `--color-*`.                                                                                         | A `--tag-*`, `--git-*`, `--pull-*` or `--chart-*` variable that is not registered, such as `--git-colour`, a per-row variable. Other `var()` values (widths, Radix origins) are layout and are ignored.                                                                                                                                                                     | FND-20         | `--color-tag-*` (WP-01)                                          |
| `radius`          | An arbitrary radius that is exactly a step becomes that step: 2px `xs`, 4px `sm`, 6px `md`, 8px `lg`, 12px `xl`, 10px `tile-md`, 14px `tile-lg`. Sides and corners are included.                                                                                                                    | A radius near a step (1px, 3px, 0.6rem), with the step as the suggestion. Any other value, such as a map node's pill or `50%`. `rounded-[inherit]` stays.                                                                                                                                                                                                                   | FND-13         | `--radius-xs`, `--radius-tile-*` (WP-01)                         |
| `z-layers`        | `z-10` becomes raised, `z-20` sticky, `z-30` pane and `z-40` float. `z-50` becomes `z-immersive` when an `immersive` or full-screen condition switches it on, `z-tooltip` in a tooltip's file, and `z-overlay` everywhere else.                                                                     | Arbitrary and negative values.                                                                                                                                                                                                                                                                                                                                              | FND-16         | `--z-index-*` (WP-01)                                            |
| `vh`              | `vh` inside an arbitrary value becomes `svh`. `h-[min(75vh,40rem)]` becomes `h-workbench`.                                                                                                                                                                                                          | `h-screen`, `min-h-screen` and `max-h-screen`. A well wants `svh` and a full-screen pane may want `dvh`.                                                                                                                                                                                                                                                                    | MOB-23         | `--height-workbench` (WP-01)                                     |
| `utilities`       | The named layout values of foundations §12: `grid-cols-main-end`, `start-main`, `start-main-end`, `grid-rows-fill`, `w-rail`, `w-inspector`, `h-workbench` and `max-h-rail`. `align-[-2px]` becomes `glyph-inline` and takes an `inline` with it. `font-mono text-[0.9em]` becomes `code-inline`.   | An `align-[…]` next to another display (`inline-flex`, `inline-block`). A `0.9em` size without `font-mono`. An inspector column of 20rem or 24rem in a grid template, with 22rem as the suggestion.                                                                                                                                                                         | FND-23         | the §12 values (WP-01)                                           |
| `containers`      | A container width that is exactly a Tailwind container size takes the size's name (`@min-[36rem]` becomes `@xl`, `@[64rem]` becomes `@5xl`). `@max-[38rem]` becomes `@max-stack`. A named container keeps its name (`/detail`).                                                                     | `@max-[30rem]`, `[34rem]` and `[36rem]`, which the plan folds into the stack width. That moves where the row stacks, so `@max-stack` is only a suggestion. Any other width, such as `@max-[46rem]`, which is 2xl or 3xl by review.                                                                                                                                          | FND-15         | `--container-stack` (WP-01)                                      |
| `media-queries`   | `useMediaQuery("(min-width: 1024px)")` becomes `useBreakpoint("lg")` and `"(max-width: 1023px)"` becomes `useBelow("lg")`. The import is added, and `useMediaQuery` leaves its import when nothing else uses it. The class variants `min-[1280px]:` and `max-[1023px]:` become `xl:` and `max-lg:`. | Widths that are not one of the five breakpoints (1100, 1400, 1600, 479), which usually mean a container query. A query held in a constant or built in a template. `matchMedia` with a breakpoint, which can use `breakpointQuery`.                                                                                                                                          | FND-14         | `lib/breakpoints.ts` with `useBreakpoint` and `useBelow` (WP-01) |
| `control-heights` | On a default-size `Button`, `h-11 sm:h-9` goes. On any `Button`, `max-sm:h-8` and `max-sm:h-10` go. On an `Input`, `h-8 sm:h-7` becomes `h-7`. No desktop height changes; the touch height comes from the control's own `pointer-coarse:` step.                                                     | `h-11 sm:h-9` on a Button of another size, or of a size given as an expression, because dropping it would change the desktop height. `max-sm:h-*` next to a fixed `h-*`, which beats the touch step. Any pair under a condition.                                                                                                                                            | FND-08, MOB-06 | `pointer-coarse:` in Button and Input (WP-02)                    |
| `rise`            | `<Page className="animate-rise">` becomes `<Page rise>`, and the rest of the class list stays.                                                                                                                                                                                                      | `animate-rise` under a condition, because the prop would make it unconditional.                                                                                                                                                                                                                                                                                             | FND-11         | Page's `rise` prop (WP-09)                                       |

Why this order:

- `imports` runs first, so every later step reads the final paths.
- The class spellings come next. `vh` runs before `utilities`, so a `vh` workbench height that
  `vh` turns into `svh` is named by `utilities` in the same run.
- The component-API steps come last, because they depend on the components having the new
  props.

A transform whose foundations are not in the tree yet refuses to run, and names what it is
waiting for. Under `all`, it is skipped with that message.

## After running

1. Read the diff. A rewrite is the same value or the plan's decision, but it is still your page.
2. Work through the review lines. `--dry-run --suggested` shows what the suggestions would do.
3. Run `bunx tsc --noEmit`, `bunx eslint <files>` and the specs your page has.
4. Run `--check` on your paths before you hand the package in.

## Tests

Each transform has a `before` and `after` fixture under `fixtures/<transform>/`. The fixtures use
the `.tsx.fixture` extension, so neither tsc nor ESLint reads their old spellings. Each transform
also has a `<transform>.test.js` beside it. The test applies the transform to the fixture, checks
that a second pass rewrites nothing, and runs `run.ts` on a scratch copy: `--check` fails, then
`--dry-run` leaves the file alone, then a write produces the `after` fixture, then `--check`
passes.

```bash
cd frontend && bun test scripts/codemods
```
