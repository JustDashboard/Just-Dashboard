#!/usr/bin/env bun
/**
 * Moves files and exports to where `moves.json` says they live, and rewrites every reference to
 * them: imports under src/, tests/ and scripts/ (`@/…`, relative, and the browser tests'
 * `../../src/lib/…`), `import()` and `import("…")` types, the relative paths a moved test reads
 * from disk, and path mentions in comments, strings and the docs.
 *
 *     bun scripts/move-files.ts --dry-run   # list what it would change, change nothing
 *     bun scripts/move-files.ts             # change it, with `git mv` for the moves, and
 *                                           # print the edited sources with Prettier
 *
 * It is written to be run again: on a tree where the moves are done it changes nothing, and on a
 * tree where someone has since written an import against an old path it points that import at
 * the new one. An entry of `exports` names exports that now live in another file; an import that
 * names some of them is split, and the moved names join an import of their new file where the
 * importer already has one.
 *
 * It parses with the TypeScript compiler the project already depends on. Paths are relative to
 * frontend/; moves.json's are relative to frontend/src.
 */
import ts from "typescript"
import { spawnSync } from "node:child_process"
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  writeFileSync,
} from "node:fs"
import { dirname, join, posix, resolve } from "node:path"
import { fileURLToPath } from "node:url"

export type Moves = {
  files: { from: string; to: string }[]
  exports?: { from: string; to: string; names: string[] }[]
}

export type Options = {
  /** The frontend directory: the one holding src/ and tests/. */
  root: string
  moves: Moves
  /** Markdown files and folders whose path mentions are rewritten. */
  docs?: string[]
  dryRun?: boolean
  /** Use `git mv` (the default when root is inside a work tree). */
  git?: boolean
  log?: (line: string) => void
}

export type Result = {
  moved: [string, string][]
  edited: { file: string; edits: number }[]
  docs: { file: string; edits: number }[]
  warnings: string[]
}

const CODE = /\.(ts|tsx|js|jsx|mjs|cjs)$/
const SCANNED = ["src", "tests", "scripts"]
const SKIPPED_DIRS = new Set([
  "node_modules",
  ".next",
  "test-results",
  "playwright-report",
  "public",
])
/**
 * The script and its table are not rewritten by themselves, and neither is the imports codemod,
 * which reads the same table and names the old paths in its test on purpose.
 */
const SELF = new Set([
  "scripts/move-files.ts",
  "scripts/moves.json",
  "scripts/move-files.test.js",
  "scripts/codemods/imports.ts",
  "scripts/codemods/imports.test.js",
])
const SUFFIXES = [
  "",
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".json",
  "/index.ts",
  "/index.tsx",
  "/index.js",
]
const COMPANIONS = [".test.js", ".test.ts", ".test.tsx"]
const PRINT_WIDTH = 100

function walk(root: string, rel: string, out: string[], keep: (path: string) => boolean) {
  const abs = join(root, rel)
  if (!existsSync(abs)) return
  for (const entry of readdirSync(abs, { withFileTypes: true }).sort((a, b) =>
    a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
  )) {
    const path = rel ? `${rel}/${entry.name}` : entry.name
    if (entry.isDirectory()) {
      if (!SKIPPED_DIRS.has(entry.name)) walk(root, path, out, keep)
    } else if (keep(path)) out.push(path)
  }
}

const stem = (path: string) => path.replace(/\.[^./]+$/, "")
const extOf = (path: string) => path.slice(stem(path).length)
const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
const ups = (spec: string) => (spec.match(/^(\.\.\/)*/)?.[0].length ?? 0) / 3

/** The move table as a function from a path (relative to frontend/) to where it lives now. */
export function pathMap(moves: Moves) {
  const exact = new Map<string, string>()
  const prefixes: [string, string][] = []
  for (const { from, to } of moves.files) {
    if (from.endsWith("*")) {
      prefixes.push([`src/${from.slice(0, -1)}`, `src/${to.slice(0, -1)}`])
      continue
    }
    exact.set(`src/${from}`, `src/${to}`)
    for (const companion of COMPANIONS)
      exact.set(`${stem(`src/${from}`)}${companion}`, `${stem(`src/${to}`)}${companion}`)
  }
  return (path: string) => {
    const hit = exact.get(path)
    if (hit) return hit
    for (const [from, to] of prefixes) {
      if (path.startsWith(from) && !path.slice(from.length).includes("/"))
        return to + path.slice(from.length)
    }
    return path
  }
}

