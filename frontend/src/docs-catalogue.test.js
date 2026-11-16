// What keeps the UI docs current (docs/internal/frontend).
//
// Nothing read those pages, so they named components that had been renamed or deleted, the
// catalogue missed exports added after it, and the token page listed values the stylesheet had
// moved on from. Four checks:
//
//   - every export of a shared folder has a row in the catalogue (components/README.md);
//   - every name in a catalogue row is an export, unless the row says it is new or retired;
//   - every backticked PascalCase name in the docs exists in the code, or decisions/ marks it
//     "(removed)";
//   - foundations/tokens.md is exactly what frontend/scripts/tokens-doc.mjs prints.
//
// The first three are a ratchet. docs-catalogue.baseline.json lists the misses there were when
// the check arrived (0.9.0, WP-40), by name, and the check fails on a miss that is not listed and
// on a listed name that is no longer a miss, so the list only shrinks. WP-41 writes the missing
// rows and fixes the stale names, and empties it. The token page has no baseline.

import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative, sep } from "node:path"
import { PAGE, STYLESHEET, tokensDoc } from "../scripts/tokens-doc.mjs"

const SRC = import.meta.dir
const REPO = join(SRC, "..", "..")
const DOCS = join(REPO, "docs", "internal", "frontend")
const CATALOGUE = join(DOCS, "components", "README.md")
const BASELINE = join(SRC, "docs-catalogue.baseline.json")

// The folders the catalogue covers, as its own introduction defines "shared".
const SHARED = [/^components\/[^/]+\.tsx?$/, /^components\/(ui|wire|settings|shell)\//, /^hooks\//]

function filesUnder(dir, keep) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...filesUnder(path, keep))
    else if (keep(entry.name)) out.push(path)
  }
  return out
}

const posix = (path) => path.split(sep).join("/")

// A module's exported names. The source is Prettier's, so a declaration starts its line; checked
// against the TypeScript parser over every file in src when this was written, with no difference.
// `export *` adds nothing here: the names belong to the module it re-exports, which is read too.
const EXPORTED_DECLARATION =
  /^export\s+(?:declare\s+)?(?:default\s+)?(?:async\s+)?(?:abstract\s+)?(?:function\*?|const|let|var|class|type|interface|enum)\s+([A-Za-z_$][\w$]*)/gm
const EXPORT_LIST = /^export\s+(?:type\s+)?\{([^}]*)\}/gm
// Any PascalCase declaration, exported or not: a doc may describe a file's own parts.
const DECLARATION =
  /^\s*(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:async\s+)?(?:abstract\s+)?(?:function\*?|const|let|var|class|type|interface|enum)\s+([A-Z][\w$]*)/gm
// What the code takes from a package (`Link`, `Dialog`): real names that are not ours.
const IMPORT =
  /^import\s+(?:type\s+)?(?:([A-Za-z_$][\w$]*)\s*,?\s*)?(?:\{([^}]*)\})?(?:\*\s+as\s+([\w$]+))?\s*from\s+"([^"]+)"/gm

function listed(list) {
  return list
    .split(",")
    .map((part) => part.trim().replace(/^type\s+/, ""))
    .filter(Boolean)
}

function exportsOf(text) {
  const names = new Set()
  for (const m of text.matchAll(EXPORTED_DECLARATION)) names.add(m[1])
  for (const m of text.matchAll(EXPORT_LIST)) {
    for (const part of listed(m[1]))
      names.add(
        part
          .split(/\s+as\s+/)
          .pop()
          .trim(),
      )
  }
  return names
}

function declaredIn(text) {
  return new Set([...text.matchAll(DECLARATION)].map((m) => m[1]))
}

function importedFromPackages(text) {
  const names = new Set()
  for (const m of text.matchAll(IMPORT)) {
    if (m[4].startsWith(".") || m[4].startsWith("@/")) continue
    if (m[1]) names.add(m[1])
    if (m[3]) names.add(m[3])
    for (const part of listed(m[2] ?? "")) {
      for (const name of part.split(/\s+as\s+/)) names.add(name.trim())
    }
  }
  return names
}

