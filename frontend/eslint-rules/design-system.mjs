import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"

/**
 * The design system's mechanical rules, as an ESLint plugin.
 *
 * The September unification pass single-sourced the primitives and the tokens,
 * and the tree drifted again in the very next feature written — because every
 * rule was enforced by one person reading it. What a linter can actually catch
 * is the two failures that are mechanical: typing a value by hand where a token
 * exists, and re-deriving a pattern a primitive already owns. Both are below.
 *
 * The rest — whether a surface is a Panel or a Pane, whether a fact is a Tag or
 * a Status — stays a judgement, and stays in
 * `docs/internal/frontend/standard.md` and the pages `design-system.md` indexes.
 *
 * A local plugin rather than `no-restricted-syntax` selectors: esquery's
 * attribute-regex grammar has no way to escape a `/`, which every one of these
 * patterns needs.
 */

/**
 * Whether a string plausibly *is* a class list.
 *
 * `radius-bare` matches a bare `rounded` surrounded by whitespace, which is
 * also how the word appears in an English sentence — and this plugin tests
 * every string literal in a file, not only the ones reaching `className`. It
 * fired on the design system's own Playwright assertion message ("fully
 * rounded filled chips") and failed `bun run lint` on an otherwise clean tree,
 * which is the worst way for a style rule to behave.
 *
 * The discriminator is that a class list contains at least one token shaped
 * like a Tailwind utility — a hyphen, a variant colon, or an arbitrary-value
 * bracket — and no token that could not be a class at all. `rounded` never
 * appears alone in practice: it arrives with a `bg-*`, a `border-*` or a `p-*`,
 * so requiring one of those costs a theoretical false negative
 * (`"flex rounded border"`) and buys back every English sentence in the tree.
 *
 * Checked here rather than by looking at the argument position, because
 * `cn(...)`, `cva(...)` and template interpolation would each need their own
 * AST shape and a new one would be missed the first time it was used.
 */
