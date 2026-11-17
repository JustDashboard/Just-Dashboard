import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { breakpointQuery } from "@/lib/breakpoints"
import { useIsPhone } from "@/hooks/use-mobile"
import type { HostInfo } from "@/lib/types"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Cpu, type Icon } from "@/components/icons"
import { Bone } from "@/components/bone"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { VerbMenu, type Verb } from "@/components/verbs"
import { FactDot, FactLine, hasFacts } from "@/components/fact-line"

export { FactDot }

const PLATFORM_NAME: Record<string, string> = {
  ubuntu: "Ubuntu",
  debian: "Debian",
  raspbian: "Raspberry Pi OS",
  fedora: "Fedora",
  arch: "Arch Linux",
  archarm: "Arch Linux ARM",
  alpine: "Alpine",
  centos: "CentOS",
  rocky: "Rocky Linux",
  almalinux: "AlmaLinux",
  linuxmint: "Linux Mint",
  opensuse: "openSUSE",
  "opensuse-leap": "openSUSE Leap",
  "opensuse-tumbleweed": "openSUSE Tumbleweed",
}

/** The distribution as its own name spells it: "Ubuntu 24.04", not "ubuntu 24.04". */
export function platformName(host: HostInfo) {
  const id = host.platform.toLowerCase()
  const name = PLATFORM_NAME[id] ?? (id ? id[0].toUpperCase() + id.slice(1) : host.os)
  return [name, host.platformVersion].filter(Boolean).join(" ")
}

type IdentityHeaderProps = {
  /** A `product-logo` id; nothing names the host when it is undefined. */
  mark?: string
  /** A picture that is no product — an account's own — in place of the tile. */
  logo?: React.ReactNode
  fallback?: Icon
  /** What the thing is called. Left out only while `loading`, where a bone holds its place. */
  title?: React.ReactNode
  /**
   * The facts under the title. Written as `Fact`s (or, until the page passes
   * move them, with `FactDot`s between them) they are drawn as a `FactLine`,
   * so no dot is left at the end of a wrapped line. Left out only while
   * `loading`.
   */
  facts?: React.ReactNode
  /** The right end of the line: the health verdict. */
  aside?: React.ReactNode
  /**
   * One compact bar holding the page's own controls as well as the line: the
   * proxy overview opened on a row of three buttons over the line, a band of
   * empty ground between them, and the operator asked for one bar.
   */
  bar?: boolean
  /**
   * The thing's state, beside its title: a `Status` dot and its word. With
   * `state`, `command` or `menu` the line is laid out as a project's header.
   * While `loading`, `true` holds the state's place with a bone.
   */
  state?: React.ReactNode
  /**
   * The one brand-faced command at the far end, and anything that sits before
   * it (`IdentityVerbs` gives it). While `loading`, `true` holds its place.
   */
  command?: React.ReactNode
  /** The `⋯` menu after the command, holding the rest of the verbs. */
  menu?: React.ReactNode
  /**
   * In a project's layout, the command on a row of its own at the line's
   * foot wherever the line itself is narrower than 42rem, not only on a
   * phone (where it always has a row of its own). For a line beside a rail,
   * whose title is a name that must be read whole: sharing a narrow row, the
   * controls kept theirs and the name and its facts were left a column a
   * word wide.
   */
  foot?: boolean
  /** The line's landmark name ("About api"): set, the line is a `section`. */
  label?: string
  /** The `data-slot`, for a page whose specs know its header by another name. */
  slot?: string
  /**
   * The line before its thing has been read: every part not given is a bone
   * in the box the words will take, so the page has its final shape while it
   * loads and nothing under the line moves when they land (D11). A title or a
   * mark that is already known is drawn as itself.
   */
  loading?: boolean
  className?: string
}

/**
 * What a thing is, as the line a page opens on (D1): a mark on a tile, a line
 * naming it, and a line of facts after it — the shape the dashboard's own
 * Version page gives the install, so every page that describes a thing
 * describes it the same way.
 *
 * The mark is the thing itself (§14): the distribution on the Overview, the
 * processor on Metrics. Inside the facts the hypervisor and the processor are
 * drawn bare at the line's height. A host this cannot name keeps a glyph on
 * the tile, never a guessed logo, and the glyph is drawn at the size a logo
 * would be, so sibling headers carry the same weight.
 *
 * An account's profile opens on the same line, with its own picture where the
 * tile would be.
 *
 * With a `state`, a `command` or a `menu`, the line is a project's header
 * (`ProjectLine` below). Its verbs come from `IdentityVerbs`: one brand-faced
 * command and the rest behind `⋯`, never a row of equal buttons over the name.
 */
