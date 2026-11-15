import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { SectionLink } from "@/components/section-link"

/**
 * The one content block in the dashboard.
 *
 * Every page is a stack of these: a framed surface with an optional header,
 * an optional toolbar under it, and a body. Before this existed each page
 * assembled its own out of Card/CardHeader/CardContent with whatever padding
 * and title size that page's author picked, which is why fourteen pages read
 * as fourteen products. A panel has exactly one look, and the props decide
 * what it contains rather than how it is drawn.
 *
 * The header is no longer a tinted strip. It was a shade off the body so a
 * panel read as "chrome, then content" — and on a page of six panels that was
 * six two-tone boxes, each one announcing itself twice. The header now sits on
 * the panel's own ground with a hairline under it: the title is the chrome,
 * and the frame is the only thing saying where the block begins and ends.
 *
 * `plain` removes the frame as well. A list that is the whole of a section —
 * recent activity, the compose projects on the Docker overview, a run of
 * findings — does not need a box around it to be legible as one thing; the
 * title and a hairline do that, and the box was the reason every page felt
 * like a page of containers. A plain panel keeps the same anatomy (header,
 * toolbar, body, footer) so a block can move between framed and plain without
 * its contents changing.
 *
 * It does not lift. A framed panel separates from the page by a border and by
 * its ground being one step above it — the gloss, shine, lip and drop shadow
 * it used to carry said the same thing four more times, and once every surface
 * on a page was saying it, none of them were. `interactive` is the one hover a
 * panel takes, for a panel that is also a link: the border steps up, and
 * nothing else moves.
 */
export function Panel({
  plain,
  framed,
  interactive,
  className,
  children,
  ...props
}: React.ComponentProps<"section"> & {
  /** No frame: title, hairline, content — for a block that is its own section. */
  plain?: boolean
  /**
   * A frame, said out loud. A block is plain unless it can say why it needs
   * an edge (a table's own scrolling, a danger zone), and this is where it
   * says so. Framed is still the default; it flips to plain once every caller
   * that keeps a frame carries this prop, and `plain` then goes.
   */
  framed?: boolean
  /** The panel is a destination. Its border answers the pointer. */
  interactive?: boolean
}) {
  const isPlain = framed === undefined ? Boolean(plain) : !framed
  return (
    <section
      data-slot="panel"
      data-plain={isPlain ? "" : undefined}
      data-framed={isPlain ? undefined : ""}
      className={cn(
        "group/panel flex min-w-0 flex-col",
        isPlain ? "" : "overflow-hidden rounded-xl border bg-card text-card-foreground",
        // On a phone a frame inside a page runs to the screen's edges. The
        // page's 20px gutter, then a border, then the panel's own padding left
        // a list 318 of 390px, and names were cut a word early. Without its
        // sides the panel's padding is the page's gutter, so its rows line up
        // with everything above them, and its ground and hairlines still say
        // where the block begins. A panel that is a destination keeps its
        // card, because the card is what you press.
        !isPlain &&
          !interactive &&
          "in-data-[slot=page]:max-sm:-mx-5 in-data-[slot=page]:max-sm:rounded-none in-data-[slot=page]:max-sm:border-x-0",
        interactive && !isPlain && "transition-colors hover:border-border-strong",
        className,
      )}
      {...props}
    >
      {children}
    </section>
  )
}

/**
 * A section head's title: the 15px surface title at 600, the weight every
 * other surface title (a dialog's, a sheet's, a form section's) already had.
 * At 500 a panel's name read a step quieter than the dialog it opened.
 */
const HEAD_TITLE =
  "flex min-w-0 items-center gap-1.5 text-title leading-tight font-semibold tracking-tight"

/**
 * A panel's header: what this block is, and what you can do to it.
 *
 * **No icon.** Every header used to open with a brand-tinted plot and a glyph
 * inside it, which on a page of six panels is six brand marks competing with
 * the one control the reader is meant to press — and none of them said anything
 * the title beside them did not already say. The title is the panel's name,
 * drawn one step up the ladder now that it stands alone, and the hairline under
 * it does the separating.
 *
 * Icons still carry meaning elsewhere — a `Status`'s verdict, a `Notice`'s
 * severity, a verb on a button — because there the glyph *is* the message.
 *
 * **The one section head.** In a plain panel this is what a section is: a
 * title and a hairline (D6). `Section` and `FormSection` draw it too, so the
 * Overview's Resources and its Top processes are one head rather than two,
 * and the count, the way on and the verbs have one place each at the end.
 */