/**
 * The path mentions to rewrite in text: `@/x` without its extension, `x.tsx` with one, a path
 * under `frontend/src/`, and one written from components/ (`deploy/wire.tsx`). Basenames alone
 * are left: `nav.ts` could be any of three.
 */
/**
 * `glob` marks a rule for a folder of prefixed files: its match carries the rest of the name, and
 * the mention is rewritten only for a file the tree has (or had, before it moved), so a sentence
 * about a module that is gone keeps the name it had.
 */
export type MentionRule = { needle: string; pattern: RegExp; to: string; glob?: string }

export function mentionRules(moves: Moves): MentionRule[] {
  const rules: MentionRule[] = []
  const before = String.raw`(^|[^\w.\-/@]|src/|@/)`
  const after = String.raw`(?![\w\-/]|\.\w)`
  const rule = (lead: string, needle: string, tail: string, to: string) =>
    rules.push({ needle, pattern: new RegExp(lead + escape(needle) + tail, "gm"), to })
  const add = (from: string, to: string, withStem: boolean) => {
    for (const [a, b] of withStem
      ? [
          [from, to],
          [stem(from), stem(to)],
        ]
      : [[from, to]]) {
      rule(before, a, after, b)
      const short = a.slice("components/".length)
      if (a.startsWith("components/") && short.includes("/"))
        rule(String.raw`(^|[^\w.\-/@])`, short, after, b)
    }
  }
  for (const { from, to } of moves.files) {
    if (from.endsWith("*")) {
      const needle = from.slice(0, -1)
      rules.push({
        needle,
        pattern: new RegExp(before + escape(needle) + String.raw`(\*|[\w.\-]*\w)`, "gm"),
        to: to.slice(0, -1),
        glob: needle,
      })
      continue
    }
    add(from, to, true)
    for (const companion of COMPANIONS) add(stem(from) + companion, stem(to) + companion, false)
  }
  return rules
}

/** `known(path)` says whether a file under src/ exists, before or after its move. */
export function rewriteMentions(
  text: string,
  rules: MentionRule[],
  known: (path: string) => boolean = () => true,
) {
  let count = 0
  for (const { needle, pattern, to, glob } of rules) {
    if (!text.includes(needle)) continue
    text = text.replace(pattern, (match: string, lead: string, rest?: string) => {
      if (glob !== undefined && rest !== "*" && !known(`src/${glob}${rest}`)) return match
      count++
      return lead + to + (glob !== undefined ? rest : "")
    })
  }
  return { text, count }
}

type Edit = { start: number; end: number; text: string }

type ImportModel = {
  node: ts.ImportDeclaration
  quote: string
  semi: boolean
  typeOnly: boolean
  defaultName?: string
  namespace?: string
  elements: string[]
  spec: string
  target?: string
  suffix: string
  changed: boolean
  removed: boolean
  added: ImportModel[]
}

function renderImport(model: ImportModel) {
  const head = `import ${model.typeOnly ? "type " : ""}`
  const from = ` from ${model.quote}${model.spec}${model.quote}${model.semi ? ";" : ""}`
  const lead = model.defaultName
    ? `${model.defaultName}${model.namespace || model.elements.length ? ", " : ""}`
    : ""
  if (model.namespace) return `${head}${lead}* as ${model.namespace}${from}`
  if (!model.elements.length) return `${head}${model.defaultName}${from}`
  const line = `${head}${lead}{ ${model.elements.join(", ")} }${from}`
  if (line.length <= PRINT_WIDTH) return line
  return `${head}${lead}{\n${model.elements.map((element) => `  ${element},\n`).join("")}}${from}`
}

function stringArgument(node: ts.CallExpression) {
  const first = node.arguments[0]
  return first && (ts.isStringLiteral(first) || ts.isNoSubstitutionTemplateLiteral(first))
    ? first
    : undefined
}

const MODULE_CALLS = new Set(["require", "mock.module", "jest.mock", "vi.mock"])

