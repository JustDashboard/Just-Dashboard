/**
 * imports: an import names the file its export lives in now (WP-04's moves).
 *
 * It reads the same table as scripts/move-files.ts, `scripts/moves.json`, and only rewrites
 * import paths, inside the paths it is given: it is for a branch or a file written against the
 * old layout after the move landed. `files` re-points a module that moved (`lib/network-*` →
 * `lib/network/*` included); `exports` splits an import of names that left a file
 * (`Section` from `@/components/page` → `@/components/section`).
 *
 * A path keeps its spelling: `@/…` stays an alias, a relative path stays relative, and an
 * extension stays written when it was. A namespace import, or an `import()` of a module that
 * some exports left, is reported, since what it reaches cannot be split by its text.
 */
import ts from "typescript"
import { dirname, relative, resolve } from "node:path"
import {
  applyEdits,
  type Edit,
  type Finding,
  type Moves,
  parse,
  position,
  type Transform,
  walk,
} from "./core"

const EXT = /\.(tsx|ts|jsx|js|mjs|cjs)$/
const strip = (path: string) => path.replace(EXT, "").replace(/\/index$/, "")

type Table = {
  settle(path: string): { path: string; ext?: string } | undefined
  exportsOf: Map<string, Map<string, string>>
}

export function table(moves: Moves): Table {
  const exact = new Map<string, { path: string; ext?: string }>()
  const globs: { from: string; to: string }[] = []
  for (const { from, to } of moves.files) {
    if (from.endsWith("*")) globs.push({ from: from.slice(0, -1), to: to.slice(0, -1) })
    else exact.set(strip(from), { path: strip(to), ext: EXT.exec(to)?.[0] })
  }
  const settle = (path: string) => {
    const hit = exact.get(path)
    if (hit) return hit
    for (const glob of globs)
      if (path.startsWith(glob.from) && !path.slice(glob.from.length).includes("/"))
        return { path: glob.to + path.slice(glob.from.length) }
    return undefined
  }
  const exportsOf = new Map<string, Map<string, string>>()
  for (const entry of moves.exports ?? []) {
    const from = settle(strip(entry.from))?.path ?? strip(entry.from)
    const names = exportsOf.get(from) ?? new Map<string, string>()
    for (const name of entry.names) names.set(name, strip(entry.to))
    exportsOf.set(from, names)
  }
  return { settle, exportsOf }
}

type Resolved = { target: string; moved: boolean; spell: (target: string) => string }

function resolver(file: string, srcRoot: string, moves: Table) {
  const fileAbs = resolve(file)
  return (spec: string): Resolved | undefined => {
    if (spec.includes("?")) return undefined
    let rel: string
    const alias = spec.startsWith("@/")
    if (alias) rel = spec.slice(2)
    else if (spec.startsWith("./") || spec.startsWith("../")) {
      rel = relative(srcRoot, resolve(dirname(fileAbs), spec))
      if (rel.startsWith("..")) return undefined
    } else return undefined
    const ext = EXT.exec(rel)?.[0]
    const bare = strip(rel)
    const settled = moves.settle(bare)
    const target = settled?.path ?? bare
    const spell = (to: string) => {
      const suffix = ext ? (to === target ? (settled?.ext ?? ext) : ext) : ""
      if (alias) return `@/${to}${suffix}`
      let out = relative(dirname(fileAbs), resolve(srcRoot, to)) + suffix
      if (!out.startsWith(".")) out = `./${out}`
      return out
    }
    return { target, moved: settled !== undefined, spell }
  }
}

