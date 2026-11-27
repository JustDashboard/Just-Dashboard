import { readdirSync, readFileSync, writeFileSync } from "node:fs"
import { dirname, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import ts from "typescript"

/**
 * A ratchet on the size of the frontend's functions. The 26 Nov 2026 audit (CQ-03) found 51
 * functions in the frontend over 500 lines and twelve over 1,000, the longest of them on the churn
 * list: `FilesPage` alone was 2,030 lines with 82 hook calls, and every change to it was a change
 * to all of it. `function-sizes.json` records each one today with its length. The check in
 * `function-sizes.test.js` fails when a function not in the record crosses the limit or a recorded
 * one grows, and passes, saying the record can come down, when one shrinks.
 *
 * Syntax only, with the frontend's own `typescript`: no program and no type checker, so the whole
 * of `src` parses in about a second. A function's length runs from its first token (not the
 * comment above it) to its closing line, the audit's measure. `bun scripts/function-sizes.ts
 * --write` writes the record from the tree as it stands.
 */

export const LIMIT = 500

const HERE = dirname(fileURLToPath(import.meta.url))
export const ROOT = resolve(HERE, "..")
export const RECORD = join(HERE, "function-sizes.json")

/** `{ "<file under frontend/>": { "<function>": lines } }`, both levels sorted. */
export type Sizes = Record<string, Record<string, number>>

const SOURCE = /\.(ts|tsx|js|jsx|mjs)$/
const SKIPPED = /\.(test|spec)\.[cm]?[jt]sx?$|\.d\.ts$/

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sources(path)
    return SOURCE.test(entry.name) && !SKIPPED.test(entry.name) ? [path] : []
  })
}

type FunctionNode =
  | ts.FunctionDeclaration
  | ts.FunctionExpression
  | ts.ArrowFunction
  | ts.MethodDeclaration
  | ts.ConstructorDeclaration
  | ts.GetAccessorDeclaration
  | ts.SetAccessorDeclaration

function isFunction(node: ts.Node): node is FunctionNode {
  return (
    ts.isFunctionDeclaration(node) ||
    ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) ||
    ts.isMethodDeclaration(node) ||
    ts.isConstructorDeclaration(node) ||
    ts.isGetAccessorDeclaration(node) ||
    ts.isSetAccessorDeclaration(node)
  )
}

const text = (node: ts.Node, sf: ts.SourceFile) => node.getText(sf).replace(/\s+/g, " ")

/**
 * What a reader calls the function: its own name, the variable or property it is assigned to
 * (through `memo`, `forwardRef`, `useCallback` and `useMemo`), the call it is handed to, or the JSX
 * attribute it fills. The same rule as the audit's, so its numbers and these agree.
 */
function ownName(node: FunctionNode, sf: ts.SourceFile): string {
  if (node.name) return text(node.name, sf)
  if (ts.isConstructorDeclaration(node)) return "constructor"
  let parent = node.parent
  while (parent && ts.isParenthesizedExpression(parent)) parent = parent.parent
  if (ts.isVariableDeclaration(parent)) return text(parent.name, sf)
  if (ts.isPropertyAssignment(parent) || ts.isPropertyDeclaration(parent))
    return text(parent.name, sf)
  if (ts.isCallExpression(parent)) {
    const callee = text(parent.expression, sf).slice(0, 40)
    const holder = parent.parent
    if (
      holder &&
      ts.isVariableDeclaration(holder) &&
      /(^|\.)(memo|forwardRef|useCallback|useMemo)$/.test(callee)
    )
      return text(holder.name, sf)
    return `${callee}(…)`
  }
  if (ts.isJsxExpression(parent) && ts.isJsxAttribute(parent.parent))
    return `${text(parent.parent.name, sf)}=…`
  if (ts.isExportAssignment(parent)) return "default"
  return "<anonymous>"
}

function qualifiedName(node: FunctionNode, sf: ts.SourceFile): string {
  const parts = [ownName(node, sf)]
  for (let parent = node.parent; parent; parent = parent.parent) {
    if (isFunction(parent)) parts.unshift(ownName(parent, sf))
    else if (ts.isClassDeclaration(parent) && parent.name) parts.unshift(parent.name.text)
  }
  return parts.join(" › ")
}