const TOKEN = /^[a-z0-9][\w:./[\]#%(),!*&>~+'"=-]*$/
const classLike = (text) => {
  const tokens = text.trim().split(/\s+/)
  return tokens.every((t) => TOKEN.test(t)) && tokens.some((t) => /[-:[]/.test(t))
}

/**
 * A class list as its classes, each split into its variants and its utility:
 * `hover:[&_svg]:text-faint/50` is the utility `text-faint/50` behind two
 * variants. A colon inside brackets belongs to the value, not to a variant.
 */
export function classes(text) {
  return text
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map((token) => {
      const variants = []
      let depth = 0
      let from = 0
      for (let at = 0; at < token.length; at++) {
        const c = token[at]
        if (c === "[" || c === "(") depth++
        else if (c === "]" || c === ")") depth--
        else if (c === ":" && depth === 0) {
          variants.push(token.slice(from, at))
          from = at + 1
        }
      }
      return { variants, utility: token.slice(from).replace(/^!|!$/g, "") }
    })
}

/**
 * The JSX element a string is a prop of, when it is one: through `cn(...)`, a
 * condition, an array or a template, and no further than the expression it is
 * written in. A class list held in a constant has no element to ask.
 */
const THROUGH = new Set([
  "JSXExpressionContainer",
  "CallExpression",
  "ConditionalExpression",
  "LogicalExpression",
  "ArrayExpression",
  "TemplateLiteral",
  "ObjectExpression",
  "Property",
  "BinaryExpression",
  "TSAsExpression",
])
export function elementOf(node) {
  let at = node.parent
  while (at) {
    if (at.type === "JSXAttribute") return at.parent
    if (!THROUGH.has(at.type)) return undefined
    at = at.parent
  }
  return undefined
}
export function elementName(element) {
  const name = element?.name
  if (name?.type === "JSXIdentifier") return name.name
  if (name?.type === "JSXMemberExpression") return name.property.name
  return undefined
}
/** A prop of an element when it is written as a literal: `tabIndex={-1}` is -1, `type="single"` is "single". */
export function literalProp(element, prop) {
  const attribute = element?.attributes?.find(
    (a) => a.type === "JSXAttribute" && a.name?.name === prop,
  )
  if (!attribute) return undefined
  if (attribute.value === null) return true
  let value = attribute.value
  if (value.type === "JSXExpressionContainer") value = value.expression
  if (value.type === "Literal") return value.value
  if (
    value.type === "UnaryExpression" &&
    value.operator === "-" &&
    value.argument.type === "Literal"
  )
    return -value.argument.value
  return undefined
}

/**
 * The colours `globals.css` registers as utilities (`--color-tag-pink` is
 * `text-tag-pink`), read from the stylesheet so the rule and the theme cannot
 * disagree. A colour that is not registered (a row's own `--git-colour`, set
 * in a style) has no utility to be spelled as, and is left alone.
 */
const STYLESHEET = fileURLToPath(new URL("../src/app/globals.css", import.meta.url))
let registered
export function registeredColours() {
  if (registered) return registered
  registered = new Set()
  try {
    for (const [, name] of readFileSync(STYLESHEET, "utf8").matchAll(/^\s*--color-([\w-]+)\s*:/gm))
      registered.add(name)
  } catch {
    // No stylesheet beside the rules: nothing is registered, and nothing is refused.
  }
  return registered
}

const COLOUR_VAR =
  /^(?:bg|text|border(?:-[trblxyse])?|ring|fill|stroke|from|to|via|divide|outline|decoration|accent|caret|shadow)-(?:\[(?:color:)?var\(--([\w-]+)\)\]|\(--([\w-]+)\))(?:\/[\w.[\]%]+)?$/
const INK_FADE = /^text-(?:foreground|muted-foreground|primary|faint)\/(?:\[[^\]]+\]|\d+)$/
// Where the glyphs come from: a fade on one of these is a glyph's, not a word's.
const ICON_MODULE = /(?:^|\/)icons$|lucide|heroicons|@mdi\//
const Z_RAW = /^-?z-(?:\d+|\[[^\]]+\])$/
const RADIUS_ARBITRARY =
  /^rounded(?:-(?:[trblse]|tl|tr|bl|br|ss|se|es|ee))?-\[[\d.]+(?:px|rem|em)?\]$/
