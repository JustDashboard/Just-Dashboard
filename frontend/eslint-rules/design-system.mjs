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
 * `docs/internal/frontend/design-system.md`.
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
  return (
    tokens.every((t) => TOKEN.test(t)) && tokens.some((t) => /[-:[]/.test(t))
  )
}

/**
 * Each rule is a regex over the contents of a string literal, plus why.
 * `guard` narrows which literals the rule is allowed to look at.
 */
const BANNED = [
  {
    id: "type-scale",
    re: /\btext-\[[0-9.]+(px|rem)\]/,
    message:
      "Off the type scale. Use text-micro (10px), text-hint (11px), text-xs (12px), text-body (13px), text-sm (14px) or text-title (15px). Thirteen sizes is what typing them by hand produced.",
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
    re: /\brounded-(?:xs|2xl|3xl|4xl)\b/,
    message: "Off the radius ladder. Use rounded-sm, rounded-md, rounded-lg or rounded-xl.",
  },
  {
    id: "focus-ring",
    re: /focus-visible:(?:ring|outline|border-ring)/,
    message:
      "Use `focus-ring`, or `focus-ring-inset` where there is no room outside the control. Six spellings of one ring is six meanings.",
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
]

const rule = {
  meta: {
    type: "problem",
    docs: { description: "Keep class strings on the design system's tokens and primitives." },
    schema: [],
  },
  create(context) {
    const check = (node, text) => {
      if (typeof text !== "string" || text.length === 0) return
      for (const banned of BANNED) {
        if (banned.guard && !banned.guard(text)) continue
        if (banned.re.test(text)) {
          context.report({ node, message: `[${banned.id}] ${banned.message}` })
        }
      }
    }
    return {
      Literal(node) {
        check(node, node.value)
      },
      TemplateElement(node) {
        check(node, node.value?.cooked)
      },
    }
  },
}

const plugin = { rules: { tokens: rule } }

export default plugin