export function IdentityHeader({
  mark,
  logo,
  fallback = Cpu,
  title,
  facts,
  aside,
  bar,
  state,
  command,
  menu,
  foot,
  label,
  slot = "host-identity",
  loading,
  className,
}: IdentityHeaderProps) {
  const lineFacts = loading && facts === undefined ? <FactsBone /> : factRun(facts)
  const lineTitle = loading && title === undefined ? <Bone className="w-28" /> : title
  if (state !== undefined || command !== undefined || menu !== undefined)
    return (
      <ProjectLine
        tile={
          logo ??
          (loading && mark === undefined ? (
            <Skeleton className="size-10 shrink-0 rounded-lg" />
          ) : (
            <ProductLogo
              id={mark}
              fallback={fallback}
              className="size-10 rounded-lg [&_img]:size-6 [&>svg]:size-5"
            />
          ))
        }
        title={lineTitle}
        facts={lineFacts}
        state={loading && state === true ? <Bone className="w-24 text-xs" /> : state}
        command={
          loading && command === true ? (
            <Skeleton className="h-(--control-compact) w-28 rounded-md max-sm:size-8" />
          ) : (
            command
          )
        }
        menu={menu}
        aside={aside}
        foot={foot}
        label={label}
        slot={slot}
        loading={loading}
        className={className}
      />
    )
  return (
    <div
      data-slot={slot}
      data-bar={bar || undefined}
      aria-label={label}
      aria-busy={loading || undefined}
      role={label ? "region" : undefined}
      className={cn(
        "flex min-w-0 flex-wrap items-center justify-between",
        bar
          ? "gap-x-6 gap-y-3 rounded-xl border bg-card px-4 py-3"
          : "gap-x-10 gap-y-3 border-b border-hairline pb-6",
        className,
      )}
    >
      {/* On a phone the tile and the title share a row and the facts run the
          full width under both, rather than down a column beside the tile;
          from `sm` the text stands beside the tile as one block that takes
          the width the aside leaves — measured at its content, a long line
          of facts pushed the aside under the line instead of wrapping the
          facts, and a run's verbs at the line's end were the first aside
          wide enough to make that happen on a laptop. It keeps 18rem of its
          own, though: with none, a wide aside on a tablet left the facts a
          word wide, one fact to a line, where the aside should go under. */}
      <div
        className={cn(
          "grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-y-3 sm:flex sm:flex-[1_1_18rem]",
          bar ? "gap-x-3" : "gap-x-4",
        )}
      >
        {logo ??
          (loading && mark === undefined ? (
            <Skeleton
              className={cn("shrink-0", bar ? "size-10 rounded-lg" : "size-12 rounded-xl")}
            />
          ) : (
            <ProductLogo
              id={mark}
              fallback={fallback}
              className={
                bar
                  ? "size-10 rounded-lg [&_img]:size-6 [&>svg]:size-5"
                  : "size-12 rounded-xl [&_img]:size-7 [&>svg]:size-6"
              }
            />
          ))}
        <div
          className={cn("contents sm:block sm:min-w-0", bar ? "sm:space-y-0.5" : "sm:space-y-1")}
        >
          <p className="min-w-0 truncate text-base font-semibold tracking-tight">{lineTitle}</p>
          <p className="col-span-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {lineFacts}
          </p>
        </div>
      </div>
      {/* A flex box, so a bare status set here sits on the line's centre
          rather than on the baseline of a 16px line box it never asked for;
          never wider than the line, so a verdict with commands beside it
          wraps them on a phone rather than pushing the last one off it. */}
      {aside && <div className="flex max-w-full shrink-0">{aside}</div>}
    </div>
  )
}

/**
 * @deprecated use `IdentityHeader`; the old name stays until the page passes
 * move its importers and WP-39 bans it.
 */
export const HostIdentity = IdentityHeader

/** Facts written as facts go through a `FactLine`; anything else is drawn as it was given. */
function factRun(facts: React.ReactNode) {
  if (!hasFacts(facts)) return facts
  // The rows a wrap makes are the line's own 4px apart, the gap its bones
  // wait at, so a line whose facts wrap is as tall loaded as loading. On a
  // phone it takes what the line leaves, so a state leading it keeps its place.
  return (
    <FactLine className="w-full max-sm:w-auto max-sm:flex-1 [&>span]:gap-y-1">{facts}</FactLine>
  )
}

/** The facts' place while they are read: one bone in the line's own height. */
function FactsBone() {
  return <Bone className="w-64 max-w-full" />
}

