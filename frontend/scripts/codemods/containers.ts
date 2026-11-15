/**
 * containers: a container query names its size (FND-15).
 *
 * `@min-[36rem]:` is `@xl:`, `@max-[38rem]:` is `@max-stack:` (the width under which a row's
 * trailing column drops beneath its name), and a named container keeps its name
 * (`@min-[36rem]/detail:` → `@xl/detail:`). A width that is exactly one of Tailwind's container
 * sizes is renamed; any other is reported. The plan folds `@max-[30rem]`, `[34rem]` and
 * `[36rem]` into the stack width, which changes where a row stacks, so those are suggestions.
 */
import { classTransform, type Rule, type Transform, renderToken } from "./core"

/** Tailwind's container scale, in rem. */
export const CONTAINER_SIZES: Record<number, string> = {
  16: "3xs",
  18: "2xs",
  20: "xs",
  24: "sm",
  28: "md",
  32: "lg",
  36: "xl",
  42: "2xl",
  48: "3xl",
  56: "4xl",
  64: "5xl",
  72: "6xl",
  80: "7xl",
}
const STACK = 38
const FOLD_INTO_STACK = new Set([30, 34, 36])

const VARIANT = /^@(min-|max-)?\[(\d+(?:\.\d+)?)(rem|px)\](\/[\w-]+)?$/

type Outcome = { variant: string; kind: "rewrite" | "review"; note?: string; suggest?: string }

function rename(variant: string): Outcome | undefined {
  const m = VARIANT.exec(variant)
  if (!m) return undefined
  const [, side = "min-", amount, unit, name = ""] = m
  const rem = Number(amount) / (unit === "px" ? 16 : 1)
  const max = side === "max-"
  const size = CONTAINER_SIZES[rem]
  if (max && rem === STACK) return { variant: `@max-stack${name}`, kind: "rewrite" }
  if (max && FOLD_INTO_STACK.has(rem) && name === "")
    return {
      variant,
      kind: "review",
      note: `the plan folds ${rem}rem into @max-stack (38rem), which moves where this stacks`,
      suggest: `@max-stack${name}`,
    }
  if (size) return { variant: `${max ? "@max-" : "@"}${size}${name}`, kind: "rewrite" }
  return { variant, kind: "review", note: `${rem}rem is not a container size` }
}

const rule = (): Rule => (token, raw) => {
  let changed = false
  const notes: string[] = []
  const variants = [...token.variants]
  const suggested = [...token.variants]
  let suggestion = false
  token.variants.forEach((v, i) => {
    const outcome = rename(v)
    if (!outcome) return
    if (outcome.kind === "rewrite" && outcome.variant !== v) {
      variants[i] = outcome.variant
      suggested[i] = outcome.variant
      changed = true
    }
    if (outcome.note) notes.push(outcome.note)
    if (outcome.suggest) {
      suggested[i] = outcome.suggest
      suggestion = true
    }
  })
  const to = renderToken({ ...token, variants })
  if (notes.length && !changed)
    return {
      review: notes.join("; "),
      suggest: suggestion ? renderToken({ ...token, variants: suggested }) : undefined,
    }
  if (changed) return { to, note: notes.join("; ") || undefined }
  return to === raw ? undefined : { to }
}

export const containers: Transform = {
  name: "containers",
  summary: "@min-[36rem] to @xl, @max-[38rem] to @max-stack, and the other container sizes",
  closes: ["FND-15"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--container-stack\s*:/,
      why: "@max-stack exists (WP-01)",
    },
  ],
  apply: (text, file, options) => classTransform("containers", rule)(text, file, options),
}
