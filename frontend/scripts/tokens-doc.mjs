// Prints the token reference, docs/internal/frontend/foundations/tokens.md, from the stylesheet.
//
//   bun scripts/tokens-doc.mjs > ../docs/internal/frontend/foundations/tokens.md   (from frontend/)
//
// The page was written by hand and listed families, so a value changed in globals.css and the page
// went on saying the old one. Generated, it cannot: src/docs-catalogue.test.js fails when the page
// differs from what this prints. What the stylesheet cannot say (what a family is for, which page
// tells its rules) is the FAMILIES table below; a token no family claims lands under "Other", which
// is the cue to give it one here.

import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
export const STYLESHEET = join(here, "..", "src", "app", "globals.css")
export const PAGE = join(
  here,
  "..",
  "..",
  "docs",
  "internal",
  "frontend",
  "foundations",
  "tokens.md",
)

// Every family a token can belong to, in the order the page lists them. A token takes the first
// family whose pattern matches its name; `use` is the utility prefix a colour family is written
// with (`bg-card`, `text-faint`), since Tailwind makes every prefix from one `--color-*`.
const FAMILIES = [
  {
    part: "Colour",
    name: "Grounds",
    match:
      /^--(ground|canvas|background|card|popover|muted|secondary|control|surface-[a-z]+|panel-ground)$/,
    use: "bg",
    page: "surfaces.md",
    for: "The window under the page, then the ladder a surface steps up: page, block, floating surface, control; a panel's ground is the one its insides are drawn over.",
  },
  {
    part: "Colour",
    name: "Inks",
    match: /^--(foreground|faint|(muted|card|popover|secondary|accent)-foreground)$/,
    use: "text",
    page: "colour.md",
    for: "The three inks, and the foreground each ground pairs with; faint keeps 4.5:1.",
  },
  {
    part: "Colour",
    name: "Edges",
    match:
      /^--(line|line-subtle|line-strong|border|input|hairline|border-strong|control-edge|edge|grid-dot|wire)$/,
    use: "border",
    page: "surfaces.md",
    for: "The three white lines, a frame, a field, where a header meets a body, a destination's hover, a control's 3:1 edge (T3), a control face's lit top, a canvas's dots.",
  },
  {
    part: "Colour",
    name: "Brand",
    match: /^--(brand(-[a-z]+)?|signal|ring)$/,
    use: "bg",
    page: "colour.md",
    for: "A command's face, where you are, a location mark, the focus ring.",
  },
  {
    part: "Colour",
    name: "Neutral command",
    match: /^--primary(-foreground)?$/,
    use: "bg",
    page: "colour.md",
    for: "White as a fill, not the command face.",
  },
  {
    part: "Colour",
    name: "Status",
    match: /^--(success(-foreground)?|warning|destructive)$/,
    use: "text",
    page: "colour.md",
    for: "A reading's state, never decoration.",
  },
  {
    part: "Colour",
    name: "Danger face",
    match: /^--destructive-(face|active|foreground)$/,
    use: "bg",
    page: "colour.md",
    for: "The danger button at rest and pressed, and its label.",
  },
  {
    part: "Colour",
    name: "Tints",
    match: /^--((plot|wash|rule)-[a-z-]+|mark|meter-track)$/,
    use: "bg",
    page: "colour.md",
    for: "An icon plot, a tinted banner, a tinted rule, a match mark, a meter's track.",
  },
  {
    part: "Colour",
    name: "Interaction",
    match: /^--(row-hover|menu-hover|control-hover|control-active|accent)$/,
    use: "bg",
    page: "states.md",
    for: "Hover's three answers, press feedback, selection.",
  },
  {
    part: "Colour",
    name: "Flow",
    match: /^--(flow-[a-z-]+|choice-surface)$/,
    use: "bg",
    page: "surfaces.md",
    for: "Register B's focused surface, a choice card's ground and the lit edge.",
  },
  {
    part: "Colour",
    name: "Charts",
    match: /^--chart-\d+$/,
    use: "text",
    page: "colour.md",
    for: "A series' hue; `--chart-5` is the RX colour of every in/out pair, near hue 200 (T9).",
  },
  {
    part: "Colour",
    name: "Categorical",
    match: /^--tag-[a-z]+$/,
    use: "text",
    page: "colour.md",
    for: "A kind: a Redis key type, a BSON type, a folder label, a lane.",
  },
  {
    part: "Colour",
    name: "Git",
    match: /^--(git|pull)-[a-z]+$/,
    use: "text",
    page: "colour.md",
    for: "A pending change, a pull request's state; aliased onto the tag ramp.",
  },
  {
    part: "Colour",
    name: "Languages",
    match: /^--language-[a-z]+$/,
    use: "text",
    page: "colour.md",
    for: "A checkout's language, at one lightness so no language shouts.",
  },
  {
    part: "Colour",
    name: "Folders",
    match: /^--folder(-[a-z]+)?$/,
    use: "text",
    page: "colour.md",
    for: "A folder's colour; the back and the mark are mixed from it on `[data-folder]`.",
  },
  {
    part: "Colour",
    name: "Documents",
    match: /^--doc-[a-z]+$/,
    use: "text",
    page: "colour.md",
    for: "A file preview's page, fold, lines and label.",
  },
  {
    part: "Colour",
    name: "Sidebar",
    match: /^--sidebar(-[a-z-]+)?$/,
    use: "bg",
    page: "surfaces.md",
    for: "The rail.",
  },
  {
    part: "Scales",
    name: "Type",
    match: /^--text-[a-z]+$/,
    page: "type.md",
    for: "The product's own steps, each with its line height; Tailwind's `xs`, `sm`, `base` and `2xl` sit between them. On a touch screen hint and micro step up (T1).",
  },
  {
    part: "Scales",
    name: "Fonts",
    match: /^--font-[a-z]+$/,
    page: "type.md",
    for: "The sans every word is set in, the mono for values, and Excalidraw's hand for a drawing's words.",
  },
  {
    part: "Scales",
    name: "Tracking",
    match: /^--tracking-[a-z]+$/,
    page: "type.md",
    for: "Letter spacing, every step relative to the normal one.",
  },
  {
    part: "Scales",
    name: "Radius",
    match: /^--radius(-[a-z-]+)?$/,
    page: "surfaces.md",
    for: "One radius per role, outer to inner: a swatch, a mark, a control, a well, a block; the tile steps are a logo tile's.",
  },
  {
    part: "Scales",
    name: "Shadow",
    match: /^--shadow-[a-z0-9]+(-[a-z]+)*$/,
    page: "surfaces.md",
    for: "Only for what floats: a popover, a dropdown, a dialog. The three `edge` steps are insets, a control face's lit top pixel, and cast nothing.",
  },
  {
    part: "Scales",
    name: "Motion",
    match: /^--(ease|duration|animate)-[a-z]+$/,
    page: "motion.md",
    for: 'Two curves, four durations and four animations; `prefers-reduced-motion` and `data-motion="paused"` collapse them (T2).',
  },
  {
    part: "Scales",
    name: "Control sizes",
    match: /^--control-(dense|compact|regular|large)$/,
    page: "layout-phone.md",
    for: "A control's box by density; a touch screen takes the taller step.",
  },
  {
    part: "Scales",
    name: "Layers",
    match: /^--z-index-[a-z]+$/,
    page: "layout-phone.md",
    for: "Stacking by name; each step says what lives there.",
  },
  {
    part: "Scales",
    name: "Layout",
    match:
      /^--(grid-template-[a-z]+-[a-z-]+|width-[a-z]+|height-[a-z]+|max-height-[a-z]+|container-[a-z]+)$/,
    page: "layout-phone.md",
    for: "The grids, widths and heights that recur, named so the layout that recurs is a word.",
  },
  {
    part: "Scales",
    name: "Breakpoints",
    match: /^--breakpoint-[a-z0-9]+$/,
    page: "layout-phone.md",
    for: "Tailwind's own, written out because `lib/breakpoints.ts` mirrors them for script.",
  },
]

