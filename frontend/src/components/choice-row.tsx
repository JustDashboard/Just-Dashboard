"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import { Bone } from "@/components/bone"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Skeleton } from "@/components/ui/skeleton"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { cn } from "@/lib/utils"

/**
 * Anything inside a row that owns its own press — mirrors `TableRow`'s rule.
 * Exported for a card that is a click target without being a `ChoiceRow` — a
 * grid card built on `SpotlightBorder` directly — so the rule of which presses
 * it skips is written once.
 */
export const CONTROL =
  "a, button, input, select, textarea, label, [role='menuitem'], [role='switch']"

/** What a press on a choice's surface does: nothing, open a new tab, or the choice's own act. */
export type ChoicePress = "ignore" | "tab" | "go"

/**
 * The one rule for a press on a surface that is a convenience for the pointer
 * — a `ChoiceRow`, a `ChoiceCard` that carries a sentence, an `OpenCard` —
 * written once because the copies drifted: the row navigated on a press that
 * ended a text selection and on Cmd-click, which the card already did not.
 *
 * A press is ignored when the choice is disabled, when it landed on a control
 * of its own inside the surface (a menu, a switch, a copy button), when React
 * carried it up from a portal that is not on the surface at all (a group label
 * in an open menu), and when it ends a text selection — selecting a path to
 * copy it is not asking to open it. A press with Cmd, Ctrl or Shift held opens
 * a destination in a new tab, as a link does. Pure, so the rule has a test.
 */
export function choicePress(
  event: {
    target: EventTarget | null
    currentTarget: EventTarget | null
    metaKey: boolean
    ctrlKey: boolean
    shiftKey: boolean
  },
  { href, disabled, selection }: { href?: string; disabled?: boolean; selection?: string },
): ChoicePress {
  if (disabled) return "ignore"
  const target = event.target as Element | null
  const surface = event.currentTarget as Element | null
  if (target && surface && target !== surface) {
    if (!surface.contains(target) || target.closest(CONTROL)) return "ignore"
  }
  if (selection) return "ignore"
  if (href && (event.metaKey || event.ctrlKey || event.shiftKey)) return "tab"
  return "go"
}

/**
 * The press of a choice's surface, and `go` for the control on its title — the
 * keyboard's way in, which takes the same act without the pointer's filters.
 *
 * The surface does not become a second tab stop or a second button: the title
 * is the control and carries the verb in its name (§12), so the surface takes
 * only `onClick`, behind the `jsx-a11y` comment every caller already writes.
 */
export function useChoicePress({
  href,
  onSelect,
  disabled,
}: {
  href?: string
  onSelect?: () => void
  disabled?: boolean
}) {
  const router = useRouter()
  const go = () => {
    if (disabled) return
    if (href) router.push(href)
    else onSelect?.()
  }
  const onClick = (event: React.MouseEvent<HTMLElement>) => {
    const press = choicePress(event, {
      href,
      disabled,
      selection: window.getSelection()?.toString(),
    })
    if (press === "tab" && href) window.open(href, "_blank")
    else if (press === "go") go()
  }
  return { onClick, go }
}

/**
 * A `ChoiceRow`'s box inside its edge: 56px at least, so with the 1px edge on
 * each side every row of a list is 58px whether it has a second line or not.
 * Shared with `ChoiceListSkeleton`, which is the reason it is a constant.
 */
export const CHOICE_ROW_BOX = "flex min-h-14 min-w-0 rounded-xl px-3 py-2.5"

