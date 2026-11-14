import { cn } from "@/lib/utils"
import type { HostInfo } from "@/lib/types"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Cpu, type Icon } from "@/components/icons"

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

/**
 * What the machine is, as one line under the page's title: a mark on a tile,
 * a line naming it, and a line of facts after it — the shape the dashboard's
 * own Version page gives the install, so the three pages that describe a thing
 * describe it the same way.
 *
 * The mark is the thing itself (§14): the distribution on the Overview, the
 * processor on Metrics. Inside the facts the hypervisor and the processor are
 * drawn bare at the line's height. A host this cannot name keeps a glyph on
 * the tile, never a guessed logo.
 *
 * An account's profile opens on the same line, with its own picture where the
 * tile would be: the fourth page that describes a thing describes it the same
 * way too.
 */
export function HostIdentity({
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
  label,
  slot = "host-identity",
  className,
}: {
  /** A `product-logo` id; nothing names the host when it is undefined. */
  mark?: string
  /** A picture that is no product — an account's own — in place of the tile. */
  logo?: React.ReactNode
  fallback?: Icon
  title: React.ReactNode
  facts: React.ReactNode
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
   */
  state?: React.ReactNode
  /** The one brand-faced command at the far end, and anything that sits before it. */
  command?: React.ReactNode
  /** The `⋯` menu after the command, holding the rest of the verbs. */
  menu?: React.ReactNode
  /** The line's landmark name ("About api"): set, the line is a `section`. */
  label?: string
  /** The `data-slot`, for a page whose specs know its header by another name. */
  slot?: string
  className?: string
}) {
  if (state !== undefined || command !== undefined || menu !== undefined)
    return (
      <ProjectLine
        tile={
          logo ?? (
            <ProductLogo
              id={mark}
              fallback={fallback}
              className="size-10 rounded-lg [&_img]:size-6"
            />
          )
        }
        title={title}
        facts={facts}
        state={state}
        command={command}
        menu={menu}
        aside={aside}
        label={label}
        slot={slot}
        className={className}
      />
    )
  return (
    <div
      data-slot={slot}
      data-bar={bar || undefined}
      aria-label={label}
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
        {logo ?? (
          <ProductLogo
            id={mark}
            fallback={fallback}
            className={
              bar ? "size-10 rounded-lg [&_img]:size-6" : "size-12 rounded-xl [&_img]:size-7"
            }
          />
        )}
        <div
          className={cn("contents sm:block sm:min-w-0", bar ? "sm:space-y-0.5" : "sm:space-y-1")}
        >
          <p className="min-w-0 truncate text-title font-semibold tracking-tight">{title}</p>
          <p className="col-span-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {facts}
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
 * The identity line laid out as a project's header (`deploy/project-shell`),
 * which the operator named the opening every page should reach: the tile
 * beside two rows, the title with its state after it on the first and the
 * facts on the second, the command and its `⋯` menu at the far end across
 * both. On a phone the tile, the title and the command share the first row,
 * the state takes the next, and the facts run the full width under them.
 */
function ProjectLine({
  tile,
  title,
  facts,
  state,
  command,
  menu,
  aside,
  label,
  slot,
  className,
}: {
  tile: React.ReactNode
  title: React.ReactNode
  facts: React.ReactNode
  state?: React.ReactNode
  command?: React.ReactNode
  menu?: React.ReactNode
  aside?: React.ReactNode
  label?: string
  slot: string
  className?: string
}) {
  const Line = label ? "section" : "div"
  return (
    <Line
      aria-label={label}
      data-slot={slot}
      data-layout="project"
      className={cn(
        "grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-hairline pb-4 sm:gap-y-0.5",
        className,
      )}
    >
      <div className="flex sm:row-span-2">{tile}</div>
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:contents">
        {/* A flex box, so a title that is a link or an address sits on the
            row's centre rather than on the baseline of a taller line box. */}
        <p className="flex max-w-full min-w-0 text-title font-semibold tracking-tight">{title}</p>
        {state !== undefined && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:order-2 max-sm:col-span-3">
            {state}
          </div>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2 max-sm:order-1 sm:row-span-2">
        {aside}
        {command}
        {menu}
      </div>
      <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground max-sm:order-3 sm:col-span-1 sm:col-start-2">
        {facts}
      </div>
    </Line>
  )
}

/** A fact in the identity line, with the product it names drawn before it. */
export function HostFact({ product, children }: { product?: string; children: React.ReactNode }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      {product && <ProductGlyph id={product} />}
      <span className="truncate">{children}</span>
    </span>
  )
}

export function FactDot() {
  return <span className="text-muted-foreground/40">·</span>
}