// Theme namespaces and the utility each writes, longest first so `--grid-template-columns-*` is
// not read as something shorter.
const NAMESPACES = [
  ["grid-template-columns", (k) => `grid-cols-${k}`],
  ["grid-template-rows", (k) => `grid-rows-${k}`],
  ["transition-duration", (k) => `duration-${k}`],
  ["max-height", (k) => `max-h-${k}`],
  ["breakpoint", (k) => `${k}:`],
  ["container", (k) => `@${k}:`],
  ["tracking", (k) => `tracking-${k}`],
  ["z-index", (k) => `z-${k}`],
  ["animate", (k) => `animate-${k}`],
  ["radius", (k) => `rounded-${k}`],
  ["shadow", (k) => `shadow-${k}`],
  ["height", (k) => `h-${k}`],
  ["width", (k) => `w-${k}`],
  ["color", (k, family) => `${colourPrefix(k, family)}-${k}`],
  ["font", (k) => `font-${k}`],
  ["text", (k) => `text-${k}`],
  ["ease", (k) => `ease-${k}`],
  ["size", (k) => `size-${k}`],
]

// The prefix a colour is written with: its family's, unless the name says what it paints.
function colourPrefix(key, family) {
  if (key.endsWith("foreground")) return "text"
  if (key.endsWith("border") || key.startsWith("rule-")) return "border"
  if (key === "ring") return "ring"
  return family?.use ?? "bg"
}

