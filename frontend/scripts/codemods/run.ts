#!/usr/bin/env bun
/**
 * Runs the 0.9.0 codemods on the paths you own.
 *
 *     bun scripts/codemods/run.ts <transform|all> <paths...> [flags]
 *
 *     --check        change nothing; exit 1 while an old spelling this can rewrite is left
 *     --dry-run      change nothing; print each change and each finding
 *     --suggested    apply the suggestions of review findings as well
 *     --strict       with --check, review findings fail too
 *     --json         print the findings as JSON lines
 *     --no-guard     run a transform whose foundations are not in the tree yet (tests only)
 *     --no-format    skip prettier on the files it changed
 *     --moves <file> the moves table for `imports` (default scripts/moves.json)
 *     --src <dir>    what `@/` and moves.json's paths are relative to (default src)
 *
 * Paths are required: a package runs these on its own files only. The order `all` runs them in,
 * and why, is in README.md beside this file.
 */
import { spawnSync } from "node:child_process"
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs"
import { dirname, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { CODE_FILE, type Finding, type Moves, type Options, type Transform } from "./core"
import { colourVars, PLANNED_COLOURS, registeredColours } from "./colour-vars"
import { containers } from "./containers"
import { controlHeights } from "./control-heights"
import { imports } from "./imports"
import { inkOpacity } from "./ink-opacity"
import { mediaQueries } from "./media-queries"
import { radius } from "./radius"
import { rise } from "./rise"
import { utilities } from "./utilities"
import { vh } from "./vh"
import { zLayers } from "./z-layers"

/** The order `all` runs in: paths first, then the class spellings, then the component APIs. */
export const TRANSFORMS: Transform[] = [
  imports,
  inkOpacity,
  colourVars,
  radius,
  zLayers,
  vh,
  utilities,
  containers,
  mediaQueries,
  controlHeights,
  rise,
]

const HERE = dirname(fileURLToPath(import.meta.url))
export const FRONTEND = resolve(HERE, "../..")
const SKIP_DIRS = new Set([
  "node_modules",
  ".next",
  "out",
  "build",
  "public",
  "test-results",
  "playwright-report",
])

function files(path: string, out: string[]) {
  const stat = statSync(path)
  if (stat.isFile()) {
    if (CODE_FILE.test(path) && !path.endsWith(".d.ts")) out.push(path)
    return
  }
  // This folder names every old spelling in its tables and fixtures.
  if (resolve(path) === HERE) return
  for (const entry of readdirSync(path)) {
    if (SKIP_DIRS.has(entry) || entry.startsWith(".")) continue
    files(join(path, entry), out)
  }
}

type Args = {
  which: string
  paths: string[]
  check: boolean
  dryRun: boolean
  suggested: boolean
  strict: boolean
  json: boolean
  guard: boolean
  format: boolean
  moves?: string
  src?: string
}

function parseArgs(argv: string[]): Args {
  const args: Args = {
    which: "",
    paths: [],
    check: false,
    dryRun: false,
    suggested: false,
    strict: false,
    json: false,
    guard: true,
    format: true,
  }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a === "--check") args.check = true
    else if (a === "--dry-run") args.dryRun = true
    else if (a === "--suggested") args.suggested = true
    else if (a === "--strict") args.strict = true
    else if (a === "--json") args.json = true
    else if (a === "--no-guard") args.guard = false
    else if (a === "--no-format") args.format = false
    else if (a === "--moves") args.moves = argv[++i]
    else if (a === "--src") args.src = argv[++i]
    else if (a.startsWith("--")) throw new Error(`unknown flag ${a}`)
    else if (!args.which) args.which = a
    else args.paths.push(a)
  }
  return args
}

const usage = `usage: bun scripts/codemods/run.ts <${["all", ...TRANSFORMS.map((t) => t.name)].join("|")}> <paths...> [--check] [--dry-run] [--suggested] [--strict] [--json]`

/** Why a transform cannot run in this tree yet, or undefined when it can. */
export function unmet(transform: Transform): string | undefined {
  for (const guard of transform.requires ?? []) {
    const path = join(FRONTEND, guard.file)
    if (!existsSync(path) || !guard.pattern.test(readFileSync(path, "utf8")))
      return `${transform.name} waits until ${guard.why}: ${guard.file} does not have it yet`
  }
  return undefined
}

function describe(f: Finding) {
  const where = `${f.file}:${f.line}:${f.column}`
  const what = f.to !== undefined ? `${f.from} → ${f.to || "(removed)"}` : f.from
  return `${where}  ${f.kind === "rewrite" ? "rewrite" : "review "}  ${f.transform}  ${what}${f.note ? `  (${f.note})` : ""}`
}