export const imports: Transform = {
  name: "imports",
  summary: "re-point imports to the moved and split files of scripts/moves.json",
  closes: [],
  requires: [
    {
      file: "scripts/moves.json",
      pattern: /"files"/,
      why: "the moves table exists (WP-04)",
    },
  ],
  apply(text, file, options = {}) {
    if (!options.moves) throw new Error("imports needs the moves table (--moves)")
    const srcRoot = options.srcRoot ?? resolve("src")
    const moves = table(options.moves)
    const resolve_ = resolver(file, srcRoot, moves)
    const sf = parse(text, file)
    const edits: Edit[] = []
    const findings: Finding[] = []
    const note = (node: ts.Node, kind: Finding["kind"], from: string, to?: string, why?: string) =>
      findings.push({
        file,
        ...position(sf, node.getStart(sf)),
        transform: "imports",
        kind,
        from,
        to,
        note: why,
      })

    const respell = (literal: ts.StringLiteralLike) => {
      const resolved = resolve_(literal.text)
      if (!resolved?.moved) return resolved
      const to = resolved.spell(resolved.target)
      if (to !== literal.text) {
        note(literal, "rewrite", literal.text, to)
        edits.push({ start: literal.getStart(sf) + 1, end: literal.getEnd() - 1, text: to })
      }
      return resolved
    }

    /** Splits named elements by the file each now lives in; undefined when none moved. */
    const split = (
      resolved: Resolved,
      elements: readonly (ts.ImportSpecifier | ts.ExportSpecifier)[],
    ): Map<string, string[]> | undefined => {
      const moved = moves.exportsOf.get(resolved.target)
      if (!moved || !elements.some((e) => moved.has((e.propertyName ?? e.name).text)))
        return undefined
      const groups = new Map<string, string[]>([[resolved.target, []]])
      for (const e of elements) {
        const dest = moved.get((e.propertyName ?? e.name).text) ?? resolved.target
        groups.set(dest, [...(groups.get(dest) ?? []), e.getText(sf)])
      }
      return groups
    }

    for (const statement of sf.statements) {
      if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
        const literal = statement.moduleSpecifier
        const resolved = resolve_(literal.text)
        if (!resolved) continue
        const clause = statement.importClause
        const bindings = clause?.namedBindings
        const groups =
          bindings && ts.isNamedImports(bindings) ? split(resolved, bindings.elements) : undefined
        if (!groups) {
          if (bindings && ts.isNamespaceImport(bindings) && moves.exportsOf.has(resolved.target))
            note(
              statement,
              "review",
              statement.getText(sf),
              undefined,
              "a namespace import of a file some exports left",
            )
          respell(literal)
          continue
        }
        const quote = sf.text[literal.getStart(sf)]
        const type = clause!.isTypeOnly ? "type " : ""
        const lines: string[] = []
        for (const [dest, names] of groups) {
          const head = dest === resolved.target && clause!.name ? clause!.name.text : ""
          if (!names.length && !head) continue
          const named = names.length ? `{ ${names.join(", ")} }` : ""
          const what = [head, named].filter(Boolean).join(", ")
          lines.push(`import ${type}${what} from ${quote}${resolved.spell(dest)}${quote}`)
        }
        const to = lines.join("\n")
        note(statement, "rewrite", statement.getText(sf), to)
        edits.push({ start: statement.getStart(sf), end: statement.getEnd(), text: to })
        continue
      }
      if (
        ts.isExportDeclaration(statement) &&
        statement.moduleSpecifier &&
        ts.isStringLiteral(statement.moduleSpecifier)
      ) {
        const literal = statement.moduleSpecifier
        const resolved = resolve_(literal.text)
        if (!resolved) continue
        const clause = statement.exportClause
        const groups =
          clause && ts.isNamedExports(clause) ? split(resolved, clause.elements) : undefined
        if (!groups) {
          if (!clause && moves.exportsOf.has(resolved.target))
            note(
              statement,
              "review",
              statement.getText(sf),
              undefined,
              "export * from a file some exports left",
            )
          respell(literal)
          continue
        }
        const quote = sf.text[literal.getStart(sf)]
        const type = statement.isTypeOnly ? "type " : ""
        const to = [...groups]
          .filter(([, names]) => names.length)
          .map(
            ([dest, names]) =>
              `export ${type}{ ${names.join(", ")} } from ${quote}${resolved.spell(dest)}${quote}`,
          )
          .join("\n")
        note(statement, "rewrite", statement.getText(sf), to)
        edits.push({ start: statement.getStart(sf), end: statement.getEnd(), text: to })
      }
    }

    // import("…"), require("…"), mock.module("…") and import("…") types: the path only.
    walk(sf, (node) => {
      let literal: ts.StringLiteralLike | undefined
      if (ts.isCallExpression(node)) {
        const callee = node.expression
        const name =
          callee.kind === ts.SyntaxKind.ImportKeyword
            ? "import"
            : ts.isIdentifier(callee)
              ? callee.text
              : ts.isPropertyAccessExpression(callee)
                ? callee.getText(sf)
                : ""
        if (!["import", "require", "mock.module", "jest.mock", "vi.mock"].includes(name)) return
        const arg = node.arguments[0]
        if (arg && ts.isStringLiteralLike(arg)) literal = arg
      } else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) {
        const arg = node.argument.literal
        if (ts.isStringLiteral(arg)) literal = arg
      }
      if (!literal) return
      const resolved = respell(literal)
      if (resolved && moves.exportsOf.has(resolved.target))
        note(
          node,
          "review",
          node.getText(sf),
          undefined,
          "names a module some exports left; check what it reaches",
        )
    })

    findings.sort((a, b) => a.line - b.line || a.column - b.column)
    return { text: applyEdits(text, edits), findings }
  },
}
