import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/**
 * The one content block in the dashboard.
 *
 * Every page is a stack of these: a framed surface with an optional header
 * strip, an optional toolbar under it, and a body. Before this existed each
 * page assembled its own out of Card/CardHeader/CardContent with whatever
 * padding and title size that page's author picked, which is why fourteen
 * pages read as fourteen products. A panel has exactly one look, and the props
 * decide what it contains rather than how it is drawn.
 *
 * The header is a strip with its own tint and a hairline under it, not a block
 * of padding sharing the body's ground — that is what makes a panel legible as
 * "chrome, then content" at a glance, and what lets a toolbar or a full-bleed
 * table sit flush beneath it without inventing a second edge.
 *
 * It does not lift. A panel separates from the page by its own ground being a
 * step above it and by a single border — the gloss, shine, lip and drop shadow
 * it used to carry said the same thing four more times, and once every surface
 * on a page was saying it, none of them were.
 */
export function Panel({ className, children, ...props }: React.ComponentProps<"section">) {
  return (
    <section
      data-slot="panel"
      className={cn(
        "flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card text-card-foreground",
        className,
      )}
      {...props}
    >
      {children}
    </section>
  )
}

/**
 * A panel's chrome strip: what this block is, and what you can do to it.
 *
 * **No icon.** Every header used to open with a brand-tinted plot and a glyph
 * inside it, which on a page of six panels is six orange marks competing with
 * the one control the reader is meant to press — and none of them said anything
 * the title beside them did not already say. A `Servers` box in front of the
 * word "Filesystems" is decoration with a colour budget. The title is the
 * panel's name, drawn one step up the ladder now that it stands alone, and the
 * header's ground and hairline do the separating the plot was crowding.
 *
 * Icons still carry meaning elsewhere — a `Status`'s verdict, a `Notice`'s
 * severity, a verb on a button — because there the glyph *is* the message.
 */
export function PanelHeader({
  title,
  eyebrow,
  actions,
  advanced,
  className,
  children,
}: {
  title?: React.ReactNode
  eyebrow?: React.ReactNode
  actions?: React.ReactNode
  /**
   * Marks a panel that assumes the reader already knows the thing it operates
   * on — an sshd_config editor, the host's raw systemd units.
   *
   * The dashboard explains Docker to somebody who has never met it, and two
   * clicks from that explanation are controls where a wrong answer locks you
   * out of the server. Presenting both with identical confidence leaves a
   * newcomer no way to tell which is which. This is the smallest honest
   * signal: not a lock, not a warning — a note that skipping this panel is a
   * valid thing to do.
   */
  advanced?: boolean
  className?: string
  children?: React.ReactNode
}) {
  return (
    <header
      data-slot="panel-header"
      className={cn(
        "flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline bg-surface-header px-4 py-2.5",
        className,
      )}
    >
      <div className="min-w-0">
        {eyebrow && <p className="eyebrow mb-0.5">{eyebrow}</p>}
        {title && (
          <h2 className="flex min-w-0 items-center gap-1.5 text-title leading-tight font-medium">
            <span className="truncate">{title}</span>
            {advanced && (
              <span
                className="shrink-0 text-micro font-medium tracking-[0.06em] text-muted-foreground/80 uppercase"
                title="Assumes you already administer this directly. Nothing here needs your attention unless you came looking for it."
              >
                Advanced
              </span>
            )}
          </h2>
        )}
      </div>
      {children}
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-1.5">{actions}</div>}
    </header>
  )
}

/**
 * A strip of filters directly under a panel header.
 *
 * Kept on the header's ground rather than the body's, so search inputs and
 * segmented controls read as part of the frame and the body below is only the
 * data. Also the reason a filter row never scrolls away with the rows it
 * filters.
 */
export function PanelToolbar({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="panel-toolbar"
      className={cn(
        "flex flex-wrap items-center gap-2 border-b border-hairline bg-surface-header/60 px-3 py-2",
        className,
      )}
      {...props}
    />
  )
}

/**
 * `flush` drops the padding for a body that is one full-bleed table or list,
 * and is also what the alignment rule in globals.css keys off so those tables
 * still line their outer columns up with the header's text.
 */
export function PanelBody({
  className,
  flush,
  scroll,
  ...props
}: React.ComponentProps<"div"> & { flush?: boolean; scroll?: boolean }) {
  return (
    <div
      data-slot="panel-body"
      data-flush={flush ? "" : undefined}
      className={cn(
        "min-w-0",
        flush ? "" : "p-4",
        scroll && "min-h-0 flex-1 overflow-auto",
        className,
      )}
      {...props}
    />
  )
}

