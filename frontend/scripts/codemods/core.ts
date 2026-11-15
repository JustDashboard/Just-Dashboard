/**
 * What every codemod in this folder shares: parsing a file, finding the strings that are class
 * lists, splitting a class into its variants and its utility, applying edits, and the finding
 * each change or doubt is reported as.
 *
 * A transform is a pure function from a file's text to its new text and findings, so a test can
 * run it on a fixture without a tree. `run.ts` is the only part that reads or writes files.
 */
import ts from "typescript"

export type Finding = {
  file: string
  line: number
  column: number
  transform: string
  /**
   * `rewrite` is applied (or, under --check and --dry-run, would be). `review` is left for a
   * person: the old spelling has no single right answer, and `to` is a suggestion when there is
   * one. `--suggested` applies the suggestions as well.
   */
  kind: "rewrite" | "review"
  from: string
  to?: string
  note?: string
}

export type Moves = {
  files: { from: string; to: string }[]
  exports?: { from: string; to: string; names: string[] }[]
}

export type Options = {
  /** Apply a review finding's suggestion as well. */
  suggested?: boolean
  /** Colour names registered as `--color-<name>` in globals.css. */
  colours?: Set<string>
  /** The file-move table of scripts/moves.json. */
  moves?: Moves
  /** Absolute path of frontend/src, which `@/` and moves.json's paths are relative to. */
  srcRoot?: string
}

export type Output = { text: string; findings: Finding[] }

export type Guard = {
  /** A path relative to frontend/. */
  file: string
  /** What the file has to hold before this transform's output means anything. */
  pattern: RegExp
  why: string
}

export type Transform = {
  name: string
  summary: string
  /** The finding ids this transform does the mechanical part of. */
  closes: string[]
  requires?: Guard[]
  apply(text: string, file: string, options?: Options): Output
}

// ─── Parsing ────────────────────────────────────────────────────────────────

export const CODE_FILE = /\.(tsx|ts|jsx|js|mjs|cjs)$/

export function parse(text: string, file: string): ts.SourceFile {
  const kind = /\.tsx$/.test(file)
    ? ts.ScriptKind.TSX
    : /\.jsx$/.test(file)
      ? ts.ScriptKind.JSX
      : /\.ts$/.test(file)
        ? ts.ScriptKind.TS
        : ts.ScriptKind.JS
  return ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, kind)
}

export function walk(node: ts.Node, visit: (node: ts.Node) => void) {
  visit(node)
  node.forEachChild((child) => walk(child, visit))
}

export function position(sf: ts.SourceFile, at: number) {
  const { line, character } = sf.getLineAndCharacterOfPosition(at)
  return { line: line + 1, column: character + 1 }
}

/** The name a JSX element is written with: `Button`, `svg`, `Select.Trigger`. */
export function tagOf(node: ts.JsxOpeningElement | ts.JsxSelfClosingElement) {
  return node.tagName.getText()
}

export function attributeOf(
  element: ts.JsxOpeningElement | ts.JsxSelfClosingElement,
  name: string,
): ts.JsxAttribute | undefined {
  for (const property of element.attributes.properties)
    if (ts.isJsxAttribute(property) && property.name.getText() === name) return property
  return undefined
}

/** An attribute's value when it is written as a plain string: `size="sm"` or `size={"sm"}`. */
export function stringValue(attribute: ts.JsxAttribute | undefined): string | undefined {
  const value = attribute?.initializer
  if (!value) return undefined
  if (ts.isStringLiteral(value)) return value.text
  if (ts.isJsxExpression(value) && value.expression && ts.isStringLiteralLike(value.expression))
    return value.expression.text
  return undefined
}

// ─── Edits ──────────────────────────────────────────────────────────────────

export type Edit = { start: number; end: number; text: string }

/**
 * Applies edits that do not overlap. Two edits may share a position when one of them is an
 * insertion; the removal goes first, so an attribute that is removed and a word inserted where it
 * began both land.
 */
export function applyEdits(text: string, edits: Edit[]): string {
  const sorted = [...edits].sort((a, b) => b.start - a.start || b.end - a.end)
  let out = text
  let floor = Infinity
  for (const edit of sorted) {
    if (edit.end > floor) throw new Error(`overlapping edits at ${edit.start}..${edit.end}`)
    out = out.slice(0, edit.start) + edit.text + out.slice(edit.end)
    floor = edit.start
  }
  return out
}

// ─── Class tokens ───────────────────────────────────────────────────────────

export type Token = {
  /** Each variant without its colon: `sm`, `data-[state=open]`, `@min-[36rem]/detail`. */
  variants: string[]
  important: "" | "prefix" | "suffix"
  utility: string
}

