"use client"

import Link from "next/link"
import { ArrowRight, Check } from "@/components/icons"
import { BlurFade } from "@/components/ui/blur-fade"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { cn } from "@/lib/utils"

/**
 * A block you pick one of: a deployment source, a repository, a container
 * template, a database engine, a credential kind.
 *
 * Six places had built this and none of them agreed. Three radii, two grounds,
 * three paddings, and only the deployment wizard set a minimum height — so the
 * same gesture looked like a card in Docker, a button in the wizard and a
 * swatch on the appearance page. It was unified once; then the flow register
 * shipped a *second* `ChoiceCard` in `components/flow.tsx` doing the same job
 * with a lit border — the drift this file exists to stop, arriving by the hand
 * of the person who wrote the rule. There is one again, and the lit border is
 * part of it, so every caller gets the edge that was only on the deploy pages.
 *
 * It is still not a `Panel`: a panel is a block of content that happens to be
 * framed, and this is a button that happens to be large.
 *
 * **Two shapes, one look.** A choice that is *a name* is the whole card — the
 * card is the button and its contents are its accessible name, which is right
 * when those contents are two short lines. A choice that carries *a sentence*
 * puts the control on its title and takes `verb`: an ARIA button names itself
 * from everything inside it, so a card holding a title, a paragraph and three
 * tags announces all of it as the name of one control (§12). The press on the
 * card around it is then a convenience for the pointer, exactly as a row does.
 *
 * Selection is `border-brand` over `bg-accent` — the same mark the terminal
 * rail puts on the active session and `ToggleGroup` on the pressed item (§3).
 * The lit edge is the *hover*: three states, three mechanisms (§6).
 */
export function ChoiceCard({
  selected,
  disabled,
  onClick,
  className,
  children,
  verb,
  title,
  description,
  trailing,
  mark,
  href,
  index = 0,
  ...props
}: Omit<React.ComponentProps<"button">, "title" | "onClick"> & {
  selected?: boolean
  onClick?: () => void
  /**
   * The accessible name of the control, for a card carrying more than a name.
   * Supplying it moves the control onto the title and draws the arrow.
   */
  verb?: string
  title?: React.ReactNode
  description?: React.ReactNode
  /** A reading about this option — what it costs, what it is built from. */
  trailing?: React.ReactNode
  /** Wayfinding: the glyph the reader is choosing a *kind* by (§14). */
  mark?: React.ComponentType<{ className?: string }>
  href?: string
  /** Position in a grid, for the arrival stagger. */
  index?: number
}) {
  const shape = cn(
    "group/choice flex min-h-20 min-w-0 flex-col items-start gap-1.5 rounded-xl p-3 text-left transition-colors",
    selected && "bg-accent",
    disabled && "opacity-50",
    className,
  )
  const edge = selected ? "brand" : "border"

  // The compact shape: the card is the control, and its contents are its name.
  if (!verb) {
    return (
      <SpotlightBorder resting={edge} className="h-full">
        <button
          type="button"
          data-slot="choice-card"
          aria-pressed={selected}
          data-selected={selected || undefined}
          disabled={disabled}
          onClick={onClick}
          className={cn(shape, "w-full focus-ring")}
          {...props}
        >
          {children}
        </button>
      </SpotlightBorder>
    )
  }

  const Mark = mark
  const choose = () => {
    if (!disabled) onClick?.()
  }
  return (
    <BlurFade delay={index * 0.03} className="h-full">
      <SpotlightBorder resting={edge} className="h-full">
        <div
          data-slot="choice-card"
          data-selected={selected || undefined}
          onClick={href ? undefined : choose}
          className={cn(shape, "h-full", !disabled && "cursor-pointer")}
        >
          <span className="flex w-full min-w-0 items-center gap-2">
            {Mark && (
              <Mark
                aria-hidden
                className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
              />
            )}
            {disabled ? (
              // An option that cannot be taken carries no control at all — not
              // a disabled one. A disabled button still answers `getByRole`,
              // still announces itself as the way to use the thing, and makes
              // the card say no twice. The card stays drawn and keeps its
              // sentence, because the reader needs to know it exists and why
              // they cannot have it.
              <span className="min-w-0 flex-1 truncate text-body font-medium">{title}</span>
            ) : href ? (
              <Link
                href={href}
                aria-label={verb}
                className="min-w-0 flex-1 truncate rounded-sm text-body font-medium focus-ring"
              >
                {title}
              </Link>
            ) : (
              <button
                type="button"
                aria-label={verb}
                aria-pressed={selected}
                // The card's own handler already fires on the pointer; this one
                // is for the keyboard and must not choose twice.
                onClick={(event) => {
                  event.stopPropagation()
                  choose()
                }}
                className="min-w-0 flex-1 truncate rounded-sm text-left text-body font-medium focus-ring"
              >
                {title}
              </button>
            )}
            {selected ? (
              <Check aria-hidden className="size-4 shrink-0 text-brand" />
            ) : (
              !disabled && (
                <ArrowRight
                  aria-hidden
                  className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
              )
            )}
          </span>
          {description && <ChoiceCardHint>{description}</ChoiceCardHint>}
          {trailing}
          {children}
        </div>
      </SpotlightBorder>
    </BlurFade>
  )
}

/** A run of choices. Layout lives beside the card it lays out. */
export function ChoiceGrid({
  className,
  columns = 3,
  ...props
}: React.ComponentProps<"div"> & { columns?: 2 | 3 }) {
  return (
    <div
      data-slot="choice-grid"
      className={cn(
        "grid min-w-0 gap-3",
        columns === 2 ? "sm:grid-cols-2" : "sm:grid-cols-2 lg:grid-cols-3",
        className,
      )}
      {...props}
    />
  )
}

/** The choice's name. Always the first line, so a row of them scans. */
export function ChoiceCardTitle({ className, ...props }: React.ComponentProps<"span">) {
  return <span className={cn("text-body font-medium", className)} {...props} />
}

/** One line on what picking it means. Two at most — this is not a panel body. */
export function ChoiceCardHint({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span className={cn("text-hint leading-relaxed text-muted-foreground", className)} {...props} />
  )
}