export function PanelFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="panel-footer"
      className={cn(
        // mt-auto so a row of panels stretched to a common height still ends
        // with their footers on the same line rather than mid-card.
        "mt-auto flex flex-wrap items-center gap-2 border-t border-hairline bg-surface-header/60 px-4 py-2.5",
        className,
      )}
      {...props}
    />
  )
}

/**
 * The other framed surface: a working region of the page rather than a block of
 * content on it — the terminal's session rail, a file tree, a log console, a
 * diff viewer, the git tools column.
 *
 * Structurally identical to `Panel` now that neither lifts; the distinction is
 * semantic and lives in how the two are composed — a panel is a block of
 * content stacked in a page's flow, a pane is a sized region of a workspace
 * that owns its own scrolling. Kept as two names because call sites read
 * correctly that way and because the two headers are deliberately different
 * heights.
 */
export function Pane({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="pane"
      className={cn(
        "flex min-h-0 min-w-0 flex-col overflow-hidden rounded-xl border bg-card",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A pane's chrome strip.
 *
 * This existed as a hand-typed class string in about twenty places and had
 * arrived at nineteen different paddings, so no two panes in a split view were
 * the same height at the top and the eye read the whole screen as slightly
 * misaligned. `px-2 py-1.5` is the terminal rail's, which is the one the brief
 * called correct.
 *
 * It is tighter than `PanelHeader` on purpose: a panel's header carries a
 * title and actions for a block of content, and a pane's carries a name and two
 * icon buttons for a region of the screen.
 */
export function PaneHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="pane-header"
      className={cn(
        "flex min-h-9 shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-2 py-1.5",
        className,
      )}
      {...props}
    />
  )
}

/** A pane's closing strip: a line count, a retention note, an export button. */
export function PaneFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="pane-footer"
      className={cn(
        "mt-auto flex min-h-9 shrink-0 flex-wrap items-center gap-2 border-t border-hairline bg-surface-header px-2 py-1.5",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A recessed well: command output, a log tail, a diff, a stored secret.
 *
 * Its ground is mixed towards the page background rather than being a flat
 * black, which is the only version of this that survives a light palette.
 */
export function Well({
  plain,
  className,
  ...props
}: React.ComponentProps<"div"> & {
  /**
   * A recessed region that holds controls or prose rather than output. Same
   * ground, without the monospace: three places wanted "sunken but not code"
   * and each re-drew the frame instead, which is how `bg-surface-sunken` ended
   * up on three different radius-and-padding combinations.
   */
  plain?: boolean
}) {
  return (
    <div
      data-slot="well"
      className={cn(
        "overflow-auto rounded-lg border border-hairline bg-surface-sunken p-3",
        plain ? "min-w-0" : "font-mono text-xs leading-relaxed",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A fenced group of fields inside a body: a set of ports, one release task, a
 * schedule, a repeated row in a form.
 *
 * Not a `Panel` — it has no header strip and does not lift, because it is not a
 * block of content sitting on the page; it is a fence around part of one. Not a
 * `Well` either: a well is *recessed*, for output you read rather than controls
 * you operate.
 *
 * This existed 66 times as a hand-typed class string and had arrived at two
 * radii at the same nesting depth (`rounded-lg` and `rounded-xl`), four grounds
 * (none, `bg-muted/20`, `bg-surface-header`, `bg-surface-sunken`) and three
 * paddings — the deployment wizard alone drew it thirty times. One radius, one
 * padding, and a `tone` for the two cases that need to say something: a group
 * holding an error and a group holding a warning.
 *
 * A group that is a *set of related inputs* — and so wants the grouping said out
 * loud — takes `role="group"` with an `aria-labelledby`, which is what a
 * `fieldset` would have given it. It stays a `div` because a `fieldset` brings
 * its own layout quirks and cannot be flexed reliably across browsers.
 */
export function Group({
  tone = "default",
  tinted,
  className,
  ...props
}: React.ComponentProps<"div"> & {
  tone?: Tone
  /** A group that should read as a distinct region rather than just fenced. */
  tinted?: boolean
}) {
  return (
    <div
      data-slot="group"
      className={cn(
        "min-w-0 rounded-lg border p-3",
        tone === "default" && "border-hairline",
        tone === "warning" && "border-rule-warning bg-wash-warning",
        tone === "danger" && "border-rule-danger bg-wash-danger",
        tone === "success" && "border-rule-success bg-wash-success",
        tinted && tone === "default" && "bg-surface-header/50",
        className,
      )}
      {...props}
    />
  )
}
