"use client"

import Link from "next/link"
import { ArrowRight, Copy, Slash } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  missingFromError,
  type Missing,
  type MissingStep,
  type SystemdReader,
} from "@/components/missing-thing-model"

type Glyph = React.ComponentType<{ className?: string }>

/** What the page draws once the thing is there, said before it is. */
export type MissingGain = { icon: Glyph; title: string; text: string }

/**
 * The screen a page shows where the thing it reads is not there: Docker that
 * is not installed or does not answer, a server with no systemd, a module
 * switched off in Configuration.
 *
 * It is the proxy's `EngineSetup` made general: the thing drawn as itself, a
 * headline that says what is missing, one or two sentences on why and where
 * it is fixed, and the next step as a button. The proxy had that screen and
 * every other section said the same fact as a grey sentence, a calm box
 * reading "Not available on this host", or the daemon's own error.
 *
 * - `size="page"` (the default) is the whole page under its title, at the
 *   reading size, with an optional rail of what the page becomes.
 * - `size="block"` is the same words inside a section of a page that has
 *   other things on it (Timers on Scheduled): the mark beside the words, the
 *   steps under them, no frame of its own.
 *
 * An empty list is still `EmptyState`, a failed read still `ErrorState`, and
 * one line inside a rail or an inspector is `MissingLine`. `MissingOrError`
 * picks between this and `ErrorState` for a read that failed.
 *
 * The daemon's words go in `raw` and are drawn folded, never as the title.
 */