/**
 * The identity line laid out as a project's header (`deploy/project-shell`),
 * which the operator named the opening every page should reach: the tile
 * beside two rows, the title with its state after it on the first and the
 * facts on the second, the command and its `⋯` menu at the far end across
 * both.
 *
 * On a phone it is one row and one line of facts, and nothing else. The row
 * is the tile a step smaller (32px), the name, and the verbs at the far end
 * as icon buttons: the command drawn as its icon alone, its words kept as its
 * name and said in a tooltip, then `⋯`. Every verb keeps its 44px press box
 * (`hit-area`) without growing the row. The state stands after the name when
 * both fit; when they do not, it leads the facts line, so the name is the
 * last thing to give way. The facts are one line that never wraps: what does
 * not fit is left off whole, at a fact's edge, never cut inside a word.
 *
 * The row has a control's height and the facts a line's whatever is in
 * them, so a loading line is as tall as the line it becomes, and props that
 * once reserved a row for the verbs (`menu={false}`, `command` while
 * `loading`, `foot`) leave the height alone (D11).
 */
function ProjectLine({
  tile,
  title,
  facts,
  state,
  command,
  menu,
  aside,
  foot,
  label,
  slot,
  loading,
  className,
}: {
  tile: React.ReactNode
  title: React.ReactNode
  facts: React.ReactNode
  state?: React.ReactNode
  command?: React.ReactNode
  menu?: React.ReactNode
  aside?: React.ReactNode
  foot?: boolean
  label?: string
  slot: string
  loading?: boolean
  className?: string
}) {
  const Line = label ? "section" : "div"
  const nameRef = useRef<HTMLDivElement>(null)
  const titleRef = useRef<HTMLParagraphElement>(null)
  const stateRef = useRef<HTMLDivElement>(null)
  const stateLeadsFacts = useStateLeadsFacts(nameRef, titleRef, stateRef)
  const stateBox = state !== undefined && (
    <div
      ref={stateRef}
      data-slot="identity-state"
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:shrink-0 max-sm:flex-nowrap max-sm:empty:hidden",
        // In the facts' muted line, the state keeps the ink it has beside the name.
        stateLeadsFacts && "text-foreground",
      )}
    >
      {state}
    </div>
  )
  const line = (
    <Line
      aria-label={label}
      aria-busy={loading || undefined}
      data-slot={slot}
      data-layout="project"
      className={cn(
        "grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-hairline pb-4 sm:gap-y-0.5",
        className,
      )}
    >
      {/* A step smaller on a phone, whatever picture the tile holds. */}
      <div className="flex max-sm:[zoom:0.8] sm:row-span-2">{tile}</div>
      <div
        ref={nameRef}
        className={cn(
          "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:flex-nowrap",
          foot && "sm:@max-2xl:col-span-2",
        )}
      >
        {/* A flex box, so a title that is a link or an address sits on the
            row's centre rather than on the baseline of a taller line box; on
            a phone a block that truncates, so a name too long for the row
            ends in an ellipsis rather than running under the verbs. */}
        <p
          ref={titleRef}
          className="flex max-w-full min-w-0 text-base font-semibold tracking-tight max-sm:block max-sm:truncate"
        >
          {title}
        </p>
        {!stateLeadsFacts && stateBox}
      </div>
      <div
        className={cn(
          // On a phone a verb's 32px even with no verb in it, so the row is a
          // control's height whatever the line holds: a loading line has no
          // verbs yet, and a tile smaller than 32px (an account's mark) left
          // its row 3px short of the loaded one's.
          "flex shrink-0 items-center gap-2 max-sm:min-h-8 sm:row-span-2",
          PHONE_VERBS,
          // Important, so the row of its own wins over the line's place for it
          // whichever of the two rules the sheet happens to write last. From
          // `sm` only: on a phone the verbs keep the name's row.
          foot &&
            "sm:@max-2xl:order-last! sm:@max-2xl:col-span-3 sm:@max-2xl:row-span-1! sm:@max-2xl:mt-1.5 sm:@max-2xl:flex-wrap",
        )}
      >
        {aside}
        {command ? <CommandTip>{command}</CommandTip> : command}
        {menu}
      </div>
      {/* On a phone one line that never grows: what wraps falls past its
          foot and is clipped, a whole fact at a time. A `FactLine` is held to
          the line too, so its own wrapped rows never stand it taller than the
          state beside it and pull the state off the line's centre. */}
      <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground max-sm:h-4 max-sm:overflow-clip max-sm:[overflow-clip-margin:2px] sm:col-span-1 sm:col-start-2 max-sm:[&>[data-slot=fact-line]]:h-4">
        {stateLeadsFacts && stateBox}
        {facts}
      </div>
    </Line>
  )
  // The line measures itself, not the window (§12): a rail beside it takes a
  // column of its own, so a laptop's worth of window is a tablet's of line.
  return foot ? <div className="@container min-w-0">{line}</div> : line
}

