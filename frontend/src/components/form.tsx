"use client"

import { useId, useState } from "react"

import { ChevronDown, Copy, Information } from "@/components/icons"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"
import type { Tone } from "@/components/tone"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * The form vocabulary for a task surface.
 *
 * Every dialog in the databases section — create a table, add a column,
 * connect a server, import a file, edit a row — had assembled its own form out
 * of `Label`, `Input` and a `space-y-1.5` div, and between them they had
 * arrived at three label sizes, two input heights, four spellings of a hint
 * and no way at all of writing an error next to the field that caused it. A
 * reader opening two of those dialogs in a row saw two products.
 *
 * One vocabulary: a `Field` is a label, a control and one line under it; a
 * `FieldRow` puts two or three side by side; a `FormSection` opens a part of a
 * longer form with a title and a hairline, and a `Disclosure` is the same head
 * with the part folded away behind it; an `OptionRow` is a switch with
 * its sentence, because a checkbox labelled "Stop on error" asks the reader to
 * guess what an error stops; `FormFacts` states what the form operates on; and
 * `Statement` shows the SQL a form is about to run, which is the one thing a
 * schema-editing dialog owes the person pressing its button.
 */

/** One labelled control, its hint, and — when the value is wrong — why. */
export function Field({
  label,
  htmlFor,
  hint,
  info,
  error,
  trailing,
  className,
  children,
}: {
  label: React.ReactNode
  htmlFor?: string
  /** What goes here, in one line. Replaced by the error while there is one. */
  hint?: React.ReactNode
  /**
   * The reasoning behind the setting, behind a ⓘ beside the label. The hint
   * is what the reader needs while typing; this is the paragraph they want
   * once and never again, and a form that printed it under every field was
   * unreadable.
   */
  info?: React.ReactNode
  error?: React.ReactNode
  /** A control at the label's right edge: a null switch, a "leave blank" note. */
  trailing?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <div className="flex min-w-0 items-center justify-between gap-3">
        {/* The tip sits beside the label rather than inside it: a button is
            a labelable element, and a label may not contain one. */}
        <div className="flex min-w-0 items-center gap-1.5">
          <Label htmlFor={htmlFor} className="min-w-0 text-body font-medium">
            {label}
          </Label>
          {info && <InfoTip>{info}</InfoTip>}
        </div>
        {trailing && <div className="flex shrink-0 items-center gap-2">{trailing}</div>}
      </div>
      {children}
      {error ? (
        <p role="alert" className="text-hint leading-relaxed text-destructive">
          {error}
        </p>
      ) : (
        hint && <p className="text-hint leading-relaxed text-muted-foreground">{hint}</p>
      )}
    </div>
  )
}

/** The ⓘ a `Field` draws for its `info`, for a label a form lays out itself. */
export function InfoTip({ children }: { children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="What this setting does"
          className="text-muted-foreground/60 transition-colors hover:text-foreground"
        >
          <Information className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs text-xs leading-relaxed text-balance">
        {children}
      </TooltipContent>
    </Tooltip>
  )
}

