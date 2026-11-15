import { cn } from "@/lib/utils"
import type { HostInfo } from "@/lib/types"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Cpu, type Icon } from "@/components/icons"
import { Bone } from "@/components/bone"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
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
   * In a project's layout, the command row at the line's foot wherever the
   * line itself is narrower than 42rem, not only on a phone (where it always
   * is). For a line beside a rail, whose title is a name that must be read
   * whole: sharing a narrow row, the controls kept theirs and the name and
   * its facts were left a column a word wide.
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
            <Skeleton className="h-(--control-compact) w-28 rounded-md" />
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
  return <FactLine className="w-full">{facts}</FactLine>
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
 * On a phone the tile and the title have the first row to themselves, the
 * state takes the next, the facts run the full width under them, and the
 * command and its menu are the foot. Beside the title, "View deployment" cut
 * the project's address to eleven characters on every deploy page; the name
 * is the one part of the line that must never give way. The foot keeps a
 * control's height whenever the line has a place for verbs, so a loading
 * line that passes `menu={false}` or `command` is as tall as the line it
 * becomes (D11).
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
  // A line that has a place for verbs (even `menu={false}`, a place with
  // nothing in it yet) keeps a control's height for them at its foot on a
  // phone, so a line drawn before its verbs are known is as tall as the one
  // that replaces it.
  const footed = command !== undefined || menu !== undefined || aside !== undefined
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
      <div className="flex sm:row-span-2">{tile}</div>
      <div
        className={cn(
          "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:contents",
          foot && "@max-2xl:col-span-2",
        )}
      >
        {/* A flex box, so a title that is a link or an address sits on the
            row's centre rather than on the baseline of a taller line box. */}
        <p
          className={cn(
            "flex max-w-full min-w-0 text-base font-semibold tracking-tight",
            footed && "max-sm:col-span-2",
          )}
        >
          {title}
        </p>
        {state !== undefined && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:order-2 max-sm:col-span-3">
            {state}
          </div>
        )}
      </div>
      <div
        className={cn(
          "flex shrink-0 items-center gap-2 sm:row-span-2",
          footed
            ? "max-sm:order-last max-sm:col-span-3 max-sm:mt-1 max-sm:min-h-(--control-compact) max-sm:flex-wrap"
            : "max-sm:order-1",
          // Important, so the row of its own wins over the phone's place for
          // it whichever of the two rules the sheet happens to write last.
          foot &&
            "@max-2xl:order-last! @max-2xl:col-span-3 @max-2xl:row-span-1! @max-2xl:mt-1.5 @max-2xl:flex-wrap",
        )}
      >
        {aside}
        {command}
        {menu}
      </div>
      <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground max-sm:order-3 sm:col-span-1 sm:col-start-2">
        {facts}
      </div>
    </Line>
  )
  // The line measures itself, not the window (§12): a rail beside it takes a
  // column of its own, so a laptop's worth of window is a tablet's of line.
  return foot ? <div className="@container min-w-0">{line}</div> : line
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
 */
export function IdentityVerbs({ verbs, name }: { verbs: Verb[]; name: string }): {
  command?: React.ReactNode
  menu?: React.ReactNode
} {
  const lead = verbs.find((verb) => verb.inline) ?? verbs[0]
  const rest = verbs.filter((verb) => verb !== lead)
  return {
    command: lead && (
      <Button
        size="sm"
        variant={lead.danger ? "destructive" : "default"}
        disabled={lead.disabled}
        onClick={lead.run}
      >
        <lead.icon className="size-3.5" />
        {lead.label}
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
