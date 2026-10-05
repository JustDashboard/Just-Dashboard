"use client"

import {
  languagePercent,
  languageProduct,
  languageSegments,
  type RepoLanguage,
} from "@/lib/git-languages"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SourceRepository } from "@/components/git/glyphs"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * A checkout drawn as what it is written in: the logo of its largest
 * language on the product tile, git's own mark for one the server could not
 * read (an empty repository, nothing but prose).
 *
 * Thirty cards that all opened on the same branch glyph were thirty of the
 * same thing; the language is what the reader already knows a repository
 * by, and a forge prints it on every listing for that reason (§14: a product
 * is drawn as itself). The owner's face stays on the shelf rule above the
 * cards, where it says whose they all are once.
 */
export function RepoMark({
  repo,
  size = "md",
  className,
}: {
  repo: Pick<GitRepo, "languages">
  size?: "sm" | "md"
  className?: string
}) {
  const top = repo.languages?.[0]
  const product = top ? languageProduct(top.name) : undefined
  return (
    <ProductLogo
      id={product ?? "git"}
      size={size}
      fallback={SourceRepository}
      className={className}
    />
  )
}

/**
 * The share of each language as one bar, the strip a forge draws under a
 * repository's name, in Linguist's hues.
 */
export function LanguageBar({
  languages,
  className,
}: {
  languages?: RepoLanguage[]
  className?: string
}) {
  const segments = languageSegments(languages)
  if (segments.length === 0) return null
  const sentence = segments.map((s) => `${s.name} ${languagePercent(s.share)}`).join(", ")
  return (
    <span
      role="img"
      aria-label={sentence}
      className={cn("flex h-1.5 min-w-0 gap-0.5 overflow-hidden rounded-full", className)}
    >
      {segments.map((s) => (
        <span
          key={s.name}
          title={`${s.name} ${languagePercent(s.share)}`}
          className="h-full min-w-0.5 first:rounded-l-full last:rounded-r-full"
          style={{ flexGrow: s.share, flexBasis: 0, background: s.colour }}
        />
      ))}
    </span>
  )
}

/**
 * The largest languages by name, each with its own logo and its share, under
 * the bar. Past `max` the rest are counted, so a card keeps to one line.
 */
export function LanguageList({
  languages,
  max = 3,
  className,
}: {
  languages?: RepoLanguage[]
  max?: number
  className?: string
}) {
  const named = (languages ?? []).filter((l) => l.share > 0)
  if (named.length === 0) return null
  const shown = named.slice(0, max)
  const rest = named.slice(shown.length)
  return (
    <span className={cn("flex min-w-0 items-center gap-x-3 overflow-hidden", className)}>
      {shown.map((l) => {
        const product = languageProduct(l.name)
        return (
          <span key={l.name} className="inline-flex min-w-0 shrink items-center gap-1 text-hint">
            {product ? (
              <ProductGlyph id={product} className="size-3" />
            ) : (
              <span aria-hidden className="size-2 shrink-0 rounded-[2px] bg-(--tag-slate)" />
            )}
            <span className="whitespace-nowrap text-foreground/85">{l.name}</span>
            <span className="numeric shrink-0 text-muted-foreground">
              {languagePercent(l.share)}
            </span>
          </span>
        )
      })}
      {rest.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="numeric shrink-0 text-hint text-muted-foreground">+{rest.length}</span>
          </TooltipTrigger>
          <TooltipContent>
            {rest.map((l) => `${l.name} ${languagePercent(l.share)}`).join(" · ")}
          </TooltipContent>
        </Tooltip>
      )}
    </span>
  )
}