/** Splits a class at the colons that are not inside an arbitrary value. */
export function parseToken(raw: string): Token {
  const parts: string[] = []
  let depth = 0
  let current = ""
  for (const ch of raw) {
    if (ch === "[" || ch === "(") depth++
    else if ((ch === "]" || ch === ")") && depth > 0) depth--
    if (ch === ":" && depth === 0) {
      parts.push(current)
      current = ""
    } else current += ch
  }
  let utility = current
  let important: Token["important"] = ""
  if (utility.startsWith("!")) {
    important = "prefix"
    utility = utility.slice(1)
  } else if (utility.endsWith("!")) {
    important = "suffix"
    utility = utility.slice(0, -1)
  }
  return { variants: parts, important, utility }
}

export function renderToken(token: Token): string {
  const utility =
    token.important === "prefix"
      ? `!${token.utility}`
      : token.important === "suffix"
        ? `${token.utility}!`
        : token.utility
  return [...token.variants, utility].join(":")
}

export const withUtility = (token: Token, utility: string) => renderToken({ ...token, utility })

const CLASS_TOKEN = /^[!@[a-z0-9*-][^\s"'`]*$/

/**
 * Whether a string's words are all shaped like classes and one of them looks like a utility.
 * Every transform here matches whole classes, so this only has to keep English sentences and
 * identifiers out, the same way the design-system lint's `classLike` does.
 */
export function classLike(tokens: string[]) {
  return (
    tokens.length > 0 &&
    tokens.every((t) => CLASS_TOKEN.test(t)) &&
    tokens.some((t) => /[-:[]/.test(t))
  )
}

// ─── Class literals ─────────────────────────────────────────────────────────

type LiteralNode =
  | ts.StringLiteral
  | ts.NoSubstitutionTemplateLiteral
  | ts.TemplateHead
  | ts.TemplateMiddle
  | ts.TemplateTail

export type ClassLiteral = {
  node: LiteralNode
  /** Where the literal's own text starts and ends in the file, inside its quotes. */
  start: number
  end: number
  text: string
  /** The text runs on into a `${…}` on that side, so a class touching that edge is partial. */
  openLeft: boolean
  openRight: boolean
  /** The JSX element whose className this literal is part of, when it is one. */
  element?: ts.JsxOpeningElement | ts.JsxSelfClosingElement
  tag?: string
  /** The literal sits under a `&&`, `?:` or `||` between itself and the attribute or call. */
  conditional: boolean
}

function isModuleSpecifier(node: ts.Node) {
  const parent = node.parent
  if (!parent) return false
  if (
    (ts.isImportDeclaration(parent) || ts.isExportDeclaration(parent)) &&
    parent.moduleSpecifier === node
  )
    return true
  if (ts.isExternalModuleReference(parent)) return true
  if (ts.isLiteralTypeNode(parent)) return true
  if (ts.isCallExpression(parent) && parent.arguments[0] === node) {
    const callee = parent.expression
    if (callee.kind === ts.SyntaxKind.ImportKeyword) return true
    if (ts.isIdentifier(callee) && callee.text === "require") return true
  }
  if (ts.isPropertyAssignment(parent) && parent.name === node) return true
  return false
}

function contextOf(node: ts.Node) {
  let conditional = false
  for (let at: ts.Node | undefined = node.parent; at; at = at.parent) {
    if (ts.isJsxAttribute(at)) {
      const name = at.name.getText()
      if (name !== "className" && name !== "class") return { conditional }
      const element = at.parent.parent
      return { conditional, element, tag: tagOf(element) }
    }
    if (ts.isBinaryExpression(at)) {
      const op = at.operatorToken.kind
      if (
        op === ts.SyntaxKind.AmpersandAmpersandToken ||
        op === ts.SyntaxKind.BarBarToken ||
        op === ts.SyntaxKind.QuestionQuestionToken
      )
        conditional = true
    }
    if (ts.isConditionalExpression(at)) conditional = true
    if (ts.isFunctionLike(at) || ts.isJsxElement(at) || ts.isJsxSelfClosingElement(at)) break
  }
  return { conditional }
}

/** Every string in the file that reads as a class list, with where it sits. */
export function classLiterals(sf: ts.SourceFile): ClassLiteral[] {
  const out: ClassLiteral[] = []
  const text = sf.text
  walk(sf, (node) => {
    let start: number
    let end: number
    let openLeft = false
    let openRight = false
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      if (isModuleSpecifier(node)) return
      start = node.getStart(sf) + 1
      end = node.getEnd() - 1
    } else if (ts.isTemplateHead(node)) {
      start = node.getStart(sf) + 1
      end = node.getEnd() - 2
      openRight = true
    } else if (ts.isTemplateMiddle(node)) {
      start = node.getStart(sf) + 1
      end = node.getEnd() - 2
      openLeft = openRight = true
    } else if (ts.isTemplateTail(node)) {
      start = node.getStart(sf) + 1
      end = node.getEnd() - 1
      openLeft = true
    } else return
    const body = text.slice(start, end)
    const words = splitWords(body, openLeft, openRight)
      .filter((w) => !w.partial)
      .map((w) => w.text)
    if (!classLike(words)) return
    // A template's context is the whole template's.
    const anchor = ts.isTemplateHead(node)
      ? node.parent
      : ts.isTemplateMiddle(node) || ts.isTemplateTail(node)
        ? node.parent.parent
        : node
    out.push({ node, start, end, text: body, openLeft, openRight, ...contextOf(anchor) })
  })
  return out
}

type Word = { text: string; partial: boolean }
type Piece = { text: string; space: boolean; partial: boolean }

function pieces(body: string, openLeft: boolean, openRight: boolean): Piece[] {
  const parts = body.split(/(\s+)/).filter((p) => p !== "")
  return parts.map((text, i) => {
    const space = /^\s+$/.test(text)
    const partial = !space && ((openLeft && i === 0) || (openRight && i === parts.length - 1))
    return { text, space, partial }
  })
}

function splitWords(body: string, openLeft: boolean, openRight: boolean): Word[] {
  return pieces(body, openLeft, openRight).filter((p) => !p.space)
}

/** What a rule says about one class. */
export type Verdict =
  | { to: string; drop?: string[]; note?: string }
  | { review: string; suggest?: string; drop?: string[] }

export type Rule = (
  token: Token,
  raw: string,
  literal: ClassLiteral,
  words: string[],
) => Verdict | undefined

/**
 * Runs a rule over every class of every class list, and turns its verdicts into edits and
 * findings. A class the rule rewrites to "" is removed with one of the spaces beside it; a list
 * left empty takes its attribute or its `cn()` argument with it.
 */
export function rewriteClasses(
  sf: ts.SourceFile,
  file: string,
  transform: string,
  rule: Rule,
  options: Options = {},
  literals: ClassLiteral[] = classLiterals(sf),
): { edits: Edit[]; findings: Finding[]; changed: ClassLiteral[] } {
  const edits: Edit[] = []
  const findings: Finding[] = []
  const changed: ClassLiteral[] = []
  for (const literal of literals) {
    const parts = pieces(literal.text, literal.openLeft, literal.openRight)
    const words = parts.filter((p) => !p.space && !p.partial).map((p) => p.text)
    const replaced = new Map<number, string>()
    const dropped = new Set<string>()
    let offset = 0
    const at: number[] = []
    for (const p of parts) {
      at.push(offset)
      offset += p.text.length
    }
    parts.forEach((part, i) => {
      if (part.space || part.partial) return
      const verdict = rule(parseToken(part.text), part.text, literal, words)
      if (!verdict) return
      const where = position(sf, literal.start + at[i])
      const base = { file, ...where, transform, from: part.text }
      if ("to" in verdict) {
        if (verdict.to === part.text) return
        replaced.set(i, verdict.to)
        for (const d of verdict.drop ?? []) dropped.add(d)
        findings.push({ ...base, kind: "rewrite", to: verdict.to, note: verdict.note })
      } else if (options.suggested && verdict.suggest !== undefined) {
        replaced.set(i, verdict.suggest)
        for (const d of verdict.drop ?? []) dropped.add(d)
        findings.push({
          ...base,
          kind: "rewrite",
          to: verdict.suggest,
          note: `suggested: ${verdict.review}`,
        })
      } else findings.push({ ...base, kind: "review", to: verdict.suggest, note: verdict.review })
    })
    parts.forEach((part, i) => {
      if (!part.space && !part.partial && !replaced.has(i) && dropped.has(part.text))
        replaced.set(i, "")
    })
    if (replaced.size === 0) continue
    changed.push(literal)
    const next = rebuild(parts, replaced)
    const empty = next.trim() === "" && !literal.openLeft && !literal.openRight
    const whole = empty ? removalOf(literal) : undefined
    if (whole) edits.push(whole)
    else edits.push({ start: literal.start, end: literal.end, text: next })
  }
  return { edits, findings, changed }
}

function rebuild(parts: Piece[], replaced: Map<number, string>) {
  const keep = parts.map((p) => p.text)
  const gone = new Set<number>()
  parts.forEach((part, i) => {
    const to = replaced.get(i)
    if (to === undefined) return
    if (to !== "") {
      keep[i] = to
      return
    }
    gone.add(i)
    // Take the space before it when a kept class precedes it, else the space after it.
    let before = -1
    for (let j = i - 1; j >= 0; j--)
      if (!parts[j].space && !gone.has(j)) {
        before = j
        break
      }
    if (before >= 0 && i > 0 && parts[i - 1].space && !gone.has(i - 1)) gone.add(i - 1)
    else if (i + 1 < parts.length && parts[i + 1].space) gone.add(i + 1)
  })
  return keep.filter((_, i) => !gone.has(i)).join("")
}

/** The edit that removes an emptied class list's attribute or `cn()` argument, when there is one. */
function removalOf(literal: ClassLiteral): Edit | undefined {
  const node = literal.node
  let holder: ts.Node = node
  if (node.parent && ts.isJsxExpression(node.parent)) holder = node.parent
  const parent = holder.parent
  if (parent && ts.isJsxAttribute(parent) && parent.initializer === holder)
    return { start: parent.getFullStart(), end: parent.getEnd(), text: "" }
  if (parent && ts.isCallExpression(parent)) {
    const args = parent.arguments
    const i = args.indexOf(node as ts.Expression)
    if (i < 0 || args.length < 2) return undefined
    if (i < args.length - 1)
      return { start: args[i].getStart(), end: args[i + 1].getStart(), text: "" }
    return { start: args[i - 1].getEnd(), end: args[i].getEnd(), text: "" }
  }
  return undefined
}

/** A transform that is one class rule. */
export function classTransform(
  name: string,
  rule: (file: string, options: Options) => Rule,
): (text: string, file: string, options?: Options) => Output {
  return (text, file, options = {}) => {
    const sf = parse(text, file)
    const { edits, findings } = rewriteClasses(sf, file, name, rule(file, options), options)
    return { text: applyEdits(text, edits), findings }
  }
}

// ─── Imports ────────────────────────────────────────────────────────────────

/**
 * The edit that makes `names` importable from `module`: added to an existing named import of it,
 * or a new import after the last one.
 */
export function addNamedImport(
  sf: ts.SourceFile,
  module: string,
  names: string[],
): Edit | undefined {
  let last: ts.ImportDeclaration | undefined
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    last = statement
    const spec = statement.moduleSpecifier
    const clause = statement.importClause
    if (!ts.isStringLiteral(spec) || spec.text !== module || !clause || clause.isTypeOnly) continue
    const bindings = clause.namedBindings
    if (!bindings || !ts.isNamedImports(bindings)) continue
    const have = new Set(bindings.elements.map((e) => e.name.text))
    const missing = names.filter((n) => !have.has(n))
    if (missing.length === 0) return undefined
    const tail = bindings.elements[bindings.elements.length - 1]
    if (!tail)
      return {
        start: bindings.getStart() + 1,
        end: bindings.getStart() + 1,
        text: ` ${missing.join(", ")} `,
      }
    return { start: tail.getEnd(), end: tail.getEnd(), text: `, ${missing.join(", ")}` }
  }
  const text = `import { ${names.join(", ")} } from "${module}"\n`
  if (!last) return { start: firstStatementStart(sf), end: firstStatementStart(sf), text }
  const lineEnd = sf.text.indexOf("\n", last.getEnd())
  const at = lineEnd < 0 ? sf.text.length : lineEnd + 1
  return { start: at, end: at, text }
}

function firstStatementStart(sf: ts.SourceFile) {
  // After a "use client" directive, which has to stay first.
  for (const statement of sf.statements) {
    if (ts.isExpressionStatement(statement) && ts.isStringLiteral(statement.expression)) continue
    return statement.getStart(sf)
  }
  return sf.text.length
}

/** Whether an identifier is still referenced anywhere but in its own import specifier. */
export function referenced(sf: ts.SourceFile, name: string, ignore: Set<ts.Node>) {
  let found = false
  walk(sf, (node) => {
    if (found || !ts.isIdentifier(node) || node.text !== name || ignore.has(node)) return
    if (ts.isImportSpecifier(node.parent)) return
    found = true
  })
  return found
}

/** The edit that drops one name from its import, or the whole import when it was the last. */
export function removeNamedImport(sf: ts.SourceFile, name: string): Edit | undefined {
  for (const statement of sf.statements) {
    if (!ts.isImportDeclaration(statement)) continue
    const clause = statement.importClause
    const bindings = clause?.namedBindings
    if (!clause || !bindings || !ts.isNamedImports(bindings)) continue
    const elements = bindings.elements
    const i = elements.findIndex((e) => e.name.text === name)
    if (i < 0) continue
    if (elements.length === 1 && !clause.name) {
      const lineEnd = sf.text.indexOf("\n", statement.getEnd())
      return {
        start: statement.getStart(sf),
        end: lineEnd < 0 ? statement.getEnd() : lineEnd + 1,
        text: "",
      }
    }
    if (elements.length === 1)
      return { start: clause.name!.getEnd(), end: bindings.getEnd(), text: "" }
    if (i < elements.length - 1)
      return { start: elements[i].getStart(), end: elements[i + 1].getStart(), text: "" }
    return { start: elements[i - 1].getEnd(), end: elements[i].getEnd(), text: "" }
  }
  return undefined
}
