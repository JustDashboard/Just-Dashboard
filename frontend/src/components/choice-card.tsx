"use client"

import Link from "next/link"
import { ArrowRight, Check, Database, type Icon } from "@/components/icons"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"
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
  logo,
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
  /**
   * The product's own logo — a `ProductLogo` — beside the whole card rather
   * than in front of its title. `mark` is a glyph for a kind of thing; this is
   * the thing itself, and a grid of sixty of them is scanned by it (§14).
   */
  logo?: React.ReactNode
  href?: string
  /** Position in a grid, for the arrival stagger. */
  index?: number
}) {
  const shape = cn(
    "group/choice flex min-h-20 min-w-0 flex-col items-start gap-1.5 rounded-xl p-3 text-left transition-colors",
    selected && "bg-accent",
    // The chosen card is never faded, disabled or not: to a reader who may
    // not change it, or on a choice that can no longer be taken again, it is
    // still the fact the grid is there to show.
    disabled && !selected && "opacity-50",
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
          className={cn(shape, "h-full", logo && "flex-row gap-3", !disabled && "cursor-pointer")}
        >
          {logo}
          {/* Beside a logo the words are one column, so every card's title
              starts on the same line whatever its logo's shape. */}
          <span className={cn("contents", logo && "flex min-w-0 flex-1 flex-col gap-1")}>
            <span className="flex w-full min-w-0 items-center gap-2">
              {Mark && (
                <Mark
                  aria-hidden
                  className={cn(
                    "size-4 shrink-0",
                    selected ? "text-brand" : "text-muted-foreground",
                  )}
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
            {description && (
              // Clamped beside a logo: a grid of these is compared card against
              // card, and one description running to five lines made its row
              // twice the height of every other.
              <ChoiceCardHint className={cn(logo && "line-clamp-2")}>{description}</ChoiceCardHint>
            )}
            {trailing}
            {children}
          </span>
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
}: React.ComponentProps<"div"> & {
  /**
   * `fill` fits as many columns as the grid's own width holds, for a grid
   * whose width depends on what is beside it rather than on the viewport — a
   * catalogue that narrows when a chosen template's settings open next to it.
   * Its rows are equal, so a card never outgrows its neighbours.
   *
   * `compact` is for a run of `ProductCard`s — ten builders, five package
   * managers — which are a logo and a word each: two to a row on a phone,
   * where one to a row made a picker of ten a screen and a half tall, and
   * five across at `xl`.
   */
  columns?: 2 | 3 | "fill" | "compact"
}) {
  return (
    <div
      data-slot="choice-grid"
      className={cn(
        "grid min-w-0 gap-3",
        columns === "fill"
          ? "auto-rows-fr grid-cols-[repeat(auto-fill,minmax(16rem,1fr))]"
          : columns === "compact"
            ? "grid-cols-2 sm:grid-cols-3 xl:grid-cols-5"
            : columns === 2
              ? "sm:grid-cols-2"
              : "sm:grid-cols-2 lg:grid-cols-3",
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

/** The three kinds `/databases/drivers` sorts every engine into. */
type EngineKind = "sql" | "document" | "keyvalue"

const KIND_WORD: Record<EngineKind, string> = {
  sql: "SQL",
  document: "documents",
  keyvalue: "key–value",
}

/**
 * The kind of a driver, for the pickers that list engines to start rather than
 * drivers to connect with — the provisioning options name a driver and nothing
 * more, and the connection dialog's `DbDriverInfo` already carries the kind.
 */
export function driverKind(driver: string): EngineKind {
  if (driver === "redis") return "keyvalue"
  if (driver === "mongodb" || driver === "mongo") return "document"
  return "sql"
}

/**
 * One product, as a picker of products draws it: its own logo, its name, and
 * one line of what exactly would be used — the image an engine would run, the
 * versions a builder covers, the lockfile a package manager reads.
 *
 * This is the compact shape of `ChoiceCard` — the card is the button and its
 * two short lines are its name — with the logo in front, because a run of
 * builders or engines is scanned by their marks before their words (§14). It
 * began as the database engine picker's card and the build settings needed
 * the same card for ten builders and five package managers; §4's rule about a
 * name that already exists applies to a shape too.
 *
 * `working` runs a light around the card while the thing picked is being
 * made — a database being provisioned (§11 *live*). The selection stays drawn
 * under it: the beam says it is happening, the edge still says which one.
 */
export function ProductCard({
  product,
  label,
  detail,
  mono,
  fallback,
  selected,
  disabled,
  working,
  onClick,
  children,
}: {
  /** The key `ProductLogo` looks the logo up by. */
  product?: string
  label: string
  /** One line under the name. */
  detail?: React.ReactNode
  /** The detail is a literal from the host — an image reference, a path. */
  mono?: boolean
  /** The glyph on the tile when the product has no logo of its own. */
  fallback?: Icon
  selected?: boolean
  disabled?: boolean
  working?: boolean
  onClick?: () => void
  /** A reading between the name and the detail: the kind of store it is. */
  children?: React.ReactNode
}) {
  return (
    <ChoiceCard
      selected={selected}
      disabled={disabled}
      onClick={onClick}
      className="min-h-0 flex-row items-center gap-3 px-2.5 py-2"
    >
      <ProductLogo id={product} size="sm" fallback={fallback} />
      {/* The rest of the card's width, not its title's: sized to its own
          content, a detail wider than the name ("1.25 · 1.26" under "Go")
          was clipped mid-glyph rather than given the room beside it. */}
      <span className="flex min-w-0 flex-1 flex-col">
        <ChoiceCardTitle className="truncate">{label}</ChoiceCardTitle>
        {children}
        {detail && (
          <span className={cn("truncate text-micro text-muted-foreground", mono && "font-mono")}>
            {detail}
          </span>
        )}
      </span>
      {working && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={60} duration={4} />
        </span>
      )}
    </ChoiceCard>
  )
}

/**
 * One database engine, as every picker of one draws it: the engine's own logo,
 * its name, the kind of store it is, and what exactly would run.
 *
 * Three pickers drew this — the deployment's quick setup, New database and the
 * connection dialog — and they had already come apart: one with a generic
 * database glyph, two with none, three different second lines. §4's rule about
 * a component whose name already exists applies to a shape too, which is why
 * it is now a `ProductCard` with the kind between the name and the image.
 */
export function EngineCard({
  engine,
  label,
  kind,
  detail,
  selected,
  disabled,
  working,
  onClick,
}: {
  /** The engine or driver key the logo is looked up by. */
  engine: string
  label: string
  /** What kind of store it is, in the drivers' own vocabulary. */
  kind?: EngineKind
  /** A literal under the name: the image that would run. */
  detail?: string
  selected?: boolean
  disabled?: boolean
  /** The engine is being started — the quick setup's provisioning. */
  working?: boolean
  onClick?: () => void
}) {
  return (
    <ProductCard
      product={engine}
      label={label}
      detail={detail}
      mono
      fallback={Database}
      selected={selected}
      disabled={disabled}
      working={working}
      onClick={onClick}
    >
      {kind && <Tag>{KIND_WORD[kind]}</Tag>}
    </ProductCard>
  )
}