/** The changed lines as hunks, from a longest common subsequence of the two files' lines. */
export function changedLines(before: string, after: string) {
  const a = before.split("\n")
  const b = after.split("\n")
  // Trim the common ends first: the table is then only as big as the region that changed.
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  )
    tail++
  const x = a.slice(head, a.length - tail)
  const y = b.slice(head, b.length - tail)
  const keep = Array.from({ length: x.length + 1 }, () => new Uint32Array(y.length + 1))
  for (let i = x.length - 1; i >= 0; i--)
    for (let j = y.length - 1; j >= 0; j--)
      keep[i][j] = x[i] === y[j] ? keep[i + 1][j + 1] + 1 : Math.max(keep[i + 1][j], keep[i][j + 1])
  const out: string[] = []
  let i = 0
  let j = 0
  let open = false
  while (i < x.length || j < y.length) {
    if (i < x.length && j < y.length && x[i] === y[j]) {
      i++
      j++
      open = false
      continue
    }
    if (!open) out.push(`@@ line ${head + i + 1}`)
    open = true
    if (i < x.length && (j === y.length || keep[i + 1][j] >= keep[i][j + 1]))
      out.push(`- ${x[i++]}`)
    else out.push(`+ ${y[j++]}`)
  }
  return out.join("\n")
}

export function main(argv: string[]): number {
  let args: Args
  try {
    args = parseArgs(argv)
  } catch (error) {
    console.error(String(error instanceof Error ? error.message : error))
    console.error(usage)
    return 2
  }
  const chosen = args.which === "all" ? TRANSFORMS : TRANSFORMS.filter((t) => t.name === args.which)
  if (!chosen.length || !args.paths.length) {
    console.error(args.paths.length ? `no transform named ${args.which}` : "name the paths you own")
    console.error(usage)
    return 2
  }
  for (const path of args.paths)
    if (!existsSync(path)) {
      console.error(`no such path: ${path}`)
      return 2
    }

  const runnable: Transform[] = []
  for (const transform of chosen) {
    const why = args.guard ? unmet(transform) : undefined
    if (!why) runnable.push(transform)
    else if (args.which === "all") console.error(`skipped: ${why}`)
    else {
      console.error(why)
      return 2
    }
  }

  const options: Options = { suggested: args.suggested }
  const globals = join(FRONTEND, "src/app/globals.css")
  const registered = existsSync(globals)
    ? registeredColours(readFileSync(globals, "utf8"))
    : new Set<string>()
  // Without the guard (a test, or a dry run ahead of WP-01) the planned families count as registered.
  options.colours = args.guard ? registered : new Set([...registered, ...PLANNED_COLOURS])
  options.srcRoot = args.src ? resolve(args.src) : join(FRONTEND, "src")
  if (runnable.includes(imports)) {
    const path = args.moves ? resolve(args.moves) : join(FRONTEND, "scripts/moves.json")
    if (!existsSync(path)) {
      console.error(`no moves table at ${path}`)
      return 2
    }
    options.moves = JSON.parse(readFileSync(path, "utf8")) as Moves
  }

  const list: string[] = []
  for (const path of args.paths) files(path, list)
  const write = !args.check && !args.dryRun
  const findings: Finding[] = []
  const changed: string[] = []
  for (const path of [...new Set(list)].sort()) {
    const before = readFileSync(path, "utf8")
    let text = before
    const shown = relative(process.cwd(), path) || path
    for (const transform of runnable) {
      const out = transform.apply(text, shown, options)
      text = out.text
      findings.push(...out.findings)
    }
    if (text === before) continue
    changed.push(path)
    if (write) writeFileSync(path, text)
    else if (args.dryRun && !args.json) console.log(`--- ${shown}\n${changedLines(before, text)}\n`)
  }

  if (write && args.format && changed.length) {
    const prettier = spawnSync("bunx", ["prettier", "--write", ...changed.map((p) => resolve(p))], {
      cwd: FRONTEND,
      stdio: "inherit",
    })
    if (prettier.status !== 0) console.error("prettier did not run cleanly on the changed files")
  }

  for (const f of findings) console.log(args.json ? JSON.stringify(f) : describe(f))
  const rewrites = findings.filter((f) => f.kind === "rewrite").length
  const reviews = findings.length - rewrites
  const verb = write ? "rewrote" : "would rewrite"
  console.error(
    `${runnable.map((t) => t.name).join(", ")}: ${verb} ${rewrites} in ${changed.length} files; ${reviews} to review`,
  )
  if (args.check && (changed.length > 0 || (args.strict && reviews > 0))) return 1
  return 0
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url))
  process.exit(main(process.argv.slice(2)))
