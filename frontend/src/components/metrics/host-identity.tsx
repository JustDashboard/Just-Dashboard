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
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-wrap items-center justify-between gap-x-10 gap-y-3 border-b border-hairline pb-6",
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-4">
        {logo ?? (
          <ProductLogo
            id={mark}
            fallback={fallback}
            className="size-12 rounded-xl [&_img]:size-7"
          />
        )}
        <div className="min-w-0 space-y-1">
          <p className="min-w-0 truncate text-title font-semibold tracking-tight">{title}</p>
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {facts}
          </p>
        </div>
      </div>
      {aside && <div className="shrink-0">{aside}</div>}
    </div>
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
