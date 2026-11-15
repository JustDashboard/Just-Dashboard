/**
 * media-queries: script asks for a breakpoint by name (FND-14).
 *
 *     useMediaQuery("(min-width: 1024px)")   → useBreakpoint("lg")
 *     useMediaQuery("(max-width: 1023px)")   → useBelow("lg")
 *     className="min-[1280px]:grid"          → className="xl:grid"
 *
 * The hooks come from `@/lib/breakpoints` (WP-01), and `useMediaQuery` leaves the import when
 * nothing else in the file uses it. A width that is not one of the five breakpoints (1100, 1400,
 * 1600, 479) is reported: a block whose layout depends on it is asking about its container, not
 * the viewport. A query held in a constant, or built in a template, is reported for the same
 * reason; so is a breakpoint written into a `matchMedia` call.
 */
import ts from "typescript"
import {
  addNamedImport,
  applyEdits,
  classLiterals,
  type Edit,
  type Finding,
  parse,
  position,
  referenced,
  removeNamedImport,
  renderToken,
  rewriteClasses,
  type Rule,
  type Transform,
  walk,
} from "./core"

export const BREAKPOINTS: Record<string, number> = {
  sm: 640,
  md: 768,
  lg: 1024,
  xl: 1280,
  "2xl": 1536,
}
const BY_WIDTH = new Map(Object.entries(BREAKPOINTS).map(([name, px]) => [px, name]))

const MODULE = "@/lib/breakpoints"

type Query = { hook: "useBreakpoint" | "useBelow"; name: string } | { off: number } | undefined

const px = (amount: string, unit: string) => Number(amount) * (unit === "px" ? 1 : 16)

/** What a media query string asks, if it is one viewport width. */
export function readQuery(query: string): Query {
  const q = query.trim()
  let m = /^\(\s*(?:min-width\s*:|width\s*>=)\s*(\d+(?:\.\d+)?)(px|rem|em)\s*\)$/.exec(q)
  if (m) {
    const width = px(m[1], m[2])
    const name = BY_WIDTH.get(width)
    return name ? { hook: "useBreakpoint", name } : { off: width }
  }
  m = /^\(\s*max-width\s*:\s*(\d+(?:\.\d+)?)(px|rem|em)\s*\)$/.exec(q)
  if (m) {
    const width = px(m[1], m[2])
    const name = m[2] === "px" ? BY_WIDTH.get(width + 1) : undefined
    return name ? { hook: "useBelow", name } : { off: width }
  }
  m = /^\(\s*width\s*<\s*(\d+(?:\.\d+)?)(px|rem|em)\s*\)$/.exec(q)
  if (m) {
    const width = px(m[1], m[2])
    const name = BY_WIDTH.get(width)
    return name ? { hook: "useBelow", name } : { off: width }
  }
  return undefined
}

const WIDTH_STRING =
  /\((?:min|max)-width\s*:\s*\d+(?:\.\d+)?(?:px|rem|em)\s*\)|\(width\s*[<>]=?\s*\d+/

/** `min-[1024px]:` → `lg:` and `max-[1023px]:` → `max-lg:`; other viewport widths reported. */
const variantRule = (): Rule => (token, raw) => {
  let off: string | undefined
  const variants = token.variants.map((v) => {
    const m = /^(min|max)-\[(\d+(?:\.\d+)?)(px|rem)\]$/.exec(v)
    if (!m) return v
    const width = px(m[2], m[3])
    const name =
      m[1] === "min" ? BY_WIDTH.get(width) : BY_WIDTH.get(m[3] === "px" ? width + 1 : width)
    if (!name) {
      off = `${width}px`
      return v
    }
    return m[1] === "min" ? name : `max-${name}`
  })
  if (off) return { review: `${off} is not a breakpoint: a container query?` }
  const to = renderToken({ ...token, variants })
  return to === raw ? undefined : { to }
}

export const mediaQueries: Transform = {
  name: "media-queries",
  summary:
    "useMediaQuery('(min-width: Npx)') to useBreakpoint; viewport widths off the breakpoints reported",
  closes: ["FND-14"],
  requires: [
    {
      file: "src/lib/breakpoints.ts",
      pattern: /export function useBreakpoint[\s\S]*export function useBelow/,
      why: "lib/breakpoints.ts exports useBreakpoint and useBelow (WP-01)",
    },
  ],
  apply(text, file, options = {}) {
    const sf = parse(text, file)
    const findings: Finding[] = []
    const edits: Edit[] = []
    const hooks = new Set<string>()
    const replaced = new Set<ts.Node>()
    const handled = new Set<ts.Node>()
    const note = (node: ts.Node, kind: Finding["kind"], from: string, to?: string, why?: string) =>
      findings.push({
        file,
        ...position(sf, node.getStart(sf)),
        transform: "media-queries",
        kind,
        from,
        to,
        note: why,
      })

    walk(sf, (node) => {
      if (!ts.isCallExpression(node)) return
      const callee = node.expression
      const arg = node.arguments[0]
      const name = ts.isIdentifier(callee)
        ? callee.text
        : ts.isPropertyAccessExpression(callee)
          ? callee.name.text
          : ""
      if (name !== "useMediaQuery" && name !== "matchMedia") return
      if (!arg) return
      handled.add(arg)
      const from = node.getText(sf)
      if (!ts.isStringLiteralLike(arg)) {
        if (name === "useMediaQuery")
          note(
            node,
            "review",
            from,
            undefined,
            "a query in a constant or a template: a breakpoint by name, or a container query?",
          )
        return
      }
      const query = readQuery(arg.text)
      if (!query) return
      if ("off" in query) {
        note(
          node,
          "review",
          from,
          undefined,
          `${query.off}px is not a breakpoint: a container query?`,
        )
        return
      }
      if (name === "matchMedia") {
        note(
          node,
          "review",
          from,
          undefined,
          `breakpointQuery("${query.name}"${query.hook === "useBelow" ? ', "below"' : ""}) from ${MODULE}`,
        )
        return
      }
      const to = `${query.hook}("${query.name}")`
      note(node, "rewrite", from, to)
      edits.push({ start: node.getStart(sf), end: node.getEnd(), text: to })
      hooks.add(query.hook)
      replaced.add(callee)
    })

    // A width held in a string elsewhere (a constant passed in later) is worth a look too.
    walk(sf, (node) => {
      if (!ts.isStringLiteralLike(node) || handled.has(node) || !WIDTH_STRING.test(node.text))
        return
      if (ts.isImportDeclaration(node.parent)) return
      const query = readQuery(node.text)
      note(
        node,
        "review",
        node.getText(sf),
        undefined,
        query && "name" in query
          ? `a ${query.name} breakpoint in a string: useBreakpoint("${query.name}") or breakpointQuery`
          : "a viewport width in a string: a breakpoint by name, or a container query?",
      )
    })

    const variants = rewriteClasses(
      sf,
      file,
      "media-queries",
      variantRule(),
      options,
      classLiterals(sf),
    )
    edits.push(...variants.edits)
    findings.push(...variants.findings)

    if (hooks.size) {
      const add = addNamedImport(sf, MODULE, [...hooks].sort())
      if (add) edits.push(add)
      if (!referenced(sf, "useMediaQuery", replaced)) {
        const remove = removeNamedImport(sf, "useMediaQuery")
        if (remove) edits.push(remove)
      }
    }
    findings.sort((a, b) => a.line - b.line || a.column - b.column)
    return { text: applyEdits(text, edits), findings }
  },
}