function utilityOf(themeName, family) {
  for (const [space, write] of NAMESPACES) {
    const prefix = `--${space}-`
    if (themeName.startsWith(prefix)) return write(themeName.slice(prefix.length), family)
  }
  return ""
}

function familyOf(name) {
  return FAMILIES.find((family) => family.match.test(name))
}

// The stylesheet as statements: each declaration with the at-rules and selectors around it and
// the comment on its own line, and each block opened. It reads the shape Prettier writes, which is
// what the repository's format check keeps it in: one statement per line or a run of lines ending
// in `;` or `{`, and `}` on a line of its own.
export function parseStylesheet(css) {
  const declarations = []
  const blocks = []
  const stack = []
  let buffer = ""
  let inComment = false
  for (const raw of css.split("\n")) {
    let code = ""
    const notes = []
    let rest = raw
    while (rest.length > 0) {
      if (inComment) {
        const end = rest.indexOf("*/")
        if (end < 0) break
        inComment = false
        rest = rest.slice(end + 2)
        continue
      }
      const start = rest.indexOf("/*")
      if (start < 0) {
        code += rest
        break
      }
      code += rest.slice(0, start)
      const end = rest.indexOf("*/", start + 2)
      if (end < 0) {
        inComment = true
        break
      }
      notes.push(rest.slice(start + 2, end).trim())
      rest = rest.slice(end + 2)
    }
    const line = code.trim()
    if (!line) continue
    if (line === "}") {
      stack.pop()
      continue
    }
    buffer = buffer ? `${buffer} ${line}` : line
    if (buffer.endsWith("{")) {
      const prelude = buffer.slice(0, -1).trim()
      blocks.push({ context: [...stack], prelude })
      stack.push(prelude)
      buffer = ""
    } else if (buffer.endsWith(";")) {
      const match = buffer.match(/^(--[A-Za-z0-9-]+)\s*:\s*([\s\S]*);$/)
      if (match) {
        declarations.push({
          context: [...stack],
          name: match[1],
          value: match[2].replace(/\s+/g, " ").trim(),
          note: notes.join(" "),
        })
      }
      buffer = ""
    }
  }
  return { declarations, blocks }
}

const same = (context, ...preludes) =>
  context.length === preludes.length && preludes.every((prelude, i) => context[i] === prelude)

const isTheme = (context) => context.length === 1 && /^@theme\b/.test(context[0])

function rowsOf(css) {
  const { declarations, blocks } = parseStylesheet(css)
  const root = declarations.filter((d) => same(d.context, ":root"))
  const touch = new Map(
    declarations
      .filter((d) => same(d.context, "@media (pointer: coarse)", ":root"))
      .map((d) => [d.name, d.value]),
  )
  const scoped = declarations.filter(
    (d) => d.context.length === 1 && d.context[0].startsWith("[") && d.context[0].endsWith("]"),
  )
  const theme = declarations.filter((d) => isTheme(d.context))
  const rootNames = new Set(root.map((d) => d.name))
  const lineHeights = new Map(
    theme
      .filter((d) => d.name.endsWith("--line-height"))
      .map((d) => [d.name.slice(0, -"--line-height".length), d.value]),
  )

  const rows = []
  const byName = new Map()
  const add = (row) => {
    rows.push(row)
    byName.set(row.name, row)
  }
  for (const d of root) add({ name: d.name, value: d.value, note: d.note, utilities: [] })
  for (const d of scoped) {
    add({ name: d.name, value: d.value, note: d.note, utilities: [], on: d.context[0] })
  }
  for (const d of theme) {
    if (d.name.endsWith("--line-height")) continue
    const alias = d.value.match(/^var\((--[A-Za-z0-9-]+)\)$/)
    if (alias && rootNames.has(alias[1])) {
      // A theme name that only points at a :root token is that token's utility, not a token.
      const target = byName.get(alias[1])
      target.utilities.push({ theme: d.name, note: d.note })
      continue
    }
    add({ name: d.name, value: d.value, note: d.note, utilities: [{ theme: d.name }] })
  }
  for (const row of rows) {
    row.family = familyOf(row.name)
    row.touch = touch.get(row.name)
    row.lineHeight = lineHeights.get(row.name)
    row.touchLineHeight = touch.get(`${row.name}--line-height`)
  }

  const utilities = blocks
    .filter((b) => b.context.length === 0 && b.prelude.startsWith("@utility "))
    .map((b) => b.prelude.slice("@utility ".length).trim())
  const components = blocks
    .filter((b) => same(b.context, "@layer components") && b.prelude.startsWith("."))
    .map((b) => b.prelude)
  const classes = new Set()
  for (const b of blocks) {
    if (b.context.length !== 0 || !b.prelude.startsWith(".")) continue
    for (const selector of b.prelude.split(",")) {
      const name = selector.trim().match(/^\.([A-Za-z0-9_-]+)/)
      if (name) classes.add(`.${name[1]}`)
    }
  }
  return { rows, utilities, components, classes: [...classes] }
}