/** Two or three fields on one line; one under another on a phone. */
export function FieldRow({
  columns = 2,
  className,
  ...props
}: React.ComponentProps<"div"> & { columns?: 2 | 3 }) {
  return (
    <div
      className={cn(
        "grid gap-3 [&>*]:min-w-0",
        columns === 2 ? "sm:grid-cols-2" : "sm:grid-cols-3",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A titled part of a longer form.
 *
 * The title is `text-title` semibold in the foreground, not the eyebrow's 10px
 * muted small caps. §8 makes this argument once already, about a panel's name:
 * a heading set at the size of the rows underneath it is legible but is not a
 * heading. The eyebrow was worse than that — a Configure screen of nine
 * sections put every one of its heads at 10px grey *under* the 13px white
 * field labels they opened, so the loudest line in each block was a label and
 * the page had no structure a reader could see from a metre away.
 *
 * It went to 14 first, and 14 was not enough: an `OptionRow`'s title is 14 to
 * outrank the fields it governs, so "Public address" and "Publish on a public
 * hostname" sat two lines apart at one size, separated by a weight step and
 * nothing else. 15 is the rung above, and the run now reads section (15) →
 * option (14) → field (13) → hint (11) with four visible steps and nothing
 * invented for any of them. It meets a `Modal`'s own title at the same size in
 * a dialog, which is correct and not a collision: that one sits in a bordered
 * header strip above the body, and these are inside it under hairlines.
 */
export function FormSection({
  id,
  title,
  hint,
  actions,
  className,
  children,
}: {
  /** A scroll target, for a page that sends a reader to one of its sections. */
  id?: string
  title: React.ReactNode
  hint?: React.ReactNode
  actions?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <section id={id} className={cn("min-w-0 space-y-3", className)}>
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-4 gap-y-1 border-b border-hairline pb-2">
        <div className="min-w-0">
          <h3 className="min-w-0 text-title font-semibold tracking-tight">{title}</h3>
          {hint && <p className="mt-0.5 text-hint text-muted-foreground">{hint}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
      </div>
      {children}
    </section>
  )
}

/**
 * A part of a form that is folded away until it is wanted.
 *
 * One spelling, because the Configure screen had four of these and three
 * spellings of them: two bare `<summary>`s at different sizes with the
 * browser's own triangle, and one full-width framed button carrying
 * `bg-surface-header` — a token §7 reserves for a `Pane`'s chrome — a
 * `SettingsSliders` glyph §14 does not allow a header, and the word "Show" at
 * the far end. Three answers to "there is more here" on one surface is the
 * drift this file exists to stop.
 *
 * A native `<details>`, so a page can still open one by setting `.open` on it
 * (which is how a preflight finding reaches a control folded away inside one)
 * and so the browser's own find-in-page can reveal it. The marker is the one
 * the Configuration page's `Detail` and the restart transcript already draw —
 * a `ChevronDown` that turns on open — rather than the platform triangle,
 * which is the only mark on those screens not drawn by this design system.
 *
 * **A fold has to look like one.** Unifying the three spellings left a left
 * chevron in front of a 13px word, floating in a column of sections that all
 * began with a title and a rule — so the two largest parts of the Configure
 * screen, the build settings and Advanced, read as stray lines of text rather
 * than as the halves of the form they hold. What answers "there is more here"
 * is the same anatomy a section already uses: the title at a section's rank,
 * a rule under it once it is open, and the chevron at the far end of a
 * full-width row that washes under the pointer, which is how everything else
 * in this product says *press me*. The rule is drawn above the body rather
 * than under the head so a closed fold is a head and nothing else — a
 * hairline hanging under a collapsed row is an edge belonging to no surface.
 *
 * `facts` is what is inside, said while it is shut. A fold whose name is
 * "Advanced" tells the reader nothing about whether their answer is in there,
 * and the cost of finding out is a press and a wall of fields (§8's data, not
 * captions). It is drawn *in* the `<summary>` — a `<details>` renders none of
 * its other children until it is open, which is the one place the thing that
 * must be readable while shut cannot go — so it joins the control's
 * accessible name, and that is the right name: "Advanced, resources, health
 * check, storage" is what the control opens.
 *
 * `quiet` is the other rank: a peek at a pasted block, a clone option, an
 * effective plan — a fold *inside* a section rather than one that is a section.
 *
 * The summary carries `role="button"` and `aria-expanded`. A native
 * `<summary>` is a disclosure widget the platform knows about, but it exposes
 * no expanded state that a caller — or an assistive technology, or a test —
 * can read, and the control this replaced was a real `<button aria-expanded>`
 * that did. Saying it explicitly costs two attributes and keeps the contract
 * the folded sections had before they were unified.
 */
export function Disclosure({
  id,
  summary,
  facts,
  quiet,
  open,
  onOpenChange,
  className,
  children,
}: {
  /** A scroll target, for a page that sends a reader into a folded section. */
  id?: string
  summary: React.ReactNode
  /** What the fold holds, drawn under the head while it is closed. */
  facts?: React.ReactNode
  /** A fold inside a section, rather than one that is a section of its own. */
  quiet?: boolean
  /** Controlled callers only; leave both out for one that remembers nothing. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  className?: string
  children: React.ReactNode
}) {
  // Seeded from `open` and thereafter fed by the element's own `toggle`, which
  // is the only thing that knows the truth. Deriving it as `open ?? selfOpen`
  // was wrong for a caller that passes `open` without `onOpenChange` — the
  // build fold does, because its initial state is computed from detection and
  // there is nowhere to store the operator's later answer. React writes the
  // `open` property only when the prop *changes*, so the reader's press stood
  // in the DOM while `aria-expanded` kept repeating the prop: a fold that
  // announced itself collapsed with its fields on screen, and the other way
  // round. `toggle` fires for a programmatic change too, so a controlled
  // caller stays in step.
  const [selfOpen, setSelfOpen] = useState(Boolean(open))
  const expanded = selfOpen
  return (
    <details
      id={id}
      open={open}
      onToggle={(event) => {
        setSelfOpen(event.currentTarget.open)
        onOpenChange?.(event.currentTarget.open)
      }}
      className={cn("group/disclosure min-w-0", className)}
    >
      {/* Bled by its own padding, so a section fold's edge sits just inside the
          body's while its title stays in line with the section titles above
          it, and a quiet fold's wash reaches the same edge a row's does. */}
      <summary
        role="button"
        aria-expanded={expanded}
        className={cn(
          "flex min-h-11 cursor-pointer list-none items-center gap-3 focus-ring transition-colors [&::-webkit-details-marker]:hidden",
          quiet
            ? "-mx-3 rounded-md px-3 py-1 text-body font-medium hover:bg-row-hover sm:min-h-9"
            : // A section fold gets a control's face at rest.
              //
              // Made to look exactly like a `FormSection`'s head, it *was* one:
              // same rank, same weight, same place in the column, and the only
              // difference a 16px chevron at the far right of a 900px row, a
              // thousand pixels from the word it belongs to. Nothing on it said
              // "press me" until the pointer was already on it — which is no
              // use, because the reader has to decide to go there first.
              //
              // So it stops being a heading and becomes what it is: a wide
              // button. `--control` over `--input`, which is the resting
              // anatomy of every other control on the screen — the outline
              // button, the select, the field. §2 allows a control a face; what
              // it refuses is a block pretending to be one, and the strip this
              // replaced was refused for the wrong ground (`--surface-header`,
              // a Pane's chrome), a glyph §14 bans and the word "Show", not for
              // having an edge. The chevron is now anchored to that edge
              // instead of floating at the end of a rule.
              "rounded-lg border border-input bg-control px-3 py-2 text-title font-semibold tracking-tight hover:border-border-strong hover:bg-control-hover active:bg-control-active sm:min-h-10",
        )}
      >
        {/* A quiet fold is a peek, not a section, so its marker leads: at the
            far end of a line of body text it reads as unrelated punctuation. */}
        {quiet && (
          <ChevronDown
            aria-hidden
            className="size-3.5 shrink-0 text-muted-foreground transition-transform group-open/disclosure:rotate-180"
          />
        )}
        <span className="min-w-0 flex-1 truncate">{summary}</span>
        {facts && (
          // A third of the row, never more: `flex-1` gives the title a flex
          // base of zero, so a facts line sized by its own content claimed the
          // width it wanted first and left the title to truncate — a long
          // build summary squeezed "Build & output settings" towards nothing.
          // Both sides truncate now, and the share between them is fixed.
          // Not on a phone, where the title has the width; not once it is
          // open, where the fields themselves say it.
          <span className="min-w-0 shrink basis-1/3 truncate text-right text-hint font-normal text-muted-foreground group-open/disclosure:hidden max-sm:hidden">
            {facts}
          </span>
        )}
        {!quiet && (
          <ChevronDown
            aria-hidden
            className="size-4 shrink-0 text-muted-foreground transition-transform group-open/disclosure:rotate-180"
          />
        )}
      </summary>
      {/* No rule over the body any more: the head has its own bottom edge now,
          and a hairline a pixel under a border is two edges saying one thing. */}
      <div className={cn("min-w-0 pb-1", quiet ? "pt-2" : "pt-4")}>{children}</div>
    </details>
  )
}

/** A run of `OptionRow`s, separated by hairlines. */
export function OptionList({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("min-w-0 divide-y divide-hairline", className)} {...props} />
}

/**
 * A switch with its sentence.
 *
 * "Stop on error" as a checkbox label asks the reader to guess what an error
 * stops and what happens to the rows before it. The title names the option and
 * the hint says what choosing it does, which is the only form most of these
 * are usable in. A `danger` tone marks the one option in a list that loses
 * something.
 *
 * Three things make the pair read as one control rather than as a sentence
 * with a switch stranded a thousand pixels away from it:
 *
 * **The switch is the product's switch.** It was `size="sm"` — 14px tall,
 * shorter than the 13px line of text naming it and the only place in the app
 * drawing that size. It is the default 18px now, the same one every other
 * switch in the product is.
 *
 * **The row is a row.** A wash under the whole label on hover joins the two
 * ends: the reader sees one target lighting up, not text and a control that
 * happen to share a line. The text stops at a readable measure rather than
 * running the full width of a 1200px panel, so the gap before the switch is
 * deliberate space instead of a void.
 *
 * **What the switch reveals is indented behind a rule.** An option's fields
 * were siblings of it at the same rank: on Configure, "Hostname" sat directly
 * under "Publish on a public hostname" in the same 13px medium, so nothing
 * said the field existed *because* the switch was on. The rule is the
 * containment §7 gives a `Group`, drawn as one line rather than four sides
 * because this fence has a head already.
 *
 * The title is `text-sm` against a field label's `text-body`. An option
 * governs the fields under it and has to outrank them, and the rung is one the
 * ladder already has (§8) — a `FormSection`'s head is the same size at
 * semibold, so the run reads section → option → field → hint without a size
 * being invented for any of them.
 */
export function OptionRow({
  title,
  hint,
  checked,
  onCheckedChange,
  disabled,
  tone = "default",
  children,
}: {
  title: React.ReactNode
  hint?: React.ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  tone?: Tone
  /** An extra control the option reveals — a field that only matters when it is on. */
  children?: React.ReactNode
}) {
  // The wrapping label already names the switch for assistive technology;
  // the explicit reference keeps the name where tooling that only reads
  // `aria-*` attributes (the design-system browser check included) can see it.
  const titleId = useId()
  return (
    <div className="min-w-0 py-1.5 first:pt-0 last:pb-0">
      {/* Centred, not top-aligned. The switch used to hang off the title's
          line, so the moment a row was two lines tall it sat against the top
          edge of a box it was nowhere near the middle of and read as having
          slipped. It is one control: both ends of it sit on the same axis. */}
      <label className="-mx-3 flex min-h-11 min-w-0 cursor-pointer items-center justify-between gap-6 rounded-md px-3 py-2 transition-colors hover:bg-row-hover sm:min-h-9">
        {/* Capped, not because the sentence is long but because a hint set
            across 1100px is one line the eye has to track back from — and
            because the space it stops leaving is what the switch needs to
            read as the other end of this control rather than as the next one. */}
        <span className="max-w-3xl min-w-0">
          <span
            id={titleId}
            className={cn(
              "block text-sm leading-snug font-medium",
              tone === "danger" && "text-destructive",
              tone === "warning" && "text-warning",
            )}
          >
            {title}
          </span>
          {hint && (
            <span className="mt-1 block text-hint leading-relaxed text-muted-foreground">
              {hint}
            </span>
          )}
        </span>
        <Switch
          className="shrink-0"
          aria-labelledby={titleId}
          checked={checked}
          onCheckedChange={onCheckedChange}
          disabled={disabled}
        />
      </label>
      {children && checked && (
        <div className="mt-2 ml-1 border-l border-hairline pl-4">{children}</div>
      )}
    </div>
  )
}

/**
 * What the form operates on, said as data under the title rather than as a
 * sentence: the schema a table lands in, the address a server was found at.
 */
export function FormFacts({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground",
        className,
      )}
      {...props}
    />
  )
}

export function FormFact({
  label,
  mono,
  children,
}: {
  label: React.ReactNode
  /** The value is a literal from the host — a name, an address, a path. */
  mono?: boolean
  children: React.ReactNode
}) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5">
      <span className="shrink-0">{label}</span>
      <span className={cn("min-w-0 truncate text-foreground", mono && "font-mono")}>
        {children}
      </span>
    </span>
  )
}

