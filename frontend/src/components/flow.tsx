"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Check } from "@/components/icons"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { cn } from "@/lib/utils"

/**
 * The flow register (§16).
 *
 * The reading register draws a page that *reports*: flat, hairlined, and loud
 * only where a figure is. It is right for the Overview and wrong for a screen
 * that is asking the reader a question, because the compensations it hands
 * back for removing decoration — a 24px figure, a tone on a reading, a Status
 * dot — all require the page to have numbers on it. A chooser has few, and
 * spends them at the wrong rung: /deploy/new rendered four counts at 11px
 * inside panel headers and fired the 24px step exactly once, on its own
 * two-word title. That is the whole reason this file exists.
 *
 * Being in this register is not an exemption from §15 pass 2. A flow screen
 * with a genuine set of headline figures still tiles them.
 *
 * What a flow screen has that a reading page does not:
 *
 *   a question rather than a name, at the page's own rank;
 *   a spine saying which of the steps this is;
 *   exactly one surface carrying depth — the thing being decided right now;
 *   exactly one brand command — the thing that advances;
 *   a choice drawn as something you pick, with its edge answering the pointer.
 *
 * And what it still does not get: no glass, no glow, no gradient ground, no
 * hover-transform, no shadow on anything that is not the focused surface, no
 * pill, no badge, no icon plate. The bans in §2, §4 and §14 are not relaxed
 * here — §16 buys depth and response, and nothing else.
 */

/**
 * The head of a flow screen: where you are, what is being asked, and how far
 * through you are.
 *
 * The question is the `h1`. It is not a sentence under the page's name —
 * that is the caption §5 removed from every page in the product, and putting
 * one back at a rank invented for it is how the ladder grew thirteen sizes the
 * first time. The step count lives in the eyebrow, where the reading register
 * already puts "which part of the product is this".
 */
export function FlowHeader({
  eyebrow,
  question,
  actions,
  steps,
  className,
}: {
  /** Where this sits — "New project", and the step, if there is a sequence. */
  eyebrow?: React.ReactNode
  /** What the screen is asking. A question, not a label for itself. */
  question: React.ReactNode
  actions?: React.ReactNode
  steps?: React.ReactNode
  className?: string
}) {
  return (
    <div data-slot="flow-header" className={cn("flex min-w-0 flex-col gap-4", className)}>
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 space-y-1.5">
          {eyebrow && <p className="eyebrow">{eyebrow}</p>}
          <h1 className="min-w-0 text-2xl leading-tight font-semibold tracking-tight text-balance">
            {question}
          </h1>
        </div>
        {actions && (
          <div className="flex max-w-full shrink-0 flex-wrap items-center gap-2">{actions}</div>
        )}
      </div>
      {steps}
      {/* One pixel, and the only gradient in the product: it marks the head of
          a sequence on a page where every other separation is also a hairline.
          A spine is already that mark, and more explicitly — drawing both put
          three rules in a hundred pixels, each saying the same thing more
          faintly than the one above it. */}
      {!steps && <div aria-hidden className="h-px w-full flow-rule" />}
    </div>
  )
}

/**
 * The spine: which step this is, what is behind it, what is left.
 *
 * Drawn as segments of a rule with their names under them, the way
 * `deploy/run-pipeline.tsx` draws the seven stages of a release — so the
 * screen where a project is planned and the screen where it is built say
 * "where we are" the same way.
 *
 * Deliberately **not** numbered circles. A filled circle under 32px with a
 * character in it is the shape §4 deleted from this product, and
 * `tests/browser/design-system.spec.ts` fails any page that renders one. The
 * count belongs in words in the eyebrow ("Step 2 of 3"), where it can be read
 * rather than decoded.
 */
export function FlowSteps({
  steps,
  current,
  className,
}: {
  steps: { key: string; label: string }[]
  /** Index of the step being worked on. Everything before it is done. */
  current: number
  className?: string
}) {
  return (
    <ol
      data-slot="flow-steps"
      aria-label="Progress"
      className={cn("flex min-w-0 gap-3", className)}
    >
      {steps.map((step, index) => {
        const done = index < current
        const here = index === current
        // A reading, not navigation. A completed step is not a link back:
        // going back to the source on this flow throws the draft away — that
        // is what Configure's own "Change source" is for, and it says so —
        // and a progress mark that silently discards minutes of work the
        // moment it is mistaken for a breadcrumb is the worst kind of
        // affordance. The spine says where you are and nothing else.
        const label = (
          <>
            <span
              aria-hidden
              className={cn(
                "block h-0.5 w-full rounded-sm transition-colors",
                done && "bg-flow-lit",
                here && "bg-brand",
                !done && !here && "bg-meter-track",
              )}
            />
            <span
              className={cn(
                "flex min-w-0 items-center gap-1 truncate text-hint transition-colors",
                here ? "font-medium text-foreground" : "text-muted-foreground",
              )}
            >
              {done && <Check aria-hidden className="size-3 shrink-0 text-brand" />}
              <span className="truncate">{step.label}</span>
            </span>
          </>
        )
        return (
          <li
            key={step.key}
            // The current step is the one worth room on a phone; the rest
            // keep their rule and let their name shrink.
            className={cn("min-w-0 space-y-1.5", here ? "flex-[2]" : "flex-1")}
            aria-current={here ? "step" : undefined}
          >
            {label}
          </li>
        )
      })}
    </ol>
  )
}