const cell = (text) => String(text).replaceAll("|", "\\|")
const code = (text) => `\`${text}\``

function valueCell(row) {
  let value = code(row.value)
  if (row.lineHeight) value += ` / ${code(row.lineHeight)}`
  if (row.touch || row.touchLineHeight) {
    const size = row.touch ?? row.value
    value += `; touch ${code(size)}`
    if (row.touchLineHeight) value += ` / ${code(row.touchLineHeight)}`
  }
  return cell(value)
}

function utilityCell(row) {
  const names = row.utilities.map((u) => utilityOf(u.theme, row.family)).filter(Boolean)
  return cell(names.map(code).join(", "))
}

function noteCell(row) {
  const notes = [row.on && `set on ${code(row.on)}`, row.note, ...row.utilities.map((u) => u.note)]
  return cell(notes.filter(Boolean).join("; "))
}

function table(rows) {
  const lines = ["| Token | Value | Utility | Note |", "| --- | --- | --- | --- |"]
  for (const row of rows) {
    lines.push(`| ${code(row.name)} | ${valueCell(row)} | ${utilityCell(row)} | ${noteCell(row)} |`)
  }
  return lines.join("\n")
}

const HEAD = `# Tokens

<!-- Generated by frontend/scripts/tokens-doc.mjs from frontend/src/app/globals.css. Do not edit:
change the stylesheet (or a family in the script) and run, from frontend/,
bun scripts/tokens-doc.mjs > ../docs/internal/frontend/foundations/tokens.md
src/docs-catalogue.test.js fails while this page differs from what the script prints. -->

Every token lives in \`frontend/src/app/globals.css\`, on \`:root\` or in an \`@theme\` block. That is the
only place a colour, a shadow or a scale step is named. A call site writes the utility, never the
value. A theme name that only points at a \`:root\` token is listed as that token's utility; the
value is the stylesheet's own, and "touch" is what a coarse pointer gets instead. \`cn\` in
\`lib/utils.ts\` is told about every theme name that shares a group with a stock one (a size, a
layer, a radius, a duration), or tailwind-merge drops it ([\`type.md\`](type.md)).`

const TAIL = `## Removed in 0.9.0

Nothing read them: \`--warning-foreground\`, \`--signal-foreground\`, \`--sidebar-primary\`,
\`--sidebar-primary-foreground\`, \`--sidebar-ring\`, \`--plot-signal\`, \`--plot-primary-hover\`,
\`--shadow-color\`, \`--shadow-2xs\`, \`--shadow\` and \`--shadow-2xl\`, and the phantom \`mark-*\` names
\`cn\` had been told about. \`--destructive-foreground\` stays: the danger face's label reads it.`

export function tokensDoc(css) {
  const { rows, utilities, components, classes } = rowsOf(css)
  const out = [HEAD]
  const families = [...FAMILIES, { part: "Other", name: "Other", page: null }]
  let part = null
  for (const family of families) {
    const members = rows.filter((row) => (row.family ?? families.at(-1)) === family)
    if (members.length === 0) continue
    if (family.part !== part) {
      part = family.part
      if (part !== "Other") out.push(`## ${part}`)
    }
    if (family.name === "Other") {
      out.push(
        "## Other\n\nNo family in `frontend/scripts/tokens-doc.mjs` claims these yet; give each one there.",
      )
    } else {
      out.push(`### ${family.name}\n\n${family.for} Rules: [\`${family.page}\`](${family.page}).`)
    }
    out.push(table(members))
  }
  out.push(`## Classes

The stylesheet's own utilities: ${utilities.map(code).join(", ")}. The component classes: ${components.map(code).join(", ")}.
The safe-area utilities pad by the device's insets ([\`layout-phone.md\`](layout-phone.md)).

Classes one feature's markup carries: ${classes.map(code).join(", ")}.`)
  out.push(TAIL)
  return `${out.join("\n\n")}\n`
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  process.stdout.write(tokensDoc(readFileSync(STYLESHEET, "utf8")))
}
