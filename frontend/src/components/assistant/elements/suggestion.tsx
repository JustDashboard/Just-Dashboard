"use client"

import { motion } from "motion/react"
import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"
import { ProductGlyph } from "@/components/product-logo"

/** How far apart suggestions arrive, and how many arrive one after another. */
const BEAT = 0.035
const STAGGERED = 8

/**
 * AI Elements' `suggestion`, from its registry: a question already written, one
 * press from being asked. Drawn with the product or glyph of what it is about, in a
 * row that wraps rather than scrolling sideways, because the page has the room.
 * Given its `index`, a row of them arrives one after another, a beat apart.
 */
export function Suggestion({
  label,
  question,
  product,
  icon: Icon,
  index = 0,
  onAsk,
  className,
}: {
  label: string
  question: string
  product?: string
  icon?: Icon
  index?: number
  onAsk: (question: string) => void
  className?: string
}) {
  return (
    <motion.button
      type="button"
      data-slot="suggestion"
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{
        duration: 0.2,
        ease: [0.16, 1, 0.3, 1],
        delay: 0.08 + Math.min(index, STAGGERED) * BEAT,
      }}
      onClick={() => onAsk(question)}
      className={cn(
        "inline-flex min-h-9 max-w-full items-center gap-2 rounded-md border border-hairline bg-card px-3 py-1.5 text-left text-body focus-ring transition-colors hover:border-border-strong hover:bg-row-hover",
        className,
      )}
    >
      {product ? (
        <ProductGlyph id={product} className="size-4" />
      ) : (
        Icon && <Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
      )}
      <span className="min-w-0 truncate">{label}</span>
    </motion.button>
  )
}