const VIEWPORT_VARIANT = /^(?:min|max)-\[[^\]]+\]$/
const CONTAINER_VARIANT = /^@(?:(?:min|max)-)?\[[^\]]+\](?:\/[\w-]+)?$/
// A width written into a media query in script: `(min-width: 1024px)`, `(width < 40rem)`.
const MEDIA_WIDTH =
  /\(\s*(?:min-|max-)?(?:device-)?width\s*(?::|[<>]=?)\s*[\d.]+(?:px|rem|em)\s*\)|\(\s*[\d.]+(?:px|rem|em)\s*[<>]=?\s*width\b/
const VH = /(?<![a-z\d])\d*\.?\d+vh\b/
const SCREEN_HEIGHT = /^(?:min-|max-)?h-screen$/
const OWN_RING = /^(?:ring(?:-|$)|outline(?:-|$)|border-ring$)/
const FOCUS_VARIANT = /^(?:focus|focus-visible|focus-within)$/
const NO_OUTLINE = /^outline-(?:none|hidden)$/

/**
 * Each rule reads the contents of a string literal: `re` is a regex over the
 * whole string, `find` a function of the string and where it stands, for a
 * rule that has to know which class it is looking at or which element the
 * list is on. `guard` narrows which literals the rule is allowed to look at,
 * and `exempt` is the files that define what the rule refuses elsewhere.
 *
 * A message names the replacement and the page of
 * `docs/internal/frontend/` that says why.
 */
const BANNED = [
  {
    id: "type-scale",
    re: /\btext-\[[0-9.]+(px|rem)\]/,
    message:
      "Off the type scale. Use text-micro (10px), text-hint (11px), text-xs (12px), text-body (13px), text-sm (14px) or text-title (15px), each with its own line height. Thirteen sizes is what typing them by hand produced.",
  },
  {
    id: "tone-tint",
    re: /\b(?:bg|border)-(?:primary|destructive|warning|success)\/(?:\[?0?\.0?[0-9]+\]?|[0-9]|[12][0-9]|3[0-5])\b/,
    message:
      "Hand-mixed tone tint. Use bg-plot-* for an icon plot, bg-wash-* for a tinted banner, border-rule-* for a tinted rule. Three roles had 52 alphas between them.",
  },
  {
    id: "radius-bare",
    re: /(?:^|\s)rounded(?:\s|$)/,
    guard: classLike,
    message: "Bare `rounded` is `rounded-sm` spelled a second way. Use the named step.",
  },
  {
    id: "radius-scale",
    re: /\brounded-(?:2xl|3xl|4xl)\b/,
    message:
      "Off the radius ladder. Use rounded-xs (swatch), rounded-sm (mark), rounded-md (control), rounded-lg (well, floating surface), rounded-xl (block) or rounded-tile-md/lg (logo tile).",
  },
  {
    // A typed radius is how 53 of them filled the gaps the ladder had. The
    // ladder has those steps now; a shape that is not a corner (`50%`, a
    // variable, `inherit`) is not a step and is left alone.
    id: "radius-arbitrary",
    find: (text) => classes(text).some((c) => RADIUS_ARBITRARY.test(c.utility)),
    message:
      "A radius typed by hand. Use the ladder: rounded-xs (2px, swatch), rounded-sm (4px, mark), rounded-md (6px, control), rounded-lg (8px, well, floating surface), rounded-xl (12px, block), rounded-tile-md (10px) or rounded-tile-lg (14px) for a logo tile (foundations/surfaces.md).",
  },
  {
    // The ring was opt-in and typed three ways. It is the base layer's now
    // (every link, button and tab stop has it), so a class list only places
    // it, and nothing may take it away: `outline-none` on an element the
    // keyboard can land on shows a keyboard nothing. Two places keep it: a
    // control inside a group that draws the ring for it (`focus-ring-within`
    // is then in the same file), and an element focus is only ever sent to
    // (`tabIndex={-1}`), which a ring would box as a whole.
    id: "focus-ring",
    find(text, { node, groupRing }) {
      const list = classes(text)
      if (
        list.some((c) => c.variants.some((v) => FOCUS_VARIANT.test(v)) && OWN_RING.test(c.utility))
      )
        return true
      if (!list.some((c) => c.variants.length === 0 && NO_OUTLINE.test(c.utility))) return false
      if (list.some((c) => c.utility === "focus-ring-within")) return false
      if (literalProp(elementOf(node), "tabIndex") === -1) return false
      return !groupRing
    },
    message:
      "Use `focus-ring`, `focus-ring-inset` where there is no room outside the control, or `focus-ring-within` on a group that draws it for the control inside. No `focus:`, `focus-visible:` or `focus-within:` ring, outline or border of your own, and no bare `outline-none`, which takes the ring off for the keyboard (foundations/states.md).",
  },
  {
    id: "row-reveal",
    re: /group-hover(?:\/[a-z-]+)?:opacity-100/,
    message:
      "Use RowActions, `IconAction reveal`, or rowReveal() from components/icon-action. A hand-written reveal is how five action clusters ended up unreachable on touch.",
  },
  {
    id: "hardcoded-palette",
    re: /\b(?:bg|text|border|ring|fill|stroke|from|to|via|divide|outline|shadow|decoration|accent|caret)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-(?:50|[1-9]00|950)\b/,
    message:
      "Tailwind's own palette is not this product's. Every colour comes from a token in globals.css.",
  },
  {
    // Three inks, and a word is never faded with opacity: 183 such fades were
    // being redrawn as `--faint` by an override, and 74 more by nothing. A
    // glyph may fade: an `svg`, an icon, or what an `[&_svg]:` variant reaches.
    // A class list held in a constant has no element to ask, so one that
    // sizes something (`size-3`) is taken to be a glyph's.
    id: "ink-opacity",
    find(text, { node, icons }) {
      const list = classes(text)
      const faded = list.filter((c) => INK_FADE.test(c.utility))
      if (faded.every((c) => c.variants.some((v) => v.includes("svg")))) return false
      const element = elementOf(node)
      if (!element) return !list.some((c) => /^size-/.test(c.utility))
      const name = elementName(element)
      return !(name === "svg" || /Icon$/.test(name ?? "") || icons.has(name))
    },
    message:
      "A text ink faded with opacity. The inks are text-foreground, text-muted-foreground and text-faint, which keeps 4.5:1; a fainter word is text-faint, never a `/NN` on an ink (foundations/colour.md).",
  },
  {
    id: "colour-var",
    find: (text) =>
      classes(text).some((c) => {
        const found = COLOUR_VAR.exec(c.utility)
        const name = found?.[1] ?? found?.[2]
        return name !== undefined && registeredColours().has(name.replace(/^color-/, ""))
      }),
    message:
      "A registered colour reached through `var()`. Every colour in globals.css's @theme has a utility: text-tag-pink, bg-git-modified, border-chart-2, not text-[var(--tag-pink)] or text-(--tag-pink) (foundations/colour.md).",
  },
  {
    id: "z-layer",
    find: (text) => classes(text).some((c) => Z_RAW.test(c.utility)),
    message:
      "A z-index typed as a number. Use the named layer: z-raised (10), z-sticky (20), z-pane (30), z-float (40), z-immersive (45), z-overlay (50) or z-tooltip (55); `isolate` starts a stacking context without one (foundations/surfaces.md).",
  },
  {
    // A width typed into a viewport variant or a media query is a sixth
    // breakpoint nobody else agrees with (1100, 1180, 1400, 1600, 1856 …), and
    // most of them were a block asking how wide the window is when it meant
    // how wide its own column is.
    id: "media-literal",
    exempt: /src[\\/]lib[\\/]breakpoints\.ts$/,
    find: (text) =>
      MEDIA_WIDTH.test(text) ||
      classes(text).some((c) => c.variants.some((v) => VIEWPORT_VARIANT.test(v))),
    message:
      "A viewport width typed by hand. In a class use sm:, md:, lg:, xl: or 2xl: (max-lg: and the rest for below); in script use useBreakpoint(name), useBelow(name) or breakpointQuery(name) from lib/breakpoints. A block that sits beside a rail on one page and full-width on another asks its container (@xl:, @4xl/detail:) instead (foundations/layout-phone.md).",
  },
  {
    id: "container-arbitrary",
    find: (text) => classes(text).some((c) => c.variants.some((v) => CONTAINER_VARIANT.test(v))),
    message:
      "A container width typed by hand. Use a named size (@md: 28rem, @lg: 32rem, @xl: 36rem, @2xl: 42rem, @3xl: 48rem, @4xl: 56rem, @5xl: 64rem) or @max-stack: (38rem), where a row stacks (foundations/layout-phone.md).",
  },
  {
    // The five animations of §11 do not include it. It dims the thing itself,
    // so a status dot or a pending glyph that pulses is half gone half the
    // time; what waits is a Skeleton or a Bone, and what is live breathes.
    id: "animate-pulse-status",
    exempt: /src[\\/]components[\\/]bone\.tsx$/,
    find: (text) => classes(text).some((c) => c.utility === "animate-pulse"),
    message:
      "animate-pulse is Skeleton's and Bone's. A placeholder is a Skeleton (components/ui/skeleton) or a Bone (components/bone.tsx) in a line of text; a live or pending mark uses animate-breathe, or StatusDot with `live` (foundations/motion.md).",
  },
  {
    // `vh` is the window with the phone's toolbars hidden, so a well sized
    // with it runs under them. `svh` is the small viewport, `dvh` follows the
    // toolbars for a pane that fills the screen.
    id: "vh-unit",
    find: (text) =>
      classes(text).some(
        (c) => SCREEN_HEIGHT.test(c.utility) || (c.utility.includes("[") && VH.test(c.utility)),
      ),
    message:
      "A height in vh, which changes with a phone's toolbars. Use svh (h-svh, max-h-[70svh]), dvh for a pane that fills the screen, or h-workbench for a workbench well (foundations/layout-phone.md).",
  },
]

const rule = {
  meta: {
    type: "problem",
    docs: { description: "Keep class strings on the design system's tokens and primitives." },
    // `off` names the ids a file is not held to yet. eslint.config.mjs lists
    // each such file with its reason, and the list only shrinks.
    schema: [
      {
        type: "object",
        properties: { off: { type: "array", items: { type: "string" } } },
        additionalProperties: false,
      },
    ],
  },
  create(context) {
    const filename = context.filename ?? ""
    const off = new Set(context.options[0]?.off ?? [])
    const active = BANNED.filter((banned) => !off.has(banned.id) && !banned.exempt?.test(filename))
    // Whether this file draws a group's ring, for the control inside one.
    const groupRing = /\bfocus-ring-within\b/.test(context.sourceCode?.text ?? "")
    // The names this file imports from an icon module.
    const icons = new Set()
    const check = (node, text) => {
      if (typeof text !== "string" || text.length === 0) return
      for (const banned of active) {
        if (banned.guard && !banned.guard(text)) continue
        const found = banned.re
          ? banned.re.test(text)
          : banned.find(text, { node, icons, groupRing })
        if (found) context.report({ node, message: `[${banned.id}] ${banned.message}` })
      }
    }
    return {
      ImportDeclaration(node) {
        if (typeof node.source.value !== "string" || !ICON_MODULE.test(node.source.value)) return
        for (const specifier of node.specifiers) icons.add(specifier.local.name)
      },
      Literal(node) {
        check(node, node.value)
      },
      TemplateElement(node) {
        check(node, node.value?.cooked)
      },
    }
  },
}

/**
 * How long things took is said in words ("19 in 20 within"), not as
 * `p50 / p95 / p99`: the operator found the notation unclear, and the same
 * figure had two names depending on the page. `src/lib/latency-words.ts` holds
 * the words; this keeps the notation from being typed into a label again.
 *
 * It reads what a person would see: JSX text, a string or template with other
 * words around the term ("p95 above", `p95 ${figure}`), and a bare term given
 * as a `label`, `title`, `name` or `hint`. A bare `"p95"` anywhere else is a
 * key — a series, a sort, a field of the server's answer — and is left alone.
 *
 * One file is outside it: the words' own module, which names the term for
 * tooltips. The interface charts were too, because their percentiles are of
 * throughput rather than of how long anything took; they say their shares in
 * the same words since 0.8.7.
 */
const NOTATION = /(?:^|[^\w.$])p(?:50|75|90|95|99)(?![\w(])/
const NOTATION_BARE = /^\s*p(?:50|75|90|95|99)\s*$/
const NOTATION_EXEMPT =
  /(?:\.test\.[jt]sx?$|[\\/]tests[\\/]|[\\/]eslint-rules[\\/]|src[\\/]lib[\\/]latency-words\.ts$)/
const WORDED_PROPS = new Set(["label", "title", "name", "hint", "info", "aria-label"])

const latencyWords = {
  meta: {
    type: "problem",
    docs: { description: "Say how long things took in words, not as p50 / p95 / p99." },
    schema: [],
  },
  create(context) {
    if (NOTATION_EXEMPT.test(context.filename ?? "")) return {}
    const message =
      'Percentile notation in rendered text. Say it in words from lib/latency-words.ts (latencyHead, latencyWords, LATENCY_SHARE): "19 in 20 within", not "p95".'
    const worded = (node) => {
      const parent = node.parent
      if (parent?.type === "JSXAttribute") return WORDED_PROPS.has(parent.name?.name)
      if (parent?.type === "Property" && parent.value === node)
        return WORDED_PROPS.has(parent.key?.name ?? parent.key?.value)
      return false
    }
    const check = (node, text) => {
      if (typeof text !== "string" || !NOTATION.test(text)) return
      if (NOTATION_BARE.test(text) && !worded(node)) return
      context.report({ node, message })
    }
    return {
      JSXText(node) {
        if (NOTATION.test(node.value)) context.report({ node, message })
      },
      Literal(node) {
        // A property's own key, an import's source and a type's literal are
        // names, not words anyone reads.
        const parent = node.parent
        if (parent?.type === "Property" && parent.key === node) return
        if (parent?.type === "TSLiteralType" || parent?.type === "ImportDeclaration") return
        check(node, node.value)
      },
      TemplateElement(node) {
        if (node.parent?.parent?.type === "TSLiteralType") return
        check(node, node.value?.cooked)
      },
    }
  },
}

/**
 * Primitives a page may not reach past or bring back.
 *
 * `BarList` draws a ranked list as bars of different lengths, which the
 * operator asked to stop: figures sit in aligned columns (`RankList`) with one
 * fixed strip for a whole (`SplitBar`). Insights left it first, the Databases
 * pages next, and the deploy, proxy, security, network and Files lists last.
 * It stays for the one place where the bars are the picture the reader asked
 * for, the Query page's Chart tab, and it lives beside that tab
 * (`components/database/query/bar-list.tsx`) since the 0.9.0 file moves. (The
 * small size bars inside table cells, the other shape the operator kept, are
 * their own few lines and never were a `BarList`.) A new ranked list is a
 * `RankList`.
 *
 * Any import that ends in `bar-list` is the one, whichever folder it is
 * written from, so a relative `./bar-list` is refused as an alias is.
 */
const BAR_LIST_HOMES = /src[\\/]components[\\/]database[\\/]query[\\/](?:bar-list|chart-view)\.tsx$/
const BAR_LIST_IMPORT = /(?:^|\/)bar-list(?:\.tsx?)?$/

/**
 * The 0.6.1 design-system audit asked for one checkbox and one spinner, and
 * each came back in a newer file: a native `<input type="checkbox">` beside
 * the Radix one in the same workspace, and `LoaderCircle` spun by hand where
 * `Spinner` (components/loading.tsx) is the spinner. Both are refused outside
 * the primitives that draw them. The spinner's home is `components/loading.tsx`,
 * which the 0.9.0 file moves split out of `state.tsx`; both stay homes, since
 * `state.tsx` still draws the states a spinner sits in.
 */
const PRIMITIVE_HOMES = /src[\\/]components[\\/](?:ui[\\/]|state\.tsx$|loading\.tsx$|icons\.tsx$)/

/**
 * The controls that take a `Field`'s id when they set none of their own
 * (`useFieldControl`). A `Switch` that names itself does not.
 */
const FIELD_CONTROLS = new Set([
  "Input",
  "InputGroupInput",
  "Textarea",
  "SelectTrigger",
  "Switch",
  "SearchInput",
  "SecretField",
  "OneTimeCodeInput",
  "ListField",
])

function takesFieldId(opening) {
  const name = elementName(opening)
  if (!FIELD_CONTROLS.has(name)) return false
  const has = (prop) =>
    opening.attributes.some((a) => a.type === "JSXAttribute" && a.name?.name === prop)
  if (has("id")) return false
  return name !== "Switch" || !(has("aria-label") || has("aria-labelledby"))
}

/**
 * How many controls under a node can take a field's id at once. Two branches
 * of a choice are one at a time; a control drawn by a callback (a `map`) is
 * drawn as many times as there are items, which is more than one.
 */
function fieldIdTakers(node) {
  if (!node) return 0
  const sum = (nodes) => nodes.reduce((total, one) => total + fieldIdTakers(one), 0)
  switch (node.type) {
    case "JSXElement":
      // A field inside is its own.
      if (elementName(node.openingElement) === "Field") return 0
      return (takesFieldId(node.openingElement) ? 1 : 0) + sum(node.children)
    case "JSXFragment":
      return sum(node.children)
    case "JSXExpressionContainer":
    case "TSAsExpression":
    case "TSNonNullExpression":
      return fieldIdTakers(node.expression)
    case "ConditionalExpression":
      return Math.max(fieldIdTakers(node.consequent), fieldIdTakers(node.alternate))
    case "LogicalExpression":
      return fieldIdTakers(node.left) + fieldIdTakers(node.right)
    case "ArrayExpression":
      return sum(node.elements)
    case "CallExpression":
      return node.arguments.reduce((total, argument) => {
        const looped =
          argument.type === "ArrowFunctionExpression" || argument.type === "FunctionExpression"
        return total + (looped ? 2 * fieldIdTakers(argument.body) : fieldIdTakers(argument))
      }, 0)
    case "BlockStatement":
      return sum(node.body)
    case "ReturnStatement":
      return fieldIdTakers(node.argument)
    case "IfStatement":
      return Math.max(fieldIdTakers(node.consequent), fieldIdTakers(node.alternate))
    default:
      return 0
  }
}

const primitives = {
  meta: {
    type: "problem",
    docs: { description: "Compose the design system's primitives rather than their predecessors." },
    schema: [],
  },
  create(context) {
    const filename = context.filename ?? ""
    const home = PRIMITIVE_HOMES.test(filename)
    return {
      // A field of several controls is a group: given to each, the field's
      // id was one id on many elements and its label named the first
      // (0.9.1 review of #508). `htmlFor` names the one the label is for.
      JSXElement(node) {
        const opening = node.openingElement
        if (elementName(opening) !== "Field") return
        const props = new Set(
          opening.attributes.filter((a) => a.type === "JSXAttribute").map((a) => a.name?.name),
        )
        if (props.has("htmlFor") || props.has("group")) return
        if (node.children.reduce((total, child) => total + fieldIdTakers(child), 0) < 2) return
        context.report({
          node: opening,
          message:
            "[field-group] This Field holds several controls, and each would take its id. Give it `group`, so it is their named group and each control names itself, or `htmlFor` the one its label is for.",
        })
      },
      JSXOpeningElement(node) {
        if (home || node.name?.type !== "JSXIdentifier") return
        if (node.name.name === "LoaderCircle") {
          context.report({
            node,
            message:
              "[spinner] Use Spinner from components/loading.tsx. A hand-spun LoaderCircle is the spinner drawn a second way.",
          })
          return
        }
        if (node.name.name !== "input") return
        const type = node.attributes.find(
          (attribute) => attribute.type === "JSXAttribute" && attribute.name?.name === "type",
        )
        if (type?.value?.type === "Literal" && type.value.value === "checkbox") {
          context.report({
            node,
            message:
              "[checkbox] Use Checkbox from components/ui/checkbox. The browser's own control beside the product's is two checkboxes on one page.",
          })
        }
      },
      ImportDeclaration(node) {
        if (typeof node.source.value !== "string" || !BAR_LIST_IMPORT.test(node.source.value))
          return
        if (BAR_LIST_HOMES.test(filename)) return
        context.report({
          node,
          message:
            "[bar-list] A ranked list is a RankList (components/rank-list.tsx): name, figure and share in columns, with SplitBar for one strip of a whole. BarList stays only on the Query page's Chart tab.",
        })
      },
    }
  },
}

const plugin = { rules: { tokens: rule, "latency-words": latencyWords, primitives } }

export default plugin
