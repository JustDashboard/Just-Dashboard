"use client"

import { ArrowRight } from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * A grid of unequal cells, each one a way in. Magic UI's `bento-grid`, from
 * the shadcn registry, with its shadows, inset glow and neutral greys
 * replaced by the product's own separation — a border and a step of ground —
 * and the cell made a button, because the one place it is used offers ways to
 * start a project, and pressing one switches the page to it.
 *
 * The glyph is the cell's mark, large and faint behind the words, and grows
 * a step under the pointer; the arrow at the foot says the cell leads
 * somewhere. Nothing lifts.
 */
export function BentoGrid({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("grid w-full auto-rows-[7.5rem] gap-3", className)} {...props} />
}

export function BentoCard({
  name,
  description,
  icon: Icon,
  className,
  ...props
}: React.ComponentProps<"button"> & {
  name: string
  description: string
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <button
      type="button"
      className={cn(
        "group relative flex min-w-0 flex-col justify-end overflow-hidden rounded-xl border bg-card p-4 text-left focus-ring transition-colors hover:border-border-strong hover:bg-row-hover",
        className,
      )}
      {...props}
    >
      <Icon
        aria-hidden
        className="pointer-events-none absolute -top-3 -right-3 size-20 text-foreground/[0.06] transition-transform duration-300 ease-out group-hover:scale-110"
      />
      <span className="relative flex min-w-0 flex-col gap-0.5">
        <span className="text-body font-medium">{name}</span>
        <span className="text-hint leading-snug text-muted-foreground">{description}</span>
      </span>
      <ArrowRight
        aria-hidden
        className="absolute right-4 bottom-4 size-3.5 text-muted-foreground transition-transform duration-300 ease-out group-hover:translate-x-0.5"
      />
    </button>
  )
}