/**
 * The statement a form will run, shown before it runs.
 *
 * A DDL form that hides its SQL asks the operator to trust a black box with
 * their schema; showing it costs a few lines and turns the form into something
 * that can also be learned from. The server renders the real statement in the
 * engine's own dialect — this is drawn from the same fields, so the shape is
 * right even where a keyword differs.
 */
export function Statement({
  label = "Statement",
  sql,
  placeholder,
  className,
}: {
  label?: React.ReactNode
  /** Empty while the form is incomplete, which shows the placeholder instead. */
  sql: string
  placeholder: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <div className="flex min-h-6 items-center justify-between gap-3">
        <p className="eyebrow">{label}</p>
        {sql && (
          <Button size="xs" variant="ghost" onClick={() => void copyText(sql, "Statement copied")}>
            <Copy />
            Copy
          </Button>
        )}
      </div>
      <Well className="max-h-44 text-hint leading-relaxed whitespace-pre-wrap">
        {sql || <span className="text-muted-foreground italic">{placeholder}</span>}
      </Well>
    </div>
  )
}

/** A short piece of guidance under a form, quieter than a `Notice`. */
export function FormNote({
  tone = "default",
  className,
  ...props
}: React.ComponentProps<"p"> & { tone?: Tone }) {
  return (
    <p
      className={cn(
        "text-hint leading-relaxed",
        tone === "default" && "text-muted-foreground",
        tone === "danger" && "text-destructive",
        tone === "warning" && "text-warning",
        tone === "success" && "text-success",
        className,
      )}
      {...props}
    />
  )
}