export function MissingThing({
  thing,
  icon: Icon = Slash,
  title,
  children,
  step,
  more,
  raw,
  gains,
  gainsLabel,
  after,
  size = "page",
  reason,
  className,
}: {
  /** The thing as itself: a `ProductLogo` id. Without one, `icon` on the same tile. */
  thing?: string
  icon?: Glyph
  /** What is missing, in plain words: "Docker is stopped". */
  title: string
  /** Why, and where it is fixed. */
  children: React.ReactNode
  /** The next step: one brand-faced `Button` (or nothing for an account that may not take it). */
  step?: React.ReactNode
  /** Further steps and a line to copy, after the first. */
  more?: React.ReactNode
  /** The subsystem's own words, behind "What the server said". */
  raw?: string
  gains?: MissingGain[]
  /** The rail's accessible name: "What the page shows once Docker answers". */
  gainsLabel?: string
  /** Under the steps: a job's console while the step runs. */
  after?: React.ReactNode
  size?: "page" | "block"
  reason?: string
  className?: string
}) {
  const page = size === "page"
  const mark = (
    <ProductLogo
      id={hasProductLogo(thing) ? thing : undefined}
      fallback={Icon}
      size={page ? "lg" : "md"}
    />
  )
  const steps = (step || more) && (
    <div
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-y-3",
        page ? "gap-x-4" : "gap-x-2.5 gap-y-2",
      )}
    >
      {step}
      {more}
    </div>
  )
  const said = raw && (
    <details className="max-w-xl min-w-0 text-xs">
      <summary className="cursor-pointer rounded-sm text-muted-foreground focus-ring hover:text-foreground">
        What the server said
      </summary>
      <pre className="mt-1.5 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
        {raw}
      </pre>
    </details>
  )

  if (!page) {
    return (
      <div
        data-slot="missing-thing"
        data-size="block"
        data-thing={thing}
        data-reason={reason}
        className={cn("flex min-w-0 items-start gap-3.5 py-4", className)}
      >
        {mark}
        <div className="min-w-0 flex-1 space-y-3">
          <div className="space-y-1">
            <p className="text-body font-medium">{title}</p>
            <p className="max-w-xl text-xs leading-relaxed text-pretty text-muted-foreground">
              {children}
            </p>
          </div>
          {steps}
          {said}
          {after}
        </div>
      </div>
    )
  }

  return (
    <section
      aria-label={title}
      data-slot="missing-thing"
      data-size="page"
      data-thing={thing}
      data-reason={reason}
      className={cn(
        "grid min-w-0 animate-rise gap-x-14 gap-y-10 pt-2",
        gains?.length && "lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]",
        className,
      )}
    >
      <div className="min-w-0 space-y-7">
        <div className="space-y-4">
          {mark}
          <div className="space-y-2">
            <h2 className="text-2xl leading-tight font-semibold tracking-tight text-balance">
              {title}
            </h2>
            <p className="max-w-xl text-sm leading-relaxed text-pretty text-muted-foreground">
              {children}
            </p>
          </div>
        </div>
        {steps}
        {said}
        {after}
      </div>
      {gains && gains.length > 0 && (
        <ul
          aria-label={gainsLabel}
          className="min-w-0 divide-y divide-hairline self-start border-y border-hairline"
        >
          {gains.map(({ icon: GainIcon, title: name, text }) => (
            <li key={name} className="flex min-w-0 gap-3.5 py-4">
              <GainIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <div className="min-w-0 space-y-1">
                <p className="text-body font-medium">{name}</p>
                <p className="text-xs leading-relaxed text-pretty text-muted-foreground">{text}</p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/**
 * One step of a `Missing` as a control. The first of a screen is brand-faced
 * (`lead`); a line to copy shows the line beside its button, because the
 * reader should see what they are about to paste.
 */
export function MissingStepControl({
  step,
  lead,
  size = "page",
}: {
  step: MissingStep
  lead?: boolean
  size?: "page" | "block"
}) {
  const button = size === "page" ? (lead ? "lg" : "default") : "sm"
  if (step.kind === "link") {
    return (
      <Button size={button} variant={lead ? "default" : "outline"} asChild>
        <Link href={step.href}>
          {step.label}
          <ArrowRight />
        </Link>
      </Button>
    )
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <Button
        size={button}
        variant={lead ? "default" : "outline"}
        onClick={() => void copyText(step.text, "Copied")}
      >
        <Copy />
        {step.label}
      </Button>
      <code className="min-w-0 font-mono text-hint break-all text-muted-foreground">
        {step.text}
      </code>
    </span>
  )
}

/** A `Missing` from the model drawn whole: its mark, its words and its steps in order. */
export function MissingScreen({
  missing,
  size = "page",
  step,
  gains,
  gainsLabel,
  after,
  className,
}: {
  missing: Missing
  size?: "page" | "block"
  /** A step the page has and the model does not (a start, an install), drawn first. */
  step?: React.ReactNode
  gains?: MissingGain[]
  gainsLabel?: string
  after?: React.ReactNode
  className?: string
}) {
  const [first, ...rest] = missing.steps
  const own = (item: MissingStep, lead: boolean) => (
    <MissingStepControl key={item.label} step={item} lead={lead} size={size} />
  )
  return (
    <MissingThing
      thing={missing.thing}
      title={missing.title}
      raw={missing.raw}
      reason={missing.reason}
      size={size}
      step={step ?? (first && own(first, true))}
      more={
        <>
          {step && first && own(first, false)}
          {rest.map((item) => own(item, false))}
        </>
      }
      gains={gains}
      gainsLabel={gainsLabel}
      after={after}
      className={className}
    >
      {missing.sentence}
    </MissingThing>
  )
}

/**
 * A read that failed, as what it is. Where the answer names an absence
 * (Docker did not answer, there is no systemd, the module is off) that is
 * drawn, with its step; anything else is the failure `ErrorState` draws.
 */
export function MissingOrError({
  error,
  onRetry,
  retryable,
  size = "block",
  systemd,
  canConfigure,
  className,
}: {
  error: Error
  onRetry?: () => void
  retryable?: boolean
  size?: "page" | "block"
  /** What this page reads from systemd, for the sentence. */
  systemd?: SystemdReader
  canConfigure?: boolean
  className?: string
}) {
  const missing = missingFromError(error, { systemd, canConfigure })
  if (!missing) {
    return (
      <ErrorState error={error} onRetry={onRetry} retryable={retryable} className={className} />
    )
  }
  return <MissingScreen missing={missing} size={size} className={className} />
}

/**
 * The same fact as one line, for a place that is already inside something: a
 * rail's group, an inspector, a pane's footer. The thing's own mark, the
 * sentence, and the way to the page that fixes it.
 */
export function MissingLine({
  thing,
  icon: Icon = Slash,
  children,
  href,
  label,
  className,
}: {
  thing?: string
  /** The glyph where the thing is no product. */
  icon?: Glyph
  children: React.ReactNode
  href?: string
  /** The link's words: "Open Docker". */
  label?: string
  className?: string
}) {
  return (
    <p
      data-slot="missing-line"
      data-thing={thing}
      className={cn(
        "flex min-w-0 items-start gap-2 text-hint leading-snug text-muted-foreground",
        className,
      )}
    >
      {/* The mark keeps the first line's height, so it sits beside the
          sentence's first line when the link wraps under it. */}
      <span aria-hidden className="flex h-[1.375em] shrink-0 items-center">
        {hasProductLogo(thing) ? (
          <ProductGlyph id={thing} />
        ) : (
          <Icon className="size-3.5 shrink-0" />
        )}
      </span>
      <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
        <span className="min-w-0 text-pretty">{children}</span>
        {href && label && (
          <Link
            href={href}
            className="inline-flex shrink-0 items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
          >
            {label}
            <ArrowRight className="size-3" />
          </Link>
        )}
      </span>
    </p>
  )
}