// The catalogue's rows: the names in each row's first cell, and its status (the last cell). The
// icon names under "### Icons" are rows too.
function catalogueOf(markdown) {
  const rows = []
  for (const line of markdown.split("\n")) {
    if (!line.startsWith("| `")) continue
    const cells = line.split("|").slice(1, -1)
    const names = [...cells[0].matchAll(/`([^`]+)`/g)].map((m) => m[1])
    rows.push({ line, names, status: cells.at(-1).trim() })
  }
  const icons = markdown.match(/^### Icons\n([\s\S]*?)(?=^## )/m)?.[1] ?? ""
  const iconNames = [...icons.matchAll(/`([A-Za-z][A-Za-z0-9]*)`/g)].map((m) => m[1])
  return { rows, iconNames }
}

// A status that excuses a row's names from being exports: a package that has not landed yet, or
// a row kept to say what replaced a retired export.
const excused = (status) => /^(new|retired)\b/.test(status)

// The PascalCase names a page writes in backticks outside fenced code, with their lines: a bare
// name (`Panel`, `Panel.Header`, `useless()` is not one) or the tag of an element (`<Panel plain>`).
// All capitals (`README`, `URL`) is an abbreviation, not a name.
function backtickedNames(markdown) {
  const found = []
  let fenced = false
  markdown.split("\n").forEach((line, index) => {
    if (line.trimStart().startsWith("```")) {
      fenced = !fenced
      return
    }
    if (fenced) return
    for (const m of line.matchAll(/`([^`\n]+)`/g)) {
      const text = m[1].trim()
      const element =
        text.match(/^<([A-Z][A-Za-z0-9]*)[\s>/.]/) ?? text.match(/^<([A-Z][A-Za-z0-9]*)$/)
      const bare = text.match(/^([A-Z][A-Za-z0-9]*)(?:\.[A-Za-z_]\w*)*(?:\(\))?$/)
      const name = (element ?? bare)?.[1]
      if (name && /[a-z]/.test(name)) found.push({ name, line: index + 1, text: line })
    }
  })
  return found
}

// Names decisions/ records as gone: "`ShareBar` (removed …)".
function removedIn(markdown) {
  return new Set(
    [...markdown.matchAll(/`<?([A-Z][A-Za-z0-9]*)>?`\s*\(removed\b/g)].map((m) => m[1]),
  )
}

// The ratchet: what is missed now against what was missed when the baseline was written.
function ratchet(misses, baseline) {
  const now = new Set(misses)
  const before = new Set(baseline)
  return {
    added: [...now].filter((name) => !before.has(name)).sort(),
    fixed: [...before].filter((name) => !now.has(name)).sort(),
  }
}

function read(path) {
  return readFileSync(path, "utf8")
}

// Which of these words the backend's Go source (tests aside) has. `git grep` answers in a tenth of
// a second where reading the 22 MB of it took most of one; outside a checkout, it is read.
function inBackend(names) {
  if (names.length === 0) return new Set()
  const args = ["grep", "-howF", ...names.flatMap((n) => ["-e", n]), "--", "backend/*.go"]
  const grep = Bun.spawnSync(["git", ...args, ":!*_test.go"], { cwd: REPO })
  if (grep.exitCode === 0 || grep.exitCode === 1) {
    return new Set(grep.stdout.toString().split("\n").filter(Boolean))
  }
  const backend = filesUnder(join(REPO, "backend"), (name) => /(?<!_test)\.go$/.test(name))
    .map(read)
    .join("\n")
  return new Set(names.filter((name) => new RegExp(`\\b${name}\\b`).test(backend)))
}

const sources = filesUnder(SRC, (name) => /\.tsx?$/.test(name) && !/\.test\./.test(name)).map(
  (path) => ({ path: posix(relative(SRC, path)), text: read(path) }),
)
const exported = new Set(sources.flatMap((s) => [...exportsOf(s.text)]))
const known = new Set(exported)
for (const s of sources) {
  for (const name of declaredIn(s.text)) known.add(name)
  for (const name of importedFromPackages(s.text)) known.add(name)
}
const docs = filesUnder(DOCS, (name) => name.endsWith(".md")).map((path) => ({
  path: posix(relative(DOCS, path)),
  text: read(path),
}))
const catalogue = catalogueOf(read(CATALOGUE))
const baseline = JSON.parse(read(BASELINE))

// Fails on a miss the baseline does not list, and on a listed name that is no longer a miss.
// `detail` says where a new miss is.
function expectRatchet(misses, list, what, detail = (name) => name) {
  const { added, fixed } = ratchet(misses, baseline[list])
  if (added.length > 0) {
    throw new Error(
      `New: ${what}. Fix the docs, or, only where another package owns the page, add the name ` +
        `to "${list}" in src/docs-catalogue.baseline.json:\n  ${added.map(detail).join("\n  ")}`,
    )
  }
  if (fixed.length > 0) {
    throw new Error(
      `No longer missed; take them out of "${list}" in src/docs-catalogue.baseline.json:\n  ` +
        fixed.join("\n  "),
    )
  }
}

describe("the catalogue", () => {
  test("has a row for every export of a shared folder", () => {
    const rowNames = new Set([...catalogue.rows.flatMap((r) => r.names), ...catalogue.iconNames])
    const misses = []
    for (const s of sources) {
      if (!SHARED.some((shared) => shared.test(s.path))) continue
      for (const name of exportsOf(s.text)) {
        if (!rowNames.has(name)) misses.push(`${name} (${s.path})`)
      }
    }
    expectRatchet(misses, "noRow", "a shared export with no row in components/README.md")
  })

  test("names only exports, except in a row that is new or retired", () => {
    const misses = catalogue.rows
      .filter((row) => !excused(row.status))
      .flatMap((row) => row.names.filter((name) => !exported.has(name)))
    expectRatchet(misses, "rowNotExport", "a catalogue row naming something that is not an export")
  })
})

describe("the docs", () => {
  test("name only components that exist, or that decisions/ marks removed", () => {
    const removed = new Set(
      docs.filter((d) => d.path.startsWith("decisions/")).flatMap((d) => [...removedIn(d.text)]),
    )
    const excusedLines = new Set(catalogue.rows.filter((r) => excused(r.status)).map((r) => r.line))
    const unknown = new Map()
    for (const doc of docs) {
      for (const found of backtickedNames(doc.text)) {
        if (known.has(found.name) || removed.has(found.name) || found.name in globalThis) continue
        if (doc.path === "components/README.md" && excusedLines.has(found.text)) continue
        unknown.set(found.name, [...(unknown.get(found.name) ?? []), `${doc.path}:${found.line}`])
      }
    }
    // A name the backend writes (an sshd or a systemd setting, a state the API reports) is real
    // too. Asked only for what the frontend did not account for.
    for (const name of inBackend([...unknown.keys()])) unknown.delete(name)
    expectRatchet(
      [...unknown.keys()],
      "unknownName",
      'a backticked name the code does not have (or mark a gone one "(removed)" in decisions/)',
      (name) => `${name} (${unknown.get(name).slice(0, 3).join(", ")})`,
    )
  })
})

describe("the token reference", () => {
  test("is what tokens-doc.mjs prints from globals.css", () => {
    // On a difference: from frontend/, bun scripts/tokens-doc.mjs > ../docs/internal/frontend/foundations/tokens.md
    expect(read(PAGE)).toBe(tokensDoc(read(STYLESHEET)))
  })
})

describe("the checks themselves", () => {
  test("read every form of export", () => {
    const text = [
      "export function Panel() {}",
      "export default function Page() {}",
      "export const ROW_DIM = 1",
      "export type Tone = 1",
      "export interface Props {}",
      'export { a, b as Bee, type C } from "./x"',
      "function Local() {}",
    ].join("\n")
    expect([...exportsOf(text)].sort()).toEqual(
      ["Bee", "C", "Page", "Panel", "Props", "ROW_DIM", "Tone", "a"].sort(),
    )
  })

  test("read a catalogue row's names and status, and the icons", () => {
    const md = [
      "| `Row`, `RowList` | [List](list-row.md) | `components/row-list.tsx` | A row. | keep |",
      "| `Old` | x | y | z | retired (0.9.0) |",
      "### Icons",
      "",
      "`Check`, `Cross`",
      "",
      "## Families",
    ].join("\n")
    const { rows, iconNames } = catalogueOf(md)
    expect(rows.map((r) => [r.names, r.status])).toEqual([
      [["Row", "RowList"], "keep"],
      [["Old"], "retired (0.9.0)"],
    ])
    expect(iconNames).toEqual(["Check", "Cross"])
    expect(excused("new (WP-33)")).toBe(true)
    expect(excused("keep")).toBe(false)
  })

  test("take PascalCase names from backticks, not from code fences or abbreviations", () => {
    const md = [
      "A `Panel` with `<PanelHeader count={3}>` and `Panel.Body`, not `README` or `cn`.",
      "```tsx",
      "<Hidden />",
      "```",
      "`two words` and `ShareBar` (removed in 0.8)",
    ].join("\n")
    expect(backtickedNames(md).map((f) => [f.name, f.line])).toEqual([
      ["Panel", 1],
      ["PanelHeader", 1],
      ["Panel", 1],
      ["ShareBar", 5],
    ])
    expect([...removedIn(md)]).toEqual(["ShareBar"])
  })

  test("let the baseline only shrink", () => {
    expect(ratchet(["A", "B"], ["B", "C"])).toEqual({ added: ["A"], fixed: ["C"] })
    expect(ratchet([], [])).toEqual({ added: [], fixed: [] })
  })
})
