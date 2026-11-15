/**
 * colour-vars: a registered colour is reached by its utility, not through `var()` (FND-20).
 *
 * `text-[var(--tag-pink)]`, `text-(--git-added)` and `bg-[color:var(--pull-open)]/15` become
 * `text-tag-pink`, `text-git-added` and `bg-pull-open/15` when globals.css registers the colour
 * as `--color-<name>`. A tag, git, pull or chart variable that is not registered is reported;
 * anything else in a `var()` (a width, a Radix origin) is layout and is left alone.
 */
import { classTransform, type Rule, type Transform, withUtility } from "./core"

const COLOUR_PREFIX =
  "text|bg|border|border-[xytrblse]|fill|stroke|ring|ring-offset|outline|decoration|from|via|to|divide|caret|accent|placeholder"
const VAR = new RegExp(
  `^(${COLOUR_PREFIX})-(?:\\[(?:color:)?var\\(--([a-z0-9-]+)\\)\\]|\\((?:color:)?--([a-z0-9-]+)\\))(/.+)?$`,
)
const FAMILY = /^(tag|git|pull|chart)-/

/** The families WP-01 registers, for a tree where globals.css has not been read. */
export const PLANNED_COLOURS = [
  ...["slate", "red", "amber", "green", "cyan", "blue", "violet", "pink"].map((c) => `tag-${c}`),
  ...["added", "modified", "deleted", "untracked", "renamed", "conflict"].map((c) => `git-${c}`),
  ...["open", "merged", "closed", "draft"].map((c) => `pull-${c}`),
  ...[1, 2, 3, 4, 5].map((n) => `chart-${n}`),
]

/** The colour names globals.css registers as `--color-<name>`. */
export function registeredColours(css: string) {
  return new Set([...css.matchAll(/--color-([a-z0-9-]+)\s*:/g)].map((m) => m[1]))
}

const rule =
  (colours: Set<string>): Rule =>
  (token) => {
    const m = VAR.exec(token.utility)
    if (!m) return undefined
    const [, prefix, bracketed, bare, alpha = ""] = m
    let name = bracketed ?? bare
    if (name.startsWith("color-")) name = name.slice("color-".length)
    if (colours.has(name)) return { to: withUtility(token, `${prefix}-${name}${alpha}`) }
    if (FAMILY.test(name))
      return { review: `--${name} is not registered as --color-${name} in globals.css` }
    return undefined
  }

export const colourVars: Transform = {
  name: "colour-vars",
  summary: "text-[var(--tag-pink)] and text-(--git-added) to text-tag-pink and text-git-added",
  closes: ["FND-20"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--color-tag-pink\s*:/,
      why: "the tag, git and pull colours are registered as utilities (WP-01)",
    },
  ],
  apply: (text, file, options = {}) =>
    classTransform("colour-vars", () => rule(options.colours ?? new Set(PLANNED_COLOURS)))(
      text,
      file,
      options,
    ),
}