/** The resting edge and ground of a card that does not answer the pointer. */
const CHOICE_ROW_EDGE = "relative isolate rounded-xl border bg-choice-surface"

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
  workspaceItem,
  leading,
  title,
  verb,
  description,
  columns,
  trailing,
  actions,
  onSelect,
  href,
  disabled,
  busy,
  index,
  children,
  className,
}: {
  workspaceItem?: { id: string; name: string }
  leading?: React.ReactNode
  title: React.ReactNode
  /** The accessible name of the title control — "Import Wayy01/api". */
  verb: string
  description?: React.ReactNode
  /**
   * The title and the description side by side rather than one over the other,
   * for a list read down its columns: every name starts on one edge and every
   * value on another, as long as the row's edge is the same width on each row.
   */
  columns?: boolean
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
   * The thing this row names is working right now — a run in flight, a test
   * delivery being sent, a backup being taken — so a light runs around its
   * edge (§11 *live*), and the eye finds it in a list without reading. It is
   * drawn over the edge rather than passed in as a child, because a child
   * would flip the row to its two-line layout.
   */
  busy?: boolean
  /**
   * Position in the list, for the arrival stagger `ChoiceCard` has (§11
   * *arrived*). Leave it out and the row simply appears, which is right for a
   * list that re-renders under a poll.
   */
  index?: number
  /**
   * What the row carries beneath its line at the full width of the card — a
   * container's live readings on a screen too narrow to hold them beside its
   * name. Presses inside it still reach the row, except on a control.
   */
  children?: React.ReactNode
  className?: string
}) {
  const press = useChoicePress({ href, onSelect, disabled })
  const line = (
    <>
      {leading && <span className="flex shrink-0 items-center">{leading}</span>}
      <span
        className={cn(
          "min-w-0 flex-1",
          columns && "grid grid-cols-[minmax(0,5fr)_minmax(0,6fr)] items-center gap-3",
        )}
      >
        {disabled ? (
          <span className="block max-w-full min-w-0 truncate text-body font-medium">{title}</span>
        ) : href ? (
          <Link
            data-workspace-item={workspaceItem?.id}
            data-workspace-name={workspaceItem?.name}
            href={href}
            aria-label={verb}
            onClick={(event) => event.stopPropagation()}
            className="block max-w-full min-w-0 truncate rounded-sm text-left text-body font-medium focus-ring"
          >
            {title}
          </Link>
        ) : (
          <button
            data-workspace-item={workspaceItem?.id}
            data-workspace-name={workspaceItem?.name}
            type="button"
            aria-label={verb}
            // The row's own handler already fires on the pointer; this one
            // is for the keyboard and must not run the action twice.
            onClick={(event) => {
              event.stopPropagation()
              press.go()
            }}
            className="block max-w-full min-w-0 truncate rounded-sm text-left text-body font-medium focus-ring"
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
          <span
            role="presentation"
            onClick={(event) => event.stopPropagation()}
            className="flex shrink-0 items-center"
          >
            {actions}
          </span>
        )}
      </span>
    </>
  )
  const body = (
    <>
      {busy && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={80} duration={4} />
        </span>
      )}
      {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the row's name is the link or button a keyboard opens it by */}
      <div
        // A control of its own inside the row — a copy button in the second
        // line, a switch beneath it — is not a press on the row.
        onClick={press.onClick}
        className={cn(
          // A fixed minimum, because a description is optional and a run of
          // cards where some are a line taller than others reads as a list
          // that failed to load rather than as a list of different things.
          // A row without one centres its title in the same box; nothing
          // reserves an empty second line, which would be a row claiming
          // content it has not got.
          // The unnamed group as well, so `DimActions` in the actions slot
          // brightens with the row the way it does in a table.
          "group group/choice text-left",
          CHOICE_ROW_BOX,
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
    </>
  )
  // The lit edge means "this is takeable" (§16), so a disabled row keeps its
  // card and its resting edge, and its edge does not answer the pointer.
  const card = disabled ? (
    <div className={CHOICE_ROW_EDGE}>{body}</div>
  ) : (
    <SpotlightBorder>{body}</SpotlightBorder>
  )
  return (
    <li data-slot="choice-row" className="min-w-0">
      {/* Capped, so the rows past the first screen arrive together rather
          than a list of forty taking over a second to finish landing. */}
      {index === undefined ? card : <BlurFade delay={Math.min(index, 11) * 0.03}>{card}</BlurFade>}
    </li>
  )
}

/**
 * What the next run of cards is, and how many of them.
 *
 * A hairline running off to the right rather than a panel header: these are
 * parts of one list, not blocks of their own, and a second framed title under
 * the page's would rank them as sections they are not. The Docker lists split
 * by what needs you first; the Git page shelves its checkouts by the account
 * their remote belongs to, with the account drawn as itself in `leading`.
 */
export function GroupRule({
  label,
  count,
  leading,
  detail,
}: {
  label: string
  count?: number
  /** The group drawn as itself, before its name: the account a shelf of checkouts belongs to. */
  leading?: React.ReactNode
  /** Where the group is, after the count — a host that is no product's. */
  detail?: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      {leading && <span className="flex shrink-0 items-center">{leading}</span>}
      <p className="eyebrow min-w-0 truncate">{label}</p>
      {/* Left out where the label ends in a figure of its own — a date — and
          the count would read as part of it. */}
      {count !== undefined && (
        <span className="numeric shrink-0 text-micro text-muted-foreground">{count}</span>
      )}
      {detail && (
        <span className="min-w-0 truncate font-mono text-micro text-muted-foreground">
          {detail}
        </span>
      )}
      <span aria-hidden className="h-px min-w-0 flex-1 bg-hairline" />
    </div>
  )
}

/** A run of `ChoiceRow`s. Gapped rather than hairlined: each row owns an edge. */
export function ChoiceList({ className, ...props }: React.ComponentProps<"ul">) {
  return <ul data-slot="choice-list" className={cn("min-w-0 space-y-2", className)} {...props} />
}

const GHOST_WIDTHS = ["w-32", "w-40", "w-28", "w-36"]

/**
 * Cards where a `ChoiceList` will be, each at its `ChoiceRow`'s own box, so the
 * list holds still when it lands (D11).
 *
 * Every page had imitated one: `LoadingRows` forced into a card's shape with a
 * class hack, grey slabs at 56, 58 and 62px, a mark and two lines in one place
 * and none in the next. This is drawn from the same box constant the row is,
 * with the mark slot at the size the row's leading mark takes (`sm` a
 * `ProductLogo size="sm"` or a `KindTile`, `md` a 40px tile, which makes the
 * real row 62px and so the ghost too), a bar where the name goes, a second
 * one under it when the rows carry a description, and a trailing bar where
 * the readings stand, which a phone does not draw. Every piece keeps
 * `data-slot="skeleton"`, the mark a spec waits on. No rows draws nothing:
 * the empty slab is the caller's to say.
 *
 * `band` is for rows that carry `children` on a phone only (the readings a
 * narrow screen moves under the name): below `sm` each ghost holds that many
 * lines of hint under its line, as the row does, so the list is as tall
 * waiting as landed. Pass `lines={2}` too when the rows have a description:
 * a row with one is a pixel taller than the 56px box.
 */
export function ChoiceListSkeleton({
  rows,
  mark = "sm",
  lines = 1,
  aside = true,
  band,
  className,
}: {
  rows: number
  mark?: "sm" | "md" | false
  lines?: 1 | 2
  aside?: boolean
  /** Lines of hint the rows carry under their line on a phone (`children` below `sm`). */
  band?: 1 | 2
  className?: string
}) {
  if (rows <= 0) return null
  return (
    <ul aria-hidden data-slot="choice-list-skeleton" className={cn("min-w-0 space-y-2", className)}>
      {Array.from({ length: rows }, (_, index) => {
        const line = (
          <>
            {mark && (
              <Skeleton
                className={cn("shrink-0 rounded-lg", mark === "md" ? "size-10" : "size-8")}
              />
            )}
            <span className="min-w-0 flex-1">
              <span className="block text-body">
                <Bone className={GHOST_WIDTHS[index % GHOST_WIDTHS.length]} />
              </span>
              {lines === 2 && (
                <span className="block text-hint">
                  <Bone className="w-48" />
                </span>
              )}
            </span>
            {aside && (
              <span className="shrink-0 text-body max-sm:hidden">
                <Bone className="w-24" />
              </span>
            )}
          </>
        )
        return (
          <li key={index} className={CHOICE_ROW_EDGE}>
            {band ? (
              // The row's own two layouts: its line with the band under it
              // on a phone, as `children` draws it, and the line alone above.
              <div
                className={cn(CHOICE_ROW_BOX, "items-center gap-3 max-sm:flex-col max-sm:gap-2.5")}
              >
                <div className="flex min-w-0 flex-1 items-center gap-3 max-sm:self-stretch">
                  {line}
                </div>
                <div className="flex min-w-0 flex-col gap-y-2 self-stretch sm:hidden">
                  {Array.from({ length: band }, (_, at) => (
                    <span key={at} className="block text-hint">
                      <Bone className={at === 0 ? "w-28" : "w-40"} />
                    </span>
                  ))}
                </div>
              </div>
            ) : (
              <div className={cn(CHOICE_ROW_BOX, "items-center gap-3")}>{line}</div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