/**
 * The one surface on a flow screen that carries depth.
 *
 * §2 says nothing lifts, and the reason is that forty-nine surfaces all
 * claiming the foreground is a page with no foreground. One does not have that
 * problem: a flow screen has exactly one thing being decided at a time, and
 * this is it. Its ground is a step above the card, its border is the strong
 * one, and it takes `shadow-sm` — the smallest step, and the only place
 * outside a popover, a dropdown and a dialog that takes any.
 *
 * **One per screen.** A second one is two foregrounds, which is none.
 */
export function FlowPanel({ className, children, ...props }: React.ComponentProps<"section">) {
  return (
    <section
      data-slot="flow-panel"
      className={cn(
        "flex min-w-0 flex-col overflow-hidden rounded-xl border border-border-strong bg-flow-surface shadow-sm",
        className,
      )}
      {...props}
    >
      {children}
    </section>
  )
}

/** The head of a focused surface: its name, and whatever states it. */
export function FlowPanelHeader({
  title,
  actions,
  className,
}: {
  title: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="flow-panel-header"
      className={cn(
        "flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline px-4 py-3",
        className,
      )}
    >
      <h2 className="min-w-0 truncate text-title font-semibold tracking-tight">{title}</h2>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/**
 * The body of a focused surface.
 *
 * Register B keeps its own body rather than borrowing `PanelBody`: that one
 * decides its padding from `group-data-[plain]/panel`, a group a `FlowPanel`
 * deliberately does not declare, so the coupling would work only by accident
 * and would break the first time either file moved.
 */
export function FlowPanelBody({
  className,
  flush,
  ...props
}: React.ComponentProps<"div"> & { flush?: boolean }) {
  return (
    <div
      data-slot="flow-panel-body"
      className={cn("min-w-0", flush ? "" : "p-4", className)}
      {...props}
    />
  )
}

/**
 * The foot of a flow screen: the one command that advances it, and the ways
 * out beside it.
 *
 * The command is a `Button` with no variant — the brand face — and there is
 * exactly one. A flow screen whose primary action is an outline button is the
 * defect this register was written for: the Git tab shipped with both of its
 * buttons `variant="outline"`, so the page's only blue was a 14px icon on the
 * active tab and nothing on it looked pressable.
 */
export function FlowActions({
  children,
  secondary,
  note,
  className,
}: {
  /** The one command. */
  children: React.ReactNode
  /** Back, cancel, save-without-deploying: drawn before it, quieter. */
  secondary?: React.ReactNode
  /** What the command is about to do, where that is not obvious from its word. */
  note?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="flow-actions"
      className={cn(
        "flex min-w-0 flex-wrap items-center justify-end gap-x-6 gap-y-3 border-t border-hairline px-4 py-3",
        className,
      )}
    >
      {/* The note yields the row, not the command. It was `mr-auto` on a bare
          paragraph in a wrapping row, so a sentence long enough to crowd three
          buttons pushed the *last* of them — the command — onto a line of its
          own, under the two ways out. The note shrinks and wraps; the buttons
          are one group that never breaks up. */}
      {note && <p className="min-w-0 flex-1 basis-48 text-hint text-muted-foreground">{note}</p>}
      <div className="flex shrink-0 flex-wrap items-center justify-end gap-x-3 gap-y-2">
        {secondary}
        {children}
      </div>
    </div>
  )
}

/**
 * One *instance* among many of the same kind: a repository, an image tag, a
 * container already on the host.
 *
 * The shape §12 settled on — the title is a real `<button>` carrying the verb
 * in its accessible name, and the press on the surrounding row is a
 * convenience for the pointer — with the one thing the reading register's row
 * does not have: an edge that answers the pointer, and an arrow that is always
 * drawn. A row that is a click target and looks exactly like a row that is a
 * reading is the other half of why the chooser read as a listing.
 */
export function ChoiceRow({
  leading,
  title,
  verb,
  description,
  trailing,
  actions,
  onSelect,
  href,
  disabled,
  children,
  className,
}: {
  leading?: React.ReactNode
  title: React.ReactNode
  /** The accessible name of the title control — "Import Wayy01/api". */
  verb: string
  description?: React.ReactNode
  trailing?: React.ReactNode
  /**
   * A second thing you can do to this row — discard it, rename it — drawn
   * beside the trailing readings rather than floating over them.
   *
   * It is a slot rather than an overlay because the overlay was a defect: an
   * `IconAction` positioned `absolute right-2` against the `<li>` while the
   * row reserved its space with `pr-12` against a box that `ROW_BLEED` had
   * made twelve pixels wider on each side, so the button sat on top of the
   * last reading in the row. Laid out in the flow there is nothing to
   * mis-measure. Use `DimActions` inside it: always drawn, one step of
   * opacity until the pointer is on the row (§6), because a control revealed
   * only on hover does not exist on a touch screen.
   */
  actions?: React.ReactNode
  onSelect?: () => void
  /** Where the row goes, when it navigates rather than choosing in place. */
  href?: string
  /**
   * A row that names something with nowhere to go yet — a service compose has
   * not created. It keeps its card and its actions, and loses the arrow, the
   * pointer and the control, because a card that looks pressable and does
   * nothing is the defect `ChoiceCard`'s own `disabled` exists to prevent.
   */
  disabled?: boolean
  /**
   * What the row carries beneath its line at the full width of the card — a
   * container's live readings on a screen too narrow to hold them beside its
   * name. Presses inside it still reach the row, except on a control.
   */
  children?: React.ReactNode
  className?: string
}) {
  const router = useRouter()
  const go = () => {
    if (href) router.push(href)
    else onSelect?.()
  }
  const line = (
    <>
      {leading && <span className="flex shrink-0 items-center">{leading}</span>}
      <span className="min-w-0 flex-1">
        {disabled ? (
          <span className="block max-w-full min-w-0 truncate text-body font-medium">{title}</span>
        ) : href ? (
          <Link
            href={href}
            aria-label={verb}
            onClick={(event) => event.stopPropagation()}
            className="block max-w-full min-w-0 truncate rounded-sm text-body font-medium focus-ring"
          >
            {title}
          </Link>
        ) : (
          <button
            type="button"
            aria-label={verb}
            // The row's own handler already fires on the pointer; this one
            // is for the keyboard and must not run the action twice.
            onClick={(event) => {
              event.stopPropagation()
              go()
            }}
            className="block max-w-full min-w-0 truncate rounded-sm text-body font-medium focus-ring"
          >
            {title}
          </button>
        )}
        {description && (
          <span className="block truncate text-hint text-muted-foreground">{description}</span>
        )}
      </span>
      <span className="flex shrink-0 items-center gap-2">
        {trailing}
        {!disabled && (
          <ArrowRight
            aria-hidden
            className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
          />
        )}
        {actions && (
          // The row around these is a click target, so a press that lands
          // on one of them must not also fire it: discarding a draft
          // navigated to the draft it had just deleted.
          <span onClick={(event) => event.stopPropagation()} className="flex shrink-0 items-center">
            {actions}
          </span>
        )}
      </span>
    </>
  )
  return (
    <li data-slot="choice-row" className="min-w-0">
      <SpotlightBorder radius={320}>
        <div
          onClick={(event) => {
            if (disabled) return
            // A control of its own inside the row — a copy button in the
            // second line, a switch beneath it — is not a press on the row.
            if (
              event.target !== event.currentTarget &&
              (event.target as HTMLElement).closest(CONTROL)
            )
              return
            go()
          }}
          className={cn(
            // A fixed minimum, because a description is optional and a run of
            // cards where some are a line taller than others reads as a list
            // that failed to load rather than as a list of different things.
            // A row without one centres its title in the same box; nothing
            // reserves an empty second line, which would be a row claiming
            // content it has not got.
            // The unnamed group as well, so `DimActions` in the actions slot
            // brightens with the row the way it does in a table.
            "group group/choice flex min-h-14 min-w-0 rounded-xl px-3 py-2.5 text-left",
            !disabled && "cursor-pointer",
            children ? "flex-col gap-2.5" : "items-center gap-3",
            className,
          )}
        >
          {children ? (
            <>
              <div className="flex min-w-0 items-center gap-3">{line}</div>
              {children}
            </>
          ) : (
            line
          )}
        </div>
      </SpotlightBorder>
    </li>
  )
}

/** Anything inside a row that owns its own press — mirrors `TableRow`'s rule. */
const CONTROL = "a, button, input, select, textarea, label, [role='menuitem'], [role='switch']"

/**
 * What the next run of cards is, and how many of them.
 *
 * A hairline running off to the right rather than a panel header: these are
 * parts of one list, not blocks of their own, and a second framed title under
 * the page's would rank them as sections they are not. The Git page's
 * checkouts and the Docker lists split the same way — what needs you first.
 */
export function GroupRule({ label, count }: { label: string; count: number }) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <p className="eyebrow shrink-0">{label}</p>
      <span className="numeric shrink-0 text-micro text-muted-foreground">{count}</span>
      <span aria-hidden className="h-px min-w-0 flex-1 bg-hairline" />
    </div>
  )
}

/** A run of `ChoiceRow`s. Gapped rather than hairlined: each row owns an edge. */
export function ChoiceList({ className, ...props }: React.ComponentProps<"ul">) {
  return <ul data-slot="choice-list" className={cn("min-w-0 space-y-2", className)} {...props} />
}