/**
 * The verbs on a phone: every button a 32px square on the name's row, its
 * press box still 44px under a finger (`hit-area`, 12px apart so no two
 * boxes overlap), and a button with an icon drawn as the icon alone. Its
 * words stay in the button at no size (`font-size: 0`, which is no step of
 * the type scale but the words taking no room), so they are still its
 * accessible name; `CommandTip` says them on hover and focus.
 */
const PHONE_VERBS = cn(
  "max-sm:gap-3",
  // `data-size` is every `Button`'s, the menu's trigger among them, whose
  // `data-slot` is the dropdown's.
  "max-sm:[&_[data-size]]:hit-area max-sm:[&_[data-size]]:[--control-h:2rem]",
  "max-sm:[&_[data-size]:has(svg)]:w-(--control-h) max-sm:[&_[data-size]:has(svg)]:gap-0 max-sm:[&_[data-size]:has(svg)]:px-0 max-sm:[&_[data-size]:has(svg)]:[font-size:0]",
)

/**
 * Whether, on a phone, the state leads the facts line rather than following
 * the name: when the name at its full width, the gap and the state together
 * are wider than the name's cell. Measured before paint, on every render and
 * whenever the cell or the state changes size, so the name is only ever cut
 * when it is too long for the row on its own. Off a phone it is always
 * beside the name.
 */
function useStateLeadsFacts(
  nameRef: React.RefObject<HTMLDivElement | null>,
  titleRef: React.RefObject<HTMLParagraphElement | null>,
  stateRef: React.RefObject<HTMLDivElement | null>,
) {
  const phone = useIsPhone()
  const [leads, setLeads] = useState(false)
  const measure = useCallback(() => {
    const cell = nameRef.current
    const words = titleRef.current
    const mark = stateRef.current
    if (!phone || !cell || !words || !mark) return setLeads(false)
    const room = cell.clientWidth
    const stateWidth = mark.getBoundingClientRect().width
    const need = naturalWidth(words) + (stateWidth > 0 ? STATE_GAP : 0) + stateWidth
    setLeads(need > room + 0.5)
  }, [phone, nameRef, titleRef, stateRef])
  // Every render: a new title or state changes what is needed without
  // changing a size the observer sees.
  useLayoutEffect(measure)
  useEffect(() => {
    if (!phone) return
    const observer = new ResizeObserver(() => measure())
    if (nameRef.current) observer.observe(nameRef.current)
    if (stateRef.current) observer.observe(stateRef.current)
    // A web font landing changes the name's width and nothing else.
    document.fonts?.ready.then(() => measure())
    return () => observer.disconnect()
  }, [phone, measure, nameRef, stateRef])
  return leads
}

/** `gap-x-3` between the name and its state. */
const STATE_GAP = 12

/** An element's width at its content's full length, however its box is held now. */
function naturalWidth(element: HTMLElement) {
  const { flex, width, maxWidth } = element.style
  element.style.flex = "none"
  element.style.width = "max-content"
  element.style.maxWidth = "none"
  const natural = element.getBoundingClientRect().width
  element.style.flex = flex
  element.style.width = width
  element.style.maxWidth = maxWidth
  return natural
}

/**
 * The command's words in a tooltip, on a phone where the command is drawn as
 * its icon alone; from `sm`, where the words are on the button, it never
 * opens. Its own box is `contents` from `sm`, so the line is laid out there
 * exactly as it was without it.
 *
 * The tooltip stands beside the command, never around it. A command is often
 * a popover (Connect), and React carries an event from a portal's content up
 * the tree it was rendered in, not the page's: wrapped in a `TooltipTrigger`
 * and its provider, every pointer move and press inside the open popover
 * reached this tip's trigger as if it were on the command, which put the
 * provider in its skip-delay state, so a tooltip inside the popover ("Show
 * the password") opened at once on a press and, as the topmost layer, took
 * the Escape meant for the popover, which stayed open. So the tip listens on
 * the command's own box in the page, where nothing in a portal reaches, and
 * its trigger is a box with nothing in it laid over the command, an anchor
 * that takes no pointer and no focus.
 */