export function PanelHeader({
  title,
  eyebrow,
  level = 2,
  count,
  link,
  actions,
  advanced,
  plain,
  className,
  children,
}: {
  title?: React.ReactNode
  eyebrow?: React.ReactNode
  /**
   * The heading element, and only that: an h3 looks exactly like an h2. A
   * head under another section head (a group of panels, a page that is a
   * form) is one level down, so a screen reader's outline keeps its order.
   */
  level?: 2 | 3
  /**
   * How many there are: tabular, the hint size, muted, at the far end of the
   * title line before the actions. It stays outside the heading, so the
   * heading's name is the name alone.
   */
  count?: React.ReactNode
  /** The way on to the page this block stands for, last at the head's end. */
  link?: { href: string; label: string }
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
  /**
   * A head drawn outside a `Panel` (a `Section`, a `FormSection`), which has
   * no panel around it to say it is plain. Inside a panel, the panel decides.
   */
  plain?: boolean
  className?: string
  children?: React.ReactNode
}) {
  const Heading = level === 3 ? "h3" : "h2"
  const counted = count !== undefined && count !== null && count !== false
  return (
    <header
      data-slot="panel-header"
      className={cn(
        "flex min-h-12 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline px-5 py-3",
        // Plain: the title sits on the page's own edge, and the hairline is
        // the only rule between it and the rows beneath.
        plain
          ? "min-h-0 px-0 pt-0 pb-3"
          : "group-data-[plain]/panel:min-h-0 group-data-[plain]/panel:px-0 group-data-[plain]/panel:pt-0 group-data-[plain]/panel:pb-3",
        className,
      )}
    >
      <div className="min-w-0">
        {eyebrow && <p className="eyebrow mb-0.5">{eyebrow}</p>}
        {title && (
          <Heading className={HEAD_TITLE}>
            <span className="truncate">{title}</span>
            {advanced && (
              <span
                className="caps shrink-0"
                title="Assumes you already administer this directly. Nothing here needs your attention unless you came looking for it."
              >
                Advanced
              </span>
            )}
          </Heading>
        )}
      </div>
      {children}
      {/* Never wider than the header: a phone's 16px field grew the history's
          filter past the header, and a row that cannot shrink scrolled the
          page sideways instead of wrapping its own controls. */}
      {(counted || actions || link) && (
        <div className="flex max-w-full shrink-0 flex-wrap items-center gap-1.5">
          {counted && (
            <span data-slot="panel-count" className="numeric mr-1 text-hint text-muted-foreground">
              {count}
            </span>
          )}
          {actions}
          {link && <SectionLink href={link.href}>{link.label}</SectionLink>}
        </div>
      )}
    </header>
  )
}

/**
 * A strip of filters directly under a panel header.
 *
 * On the panel's own ground rather than a tinted one, with a hairline under
 * it, so the filters read as part of the frame and the body below is only the
 * data. Also the reason a filter row never scrolls away with the rows it
 * filters.
 */
export function PanelToolbar({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="panel-toolbar"
      className={cn(
        "flex flex-wrap items-center gap-2 border-b border-hairline px-4 py-2.5",
        "group-data-[plain]/panel:px-0",
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
 *
 * A plain panel keeps a step under the header's hairline even when flush. The
 * rule is the only thing marking where a section begins, and with nothing
 * below it the first surface the body draws — a row's hover wash, a table
 * header — butted straight into it, so the rule read as an edge *of* that
 * surface rather than as the line under the title. Twelve pixels above it,
 * eight below: enough that the hairline belongs to neither side.
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
        flush
          ? "group-data-[plain]/panel:pt-2"
          : "p-5 group-data-[plain]/panel:px-0 group-data-[plain]/panel:py-4",
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
        "mt-auto flex flex-wrap items-center gap-2 border-t border-hairline px-5 py-3",
        "group-data-[plain]/panel:px-0",
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
 *
 * `flush` drops the frame for a pane that is one column of a workbench — the
 * terminal's rail, emulator and tools column sit inside a single frame with a
 * hairline between them. Three framed panes with a gutter between them were
 * three boxes floating on the page; one frame with two rules inside it is one
 * working surface, which is what the screen is.
 */
export function Pane({
  flush,
  className,
  ...props
}: React.ComponentProps<"div"> & { flush?: boolean }) {
  return (
    <div
      data-slot="pane"
      data-flush={flush ? "" : undefined}
      className={cn(
        "flex min-h-0 min-w-0 flex-col overflow-hidden bg-card",
        !flush && "rounded-xl border",
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
 * It is tighter than `PanelHeader` on purpose, and it keeps a faint tint: a
 * pane's strip carries a name and two icon buttons for a region of the screen,
 * and in a split view the strips are what line the regions up.
 */
export function PaneHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="pane-header"
      className={cn(
        "flex min-h-9 shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-2.5 py-1.5",
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
        "mt-auto flex min-h-9 shrink-0 flex-wrap items-center gap-2 border-t border-hairline bg-surface-header px-2.5 py-1.5",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A titled group inside a pane beside the work: the files inspector's
 * Details and Contents, the map inspector's Ports, a remote session's
 * Clipboard.
 *
 * Four inspector columns drew this four ways (two eyebrow heads, a semibold
 * h2, an unruled eyebrow; the rule above in one and below in the others), so
 * moving between them read as moving between products. One title style, the
 * eyebrow, because a pane's groups are labels over a column of facts rather
 * than sections of a page; one rule, under each group and not under the last;
 * one padding.
 */
export function PaneSection({
  title,
  count,
  aside,
  level = 3,
  className,
  children,
}: {
  /** The group's name, which also names the region for a screen reader. */
  title: string
  /** How many the group holds, beside its name. */
  count?: number
  /** One control at the head's end: a copy, a toggle, a small verb. */
  aside?: React.ReactNode
  /** The heading element only; a pane whose own head is an h3 takes 4. */
  level?: 2 | 3 | 4
  className?: string
  children?: React.ReactNode
}) {
  const Heading = level === 2 ? "h2" : level === 4 ? "h4" : "h3"
  return (
    <section
      data-slot="pane-section"
      aria-label={title}
      className={cn(
        "min-w-0 space-y-2.5 border-b border-hairline px-4 py-3.5 last:border-b-0",
        className,
      )}
    >
      <div className="flex min-h-6 items-center justify-between gap-2">
        <Heading className="flex min-w-0 items-center gap-2">
          <span className="eyebrow truncate">{title}</span>
          {count !== undefined && (
            <span className="numeric text-hint text-muted-foreground">{count}</span>
          )}
        </Heading>
        {aside}
      </div>
      {children}
    </section>
  )
}

/**
 * A recessed well: command output, a log tail, a diff, a stored secret.
 *
 * Its ground is mixed towards the page background rather than being a flat
 * black, so it reads as a step down from the panel rather than as a hole.
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
 * Not a `Panel` — it has no header and does not lift, because it is not a
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