/** Every function in one source over `limit` lines, by qualified name. */
export function measureSource(file: string, source: string, limit = LIMIT) {
  const found: Record<string, number> = {}
  // A file no longer than the limit holds no function longer than it, and that is most of them:
  // counting its lines is what keeps the check fast.
  let newlines = 0
  for (
    let at = source.indexOf("\n");
    at >= 0 && newlines < limit;
    at = source.indexOf("\n", at + 1)
  )
    newlines++
  if (newlines < limit) return found
  const kind = file.endsWith(".tsx")
    ? ts.ScriptKind.TSX
    : file.endsWith(".jsx")
      ? ts.ScriptKind.JSX
      : file.endsWith(".ts")
        ? ts.ScriptKind.TS
        : ts.ScriptKind.JS
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, kind)
  const line = (at: number) => sf.getLineAndCharacterOfPosition(at).line + 1
  const visit = (node: ts.Node) => {
    if (isFunction(node) && node.body) {
      const lines = line(node.end) - line(node.getStart(sf)) + 1
      if (lines > limit) {
        // Two long callbacks handed to the same call in one function share a name; the later
        // one is told apart by its place among the long ones, which a short one cannot move.
        const name = qualifiedName(node, sf)
        let key = name
        for (let n = 2; key in found; n++) key = `${name} #${n}`
        found[key] = lines
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return found
}

/** Every function under `frontend/src` over the limit, tests left out. */
export function measure(root = ROOT, limit = LIMIT): Sizes {
  const sizes: Sizes = {}
  for (const path of sources(join(root, "src")).sort()) {
    const found = measureSource(path, readFileSync(path, "utf8"), limit)
    if (Object.keys(found).length > 0) sizes[relative(root, path)] = sorted(found)
  }
  return sizes
}

function sorted<T>(record: Record<string, T>): Record<string, T> {
  return Object.fromEntries(Object.entries(record).sort(([a], [b]) => (a < b ? -1 : 1)))
}

export type Change = { file: string; name: string; was?: number; now?: number }

/**
 * What the tree does to the record. `crossed` is a function the record does not hold that is now
 * over the limit, `grown` a recorded one that is longer: either fails. `shrunk` and `gone` (shorter,
 * or no longer over the limit, renamed or removed) pass and mean the record can come down.
 */
export function compare(record: Sizes, now: Sizes, limit = LIMIT) {
  const crossed: Change[] = []
  const grown: Change[] = []
  const shrunk: Change[] = []
  const gone: Change[] = []
  for (const [file, functions] of Object.entries(now)) {
    for (const [name, lines] of Object.entries(functions)) {
      const was = record[file]?.[name]
      if (was === undefined) {
        if (lines > limit) crossed.push({ file, name, now: lines })
      } else if (lines > was) grown.push({ file, name, was, now: lines })
      else if (lines < was) shrunk.push({ file, name, was, now: lines })
    }
  }
  for (const [file, functions] of Object.entries(record)) {
    for (const [name, was] of Object.entries(functions)) {
      if (now[file]?.[name] === undefined) gone.push({ file, name, was })
    }
  }
  return { crossed, grown, shrunk, gone }
}

export function readRecord(path = RECORD): Sizes {
  return JSON.parse(readFileSync(path, "utf8")).functions
}

export function writeRecord(sizes: Sizes, path = RECORD) {
  const body = {
    about: `Functions in frontend/src over ${LIMIT} lines, which may only get shorter. Checked by scripts/function-sizes.test.js; rewrite with bun scripts/function-sizes.ts --write.`,
    limit: LIMIT,
    functions: sizes,
  }
  writeFileSync(path, JSON.stringify(body, null, 2) + "\n")
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const now = measure()
  if (process.argv.includes("--write")) {
    writeRecord(now)
    const count = Object.values(now).reduce((n, functions) => n + Object.keys(functions).length, 0)
    console.log(`${count} functions over ${LIMIT} lines recorded in ${relative(ROOT, RECORD)}`)
  } else {
    console.log(JSON.stringify(compare(readRecord(), now), null, 2))
  }
}