export function run(options: Options): Result {
  const { root, moves } = options
  const log = options.log ?? ((line: string) => console.log(line))
  const dry = Boolean(options.dryRun)
  const result: Result = { moved: [], edited: [], docs: [], warnings: [] }
  const warn = (line: string) => {
    result.warnings.push(line)
    log(`warn  ${line}`)
  }
  const map = pathMap(moves)

  const files: string[] = []
  for (const top of SCANNED) walk(root, top, files, () => true)
  const existing = new Set(files)
  const code = files.filter((path) => CODE.test(path) && !SELF.has(path))

  // Which files move, and where.
  const moving = new Map<string, string>()
  for (const path of files) {
    const to = map(path)
    if (to === path) continue
    if (existing.has(to)) throw new Error(`${path} would move onto ${to}, which exists`)
    moving.set(path, to)
  }
  for (const { from, to } of moves.files) {
    if (from.endsWith("*")) continue
    if (!existing.has(`src/${from}`) && !existing.has(`src/${to}`))
      warn(`neither src/${from} nor src/${to} exists`)
  }
  const now = (path: string) => moving.get(path) ?? path
  /** The file a path names once the moves are done, whether or not it has moved yet. */
  const settled = (path: string) => {
    const to = map(path)
    return existing.has(path) || existing.has(to) ? to : undefined
  }

  // Which exports now live in another file, by the module's settled path.
  const exportsOf = new Map<string, Map<string, string>>()
  for (const entry of moves.exports ?? []) {
    const from = map(`src/${entry.from}`)
    const names = exportsOf.get(from) ?? new Map<string, string>()
    for (const name of entry.names) names.set(name, map(`src/${entry.to}`))
    exportsOf.set(from, names)
  }

  /** What a module specifier written in `file` points at: the settled file and the suffix the specifier left off. */
  const resolveSpec = (spec: string, file: string) => {
    let base: string
    if (spec.startsWith("@/")) base = `src/${spec.slice(2)}`
    else if (spec.startsWith("./") || spec.startsWith("../"))
      base = posix.normalize(posix.join(posix.dirname(file), spec))
    else return undefined
    if (base.startsWith("../")) return undefined
    for (const suffix of SUFFIXES)
      if (existing.has(base + suffix))
        return { target: map(base + suffix), suffix, moved: map(base + suffix) !== base + suffix }
    for (const suffix of SUFFIXES) {
      const target = settled(base + suffix)
      if (target && target !== base + suffix) return { target, suffix, moved: true }
    }
    return undefined
  }

  const specFor = (target: string, suffix: string, importer: string, original: string) => {
    const path = suffix ? target.slice(0, -suffix.length) : target
    if (original.startsWith("@/")) return `@/${path.slice("src/".length)}`
    let relative = posix.relative(posix.dirname(importer), path)
    if (!relative.startsWith(".")) relative = `./${relative}`
    if (importer.startsWith("src/") && path.startsWith("src/") && ups(relative) > ups(original))
      return `@/${path.slice("src/".length)}`
    return relative
  }
  /** The suffix a specifier for an export's new file leaves off, matching the one it was written with. */
  const suffixFor = (dest: string, suffix: string) => (suffix === "" ? "" : extOf(dest))

  const rules = mentionRules(moves)
  /** A file under src/ the tree has, named with or without its extension, before or after its move. */
  const known = (path: string) =>
    SUFFIXES.some((suffix) => existing.has(path + suffix) || existing.has(map(path + suffix)))
  const contents = new Map<string, string>()

  for (const file of code) {
    const source = readFileSync(join(root, file), "utf8")
    const fileNow = now(file)
    const kind = file.endsWith(".tsx")
      ? ts.ScriptKind.TSX
      : file.endsWith(".ts")
        ? ts.ScriptKind.TS
        : ts.ScriptKind.JSX
    const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, kind)
    const edits: Edit[] = []
    const specifiers = new Set<ts.Node>()

    /** A specifier's new spelling, keeping a query (`./store?copy=1`, a fresh copy of a module). */
    const respell = (text: string) => {
      const query = text.includes("?") ? text.slice(text.indexOf("?")) : ""
      const path = text.slice(0, text.length - query.length)
      const resolved = resolveSpec(path, file)
      if (!resolved || (!resolved.moved && fileNow === file)) return undefined
      const spec = specFor(resolved.target, resolved.suffix, fileNow, path) + query
      return spec === text ? undefined : spec
    }
    const rewriteLiteral = (literal: ts.StringLiteral | ts.NoSubstitutionTemplateLiteral) => {
      specifiers.add(literal)
      const spec = respell(literal.text)
      if (spec === undefined) return
      const quote = source[literal.getStart(sf)]
      edits.push({
        start: literal.getStart(sf),
        end: literal.getEnd(),
        text: `${quote}${spec}${quote}`,
      })
    }

    // Static imports, which may need splitting.
    const models: ImportModel[] = []
    for (const statement of sf.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier))
        continue
      const literal = statement.moduleSpecifier
      specifiers.add(literal)
      const resolved = resolveSpec(literal.text, file)
      const clause = statement.importClause
      const named =
        clause?.namedBindings && ts.isNamedImports(clause.namedBindings)
          ? clause.namedBindings
          : undefined
      const model: ImportModel = {
        node: statement,
        quote: source[literal.getStart(sf)],
        semi: statement.getText(sf).trimEnd().endsWith(";"),
        typeOnly: Boolean(clause?.isTypeOnly),
        defaultName: clause?.name?.text,
        namespace:
          clause?.namedBindings && ts.isNamespaceImport(clause.namedBindings)
            ? clause.namedBindings.name.text
            : undefined,
        elements: named ? named.elements.map((element) => element.getText(sf)) : [],
        spec: literal.text,
        target: resolved?.target,
        suffix: resolved?.suffix ?? "",
        changed: false,
        removed: false,
        added: [],
      }
      if (resolved && (resolved.moved || fileNow !== file)) {
        const spec = specFor(resolved.target, resolved.suffix, fileNow, literal.text)
        if (spec !== literal.text) {
          model.spec = spec
          edits.push({
            start: literal.getStart(sf),
            end: literal.getEnd(),
            text: `${model.quote}${spec}${model.quote}`,
          })
        }
      }
      models.push(model)
    }
    // First what stays in each import, then where the rest goes: an import the file already has
    // of the new home takes them, which it can only once its own names are settled.
    const pending: [ImportModel, Map<string, string[]>][] = []
    for (const model of models) {
      const moved = model.target ? exportsOf.get(model.target) : undefined
      const named = model.node.importClause?.namedBindings
      if (!moved || !named || !ts.isNamedImports(named)) continue
      const groups = new Map<string, string[]>()
      const stay: string[] = []
      named.elements.forEach((element, index) => {
        const dest = moved.get((element.propertyName ?? element.name).text)
        if (!dest) stay.push(model.elements[index])
        else if (dest !== fileNow)
          groups.set(dest, [...(groups.get(dest) ?? []), model.elements[index]])
      })
      if (stay.length === model.elements.length) continue
      model.elements = stay
      model.changed = true
      if (!stay.length && !model.defaultName) model.removed = true
      pending.push([model, groups])
    }
    for (const [model, groups] of pending) {
      for (const [dest, elements] of [...groups].sort(([a], [b]) => (a < b ? -1 : 1))) {
        const into = models.find(
          (other) =>
            other.target === dest &&
            !other.namespace &&
            !other.removed &&
            other.typeOnly === model.typeOnly,
        )
        if (into) {
          into.elements.push(...elements.filter((element) => !into.elements.includes(element)))
          into.changed = true
          continue
        }
        const added: ImportModel = {
          ...model,
          defaultName: undefined,
          namespace: undefined,
          elements,
          spec: specFor(dest, suffixFor(dest, model.suffix), fileNow, model.spec),
          target: dest,
          changed: true,
          removed: false,
          added: [],
        }
        model.added.push(added)
        models.push(added)
      }
    }
    for (const model of models.filter((m) => !models.some((owner) => owner.added.includes(m)))) {
      if (!model.changed && !model.added.length) continue
      const start = model.node.getStart(sf)
      let end = model.node.getEnd()
      for (let i = edits.length - 1; i >= 0; i--)
        if (edits[i].start >= start && edits[i].end <= end) edits.splice(i, 1)
      const parts = [
        ...(model.removed ? [] : [renderImport(model)]),
        ...model.added.filter((added) => added.elements.length).map(renderImport),
      ]
      if (!parts.length && source[end] === "\n") end++
      edits.push({ start, end, text: parts.join("\n") })
    }

    const visit = (node: ts.Node) => {
      if (
        ts.isExportDeclaration(node) &&
        node.moduleSpecifier &&
        ts.isStringLiteral(node.moduleSpecifier)
      ) {
        const literal = node.moduleSpecifier
        specifiers.add(literal)
        const resolved = resolveSpec(literal.text, file)
        const moved = resolved ? exportsOf.get(resolved.target) : undefined
        const clause = node.exportClause
        if (
          resolved &&
          moved &&
          clause &&
          ts.isNamedExports(clause) &&
          clause.elements.some((e) => moved.has((e.propertyName ?? e.name).text))
        ) {
          const groups = new Map<string, string[]>()
          for (const element of clause.elements) {
            const dest = moved.get((element.propertyName ?? element.name).text) ?? resolved.target
            groups.set(dest, [...(groups.get(dest) ?? []), element.getText(sf)])
          }
          const quote = source[literal.getStart(sf)]
          const text = [...groups]
            .map(([dest, elements]) => {
              const spec = specFor(
                dest,
                dest === resolved.target ? resolved.suffix : suffixFor(dest, resolved.suffix),
                fileNow,
                literal.text,
              )
              return `export ${node.isTypeOnly ? "type " : ""}{ ${elements.join(", ")} } from ${quote}${spec}${quote}`
            })
            .join("\n")
          edits.push({ start: node.getStart(sf), end: node.getEnd(), text })
          return
        }
        rewriteLiteral(literal)
        return
      }
      if (ts.isCallExpression(node)) {
        const callee =
          node.expression.kind === ts.SyntaxKind.ImportKeyword
            ? "import"
            : node.expression.getText(sf)
        const literal = stringArgument(node)
        if (literal && (callee === "import" || MODULE_CALLS.has(callee))) {
          const resolved = resolveSpec(literal.text, file)
          if (resolved && exportsOf.has(resolved.target))
            warn(
              `${file}:${sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1} ${callee}("${literal.text}") names a module some exports left; check it by hand`,
            )
          rewriteLiteral(literal)
        }
        // import(`./metrics-store?copy=${n}`): the path is the template's head, up to its query.
        const first = node.arguments[0]
        if (
          callee === "import" &&
          first &&
          ts.isTemplateExpression(first) &&
          first.head.text.includes("?")
        ) {
          specifiers.add(first.head)
          const spec = respell(first.head.text)
          if (spec !== undefined)
            edits.push({
              start: first.head.getStart(sf),
              end: first.head.getEnd(),
              text: `\`${spec}\${`,
            })
        }
      }
      if (
        ts.isImportTypeNode(node) &&
        ts.isLiteralTypeNode(node.argument) &&
        ts.isStringLiteral(node.argument.literal)
      ) {
        rewriteLiteral(node.argument.literal)
      }
      ts.forEachChild(node, visit)
    }
    for (const statement of sf.statements) if (!ts.isImportDeclaration(statement)) visit(statement)

    // A moved file's own relative paths to things on disk: a test reading the backend's testdata.
    if (fileNow !== file) {
      const literals = (node: ts.Node) => {
        if (
          (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) &&
          !specifiers.has(node)
        ) {
          const value = node.text
          if (/^\.\.?\//.test(value) && /[^./]/.test(value)) {
            const target = resolve(root, dirname(file), value)
            if (target.startsWith(resolve(root, "..")) && existsSync(target)) {
              let relative = posix.relative(
                posix.dirname(fileNow),
                posix.relative(root, target).split("\\").join("/"),
              )
              if (!relative.startsWith(".")) relative = `./${relative}`
              if (value.endsWith("/") && !relative.endsWith("/")) relative += "/"
              if (relative !== value) {
                const quote = source[node.getStart(sf)]
                edits.push({
                  start: node.getStart(sf),
                  end: node.getEnd(),
                  text: `${quote}${relative}${quote}`,
                })
              }
            }
          }
        }
        ts.forEachChild(node, literals)
      }
      literals(sf)
      if (
        /import\.meta\.(dir|url)/.test(source) &&
        /join\([^)]*import\.meta\.dir\s*,\s*"\.\."/.test(source)
      )
        warn(`${file} joins import.meta.dir with ".." in parts; check its depth by hand`)
    }

    edits.sort((a, b) => b.start - a.start)
    let text = source
    let last = Infinity
    for (const edit of edits) {
      if (edit.end > last) throw new Error(`${file}: overlapping edits`)
      text = text.slice(0, edit.start) + edit.text + text.slice(edit.end)
      last = edit.start
    }
    const mentioned = rewriteMentions(text, rules, known)
    const count = edits.length + mentioned.count
    if (count) {
      contents.set(file, mentioned.text)
      result.edited.push({ file: fileNow, edits: count })
    }
  }

  // The docs: path mentions only.
  const docs: string[] = []
  for (const entry of options.docs ?? []) {
    if (entry.endsWith(".md")) {
      if (existsSync(entry)) docs.push(entry)
      continue
    }
    const found: string[] = []
    walk(entry, "", found, (path) => path.endsWith(".md") && !path.startsWith("audits/"))
    docs.push(...found.map((path) => join(entry, path)))
  }
  const docEdits = new Map<string, string>()
  for (const doc of docs) {
    const mentioned = rewriteMentions(readFileSync(doc, "utf8"), rules, known)
    if (!mentioned.count) continue
    docEdits.set(doc, mentioned.text)
    result.docs.push({ file: doc, edits: mentioned.count })
  }

  result.moved = [...moving].sort(([a], [b]) => (a < b ? -1 : 1))
  const verb = dry ? "would " : ""
  for (const [from, to] of result.moved) log(`${verb}move  ${from} -> ${to}`)
  for (const { file, edits } of result.edited) log(`${verb}edit  ${file} (${edits})`)
  for (const { file, edits } of result.docs) log(`${verb}edit  ${file} (${edits} mentions)`)
  if (dry) return result

  for (const [file, text] of contents) writeFileSync(join(root, file), text)
  for (const [doc, text] of docEdits) writeFileSync(doc, text)
  const git =
    options.git ??
    spawnSync("git", ["-C", root, "rev-parse", "--is-inside-work-tree"], { encoding: "utf8" })
      .status === 0
  for (const [from, to] of result.moved) {
    mkdirSync(dirname(join(root, to)), { recursive: true })
    if (git && spawnSync("git", ["-C", root, "mv", from, to], { encoding: "utf8" }).status === 0)
      continue
    renameSync(join(root, from), join(root, to))
  }
  return result
}

