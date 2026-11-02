"use client"

import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"
import { ProductGlyph } from "@/components/product-logo"

/**
 * AI Elements' `suggestion`, from its registry: a question already written, one
 * press from being asked. Drawn with the product or glyph of what it is about, in a
 * row that wraps rather than scrolling sideways, because the page has the room.
 */
export function Suggestion({
  label,
  question,
  product,
  icon: Icon,
  onAsk,
  className,
}: {
  label: string
  question: string
  product?: string
  icon?: Icon
  onAsk: (question: string) => void
  className?: string
}) {
  return (
    <button
      type="button"
      onClick={() => onAsk(question)}
      className={cn(
        "inline-flex min-h-9 max-w-full items-center gap-2 rounded-full border border-hairline bg-card px-3 py-1.5 text-left text-body focus-ring transition-colors hover:border-border-strong hover:bg-row-hover",
        className,
      )}
    >
      {product ? (
        <ProductGlyph id={product} className="size-4" />
      ) : (
        Icon && <Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
      )}
      <span className="min-w-0 truncate">{label}</span>
    </button>
  )
}
