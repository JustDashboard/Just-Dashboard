import type { Icon } from "@/components/icons"
import { LogoGlyph } from "@/components/logo"
import { ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"

/**
 * What a shared thing is drawn as: a product's own logo, this dashboard's J, or — for a kind no
 * product names — a glyph in the kind's lane hue, the way the Schedule band colours its kinds.
 * None of the hues is red or amber, so no kind reads as one that failed.
 */
export type Mark = { product?: string; glyph?: Icon; hue?: string; brand?: boolean }

const TILE =
  "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background"

export function MarkTile({ mark, className }: { mark: Mark; className?: string }) {
  if (mark.product) return <ProductLogo id={mark.product} size="sm" className={className} />
  const Glyph = mark.glyph
  return (
    <span aria-hidden className={cn(TILE, "size-8", className)}>
      {mark.brand || !Glyph ? (
        <LogoGlyph className="h-4 w-auto text-brand" />
      ) : (
        <Glyph className="size-4" style={{ color: mark.hue }} />
      )}
    </span>
  )
}

/** The scope cards' size of the same tile. */
export function LargeMarkTile({ mark }: { mark: Mark }) {
  if (mark.product) return <ProductLogo id={mark.product} />
  const Glyph = mark.glyph
  return (
    <span aria-hidden className={cn(TILE, "size-10")}>
      {mark.brand || !Glyph ? (
        <LogoGlyph className="h-5 w-auto text-brand" />
      ) : (
        <Glyph className="size-5" style={{ color: mark.hue }} />
      )}
    </span>
  )
}