/**
 * Prints the edited sources the way the repository's Prettier does: an import whose path got
 * shorter fits on one line again, one that got longer breaks over several. Files it did not edit
 * are not read.
 */
export async function format(root: string, files: string[]) {
  const prettier = await import("prettier")
  let changed = 0
  for (const file of files) {
    const path = join(root, file)
    const text = readFileSync(path, "utf8")
    const config = (await prettier.resolveConfig(path)) ?? {}
    const printed = await prettier.format(text, { ...config, filepath: path })
    if (printed === text) continue
    writeFileSync(path, printed)
    changed++
  }
  return changed
}

const here = dirname(fileURLToPath(import.meta.url))

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2)
  const flag = (name: string) => args.includes(name)
  const value = (name: string) => {
    const index = args.indexOf(name)
    return index >= 0 ? args[index + 1] : undefined
  }
  const root = resolve(value("--root") ?? join(here, ".."))
  const moves = JSON.parse(readFileSync(value("--moves") ?? join(here, "moves.json"), "utf8"))
  const docs = flag("--no-docs") ? [] : [join(root, "..", "docs"), join(root, "README.md")]
  const result = run({
    root,
    moves,
    docs,
    dryRun: flag("--dry-run"),
    git: flag("--no-git") ? false : undefined,
  })
  const printed =
    flag("--dry-run") || flag("--no-format")
      ? 0
      : await format(
          root,
          result.edited.map(({ file }) => file),
        )
  console.log(
    `${printed} reprinted by Prettier, ${result.moved.length} files ${flag("--dry-run") ? "to move" : "moved"}, ${result.edited.length} sources and ${result.docs.length} docs ${flag("--dry-run") ? "to edit" : "edited"}, ${result.warnings.length} warnings`,
  )
}