function CommandTip({ children }: { children: React.ReactNode }) {
  const ref = useRef<HTMLSpanElement>(null)
  const [words, setWords] = useState<string | null>(null)
  useEffect(() => {
    const box = ref.current
    if (!box) return
    const hide = () => setWords(null)
    // A mouse resting on it, or a keyboard landing on it; a finger never
    // opens a tooltip, since the tap that would is a press.
    const enter = (event: PointerEvent) => {
      if (event.pointerType !== "touch") setWords(iconWords(box))
    }
    const focus = (event: FocusEvent) => {
      if (event.target instanceof Element && event.target.matches(":focus-visible"))
        setWords(iconWords(box))
    }
    const listeners = [
      ["pointerenter", enter],
      ["pointerleave", hide],
      ["pointerdown", hide],
      ["focusin", focus],
      ["focusout", hide],
    ] as const
    for (const [type, listener] of listeners) box.addEventListener(type, listener as EventListener)
    return () => {
      for (const [type, listener] of listeners)
        box.removeEventListener(type, listener as EventListener)
    }
  }, [])
  return (
    <span ref={ref} className="relative flex shrink-0 items-center gap-3 sm:contents">
      {children}
      <TooltipProvider>
        <Tooltip open={words !== null} onOpenChange={(open) => !open && setWords(null)}>
          <TooltipTrigger asChild>
            <span aria-hidden className="pointer-events-none absolute inset-0 sm:hidden" />
          </TooltipTrigger>
          {words !== null && <TooltipContent>{words}</TooltipContent>}
        </Tooltip>
      </TooltipProvider>
    </span>
  )
}

/**
 * What the command's icon-only buttons say, or nothing when none is drawn as
 * an icon alone: each one's accessible name, its `aria-label` where it has one
 * (a command drawn with its `short` word), else its words.
 */
function iconWords(box: HTMLElement | null) {
  if (!box || !window.matchMedia(breakpointQuery("sm", "below")).matches) return null
  const words = Array.from(box.querySelectorAll<HTMLElement>("[data-size]"))
    .filter((button) => button.getClientRects().length > 0 && button.querySelector("svg"))
    .map((button) => buttonName(button))
    .filter(Boolean)
  return words.length > 0 ? words.join(" · ") : null
}

/** A button's name as a screen reader hears it, for the tip that says it aloud on a phone. */
function buttonName(button: Pick<Element, "getAttribute" | "textContent">) {
  return (button.getAttribute("aria-label") ?? button.textContent ?? "").trim()
}

/**
 * A thing's verbs for its identity line: the first inline verb as the one
 * brand-faced command, and every other verb behind `⋯`, named "More actions
 * for <name>". Spread the result into `IdentityHeader`:
 *
 *     <IdentityHeader {...IdentityVerbs({ verbs, name: site.name })} … />
 *
 * One rule for every detail page, where there were four (the command on the
 * line, verbs on the back-link row, a local `HeaderVerbs`, a `VerbBar`), and
 * on a phone it is already the fold MOB-12 asks for: one button and a menu.
 *
 * The command says the verb's `short` word where it has one ("Logs" for "Open
 * logs"), as `VerbBar` does, and keeps `label` as its accessible name. On a
 * phone, where it is drawn as its icon alone, the tip says that name, so the
 * icon is never explained in fewer words than a screen reader hears. The
 * menu always says `label`.
 *
 * No destructive verb takes the command (the operator's rule of 16 Nov):
 * Terminate, Kill, Delete and the like stay behind `⋯` in the destructive
 * ink, and a thing whose only verbs are destructive draws no command.
 */
export function IdentityVerbs({ verbs, name }: { verbs: Verb[]; name: string }): {
  command?: React.ReactNode
  menu?: React.ReactNode
} {
  const safe = verbs.filter((verb) => !verb.danger)
  const lead = safe.find((verb) => verb.inline) ?? safe[0]
  const rest = verbs.filter((verb) => verb !== lead)
  return {
    command: lead && (
      <Button
        size="sm"
        disabled={lead.disabled}
        aria-label={lead.short ? lead.label : undefined}
        onClick={lead.run}
      >
        <lead.icon className="size-3.5" />
        {lead.short ?? lead.label}
      </Button>
    ),
    menu: rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${name}`} />,
  }
}

/**
 * A fact in the identity line, with the product it names drawn before it.
 *
 * @deprecated use `Fact` with `product`, inside a `FactLine`.
 */
export function HostFact({ product, children }: { product?: string; children: React.ReactNode }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      {product && <ProductGlyph id={product} />}
      <span className="truncate">{children}</span>
    </span>
  )
}
