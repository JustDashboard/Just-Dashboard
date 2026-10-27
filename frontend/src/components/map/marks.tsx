"use client"

import { useState } from "react"
import {
  Archive,
  ArrowLeftRight,
  Bell,
  Box,
  ChartActivity,
  Clock,
  CloudUpload,
  Cpu,
  Database,
  DesktopDevice,
  FolderOpen,
  GitHubMark,
  Globe,
  GridMasonry,
  Key,
  Layers,
  LockClosed,
  NetworkDevice,
  PaperAirplane,
  Puzzle,
  Rocket,
  SecureConnection,
  Servers,
  Shield,
  ShieldCheck,
  Terminal,
  TerminalWindow,
  UserSettings,
  Users,
  type Icon,
} from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { ExcalidrawGlyph } from "@/components/boards/glyphs"
import { LogoGlyph } from "@/components/logo"
import {
  ProductGlyph,
  ProductLogo,
  channelProduct,
  frameworkProduct,
  hasProductLogo,
  imageProduct,
  issuerProduct,
  platformProduct,
  unitProduct,
} from "@/components/product-logo"
import type { DotTone } from "@/components/status-dot"
import { keyProduct } from "@/lib/clients"
import { kindOf, type BranchKey } from "@/lib/server-map"
import type { MapItem, MapTone, NotificationChannelKind } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The glyph each kind is found by: the sidebar's own, wherever the kind has a
 * row there, so the eye that finds "Docker" in the rail finds its shelves
 * here without reading (§14's wayfinding). A table rather than a choice made
 * in render, so a kind cannot be drawn two ways.
 */
export const KIND_GLYPH: Record<string, Icon> = {
  projects: Rocket,
  databases: Database,
  stacks: GridMasonry,
  containers: Box,
  volumes: Archive,
  networks: NetworkDevice,
  images: Layers,
  domains: Globe,
  certificates: LockClosed,
  streams: ArrowLeftRight,
  services: Servers,
  pm2: ChartActivity,
  dashboard: CloudUpload,
  packages: Puzzle,
  terminals: Terminal,
  drawings: ExcalidrawGlyph,
  repositories: GitHubMark,
  computers: DesktopDevice,
  files: FolderOpen,
  backups: Archive,
  scheduled: Clock,
  alerts: Bell,
  channels: PaperAirplane,
  accounts: Users,
  keys: Key,
  systemUsers: UserSettings,
  security: Shield,
  tunnels: SecureConnection,
}

export const BRANCH_GLYPH: Record<BranchKey, Icon> = {
  deployed: Rocket,
  web: Globe,
  host: Cpu,
  workspace: TerminalWindow,
  jobs: Clock,
  people: Users,
  protection: ShieldCheck,
}

/** A measured state as the dot draws it. Idle is a thing that is off, not a fault. */
export const TONE_DOT: Record<MapTone, DotTone> = {
  ok: "running",
  warning: "warning",
  danger: "danger",
  idle: "stopped",
  unknown: "unknown",
}

/** The branch a kind hangs from: what names its hue (`data-branch`, globals.css). */
export function branchOf(kind: string): BranchKey | undefined {
  return kindOf(kind)?.branch
}

/** A computer's system as Remote Desktop's own pages draw it: a Mac is Apple's. */
const SYSTEMS: Record<string, string> = { macos: "apple", windows: "windows", linux: "linux" }

/**
 * The firewall engines that are netfilter's own tools, drawn in its flame as
 * network drift draws the nftables files. ufw and firewalld drive netfilter
 * too but are projects of their own with no mark here, so they keep a glyph.
 */
const NETFILTER = new Set(["nftables", "iptables"])

/**
 * The server names a thing by what it is — a unit, a channel's kind, a
 * computer's system — and the product table is keyed by the logo; these are
 * the kinds whose name needs the same reading their own pages give it. Two
 * read more than the name the server sent: a certificate with no product is
 * read by its issuer, as the certificates page reads it, and an API key by
 * its name, as the keys page does (`github-actions` is GitHub's).
 */
const PRODUCT_OF: Record<string, (item: MapItem) => string | undefined> = {
  containers: (item) => item.product && imageProduct(item.product),
  images: (item) => item.product && imageProduct(item.product),
  stacks: (item) => item.product && imageProduct(item.product),
  projects: ({ product: raw }) =>
    raw && (frameworkProduct(raw) ?? (hasProductLogo(raw) ? raw : imageProduct(raw))),
  services: (item) => item.product && unitProduct(item.product),
  scheduled: (item) => item.product && unitProduct(item.product),
  channels: (item) => item.product && channelProduct(item.product as NotificationChannelKind),
  computers: ({ product: raw = "" }) =>
    platformProduct(raw) ?? (Object.hasOwn(SYSTEMS, raw) ? SYSTEMS[raw] : undefined),
  certificates: (item) => item.product ?? issuerProduct(item.detail),
  keys: (item) => keyProduct(item.name),
  security: (item) => (NETFILTER.has(item.product ?? "") ? "netfilter" : undefined),
}

export function itemProduct(item: MapItem, kind: string) {
  return PRODUCT_OF[kind]?.(item) ?? item.product
}

/**
 * The products a run of things is, most-named first and each once, with a
 * file to draw: what a branch, a folded shelf and the server name what they
 * hold by.
 */
export function strongestProducts(products: (string | undefined)[], max: number) {
  const counts = new Map<string, number>()
  for (const id of products) {
    if (id && hasProductLogo(id)) counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, max)
    .map(([id]) => id)
}

/** People, drawn as themselves: an account as the rail draws it, round; a system user square. */
const PEOPLE = new Set(["accounts", "systemUsers"])

const TILE = {
  sm: { box: "size-8", art: "size-4.5", glyph: "size-4" },
  md: { box: "size-10", art: "size-6", glyph: "size-5" },
}

/**
 * A thing drawn as itself: a project as the icon its website declares, a
 * container as the product its image is, a person as their picture or the
 * face the rail gives them, the dashboard as its own mark — and anything that
 * is no product as its kind's glyph in its branch's hue, so the tile still
 * says where on the map it hangs. Every one takes the same tile, so a shelf
 * of mixed marks still lines up.
 */
export function ItemMark({
  item,
  kind,
  size = "sm",
  className,
}: {
  item: MapItem
  kind: string
  /** The tile's size, `ProductLogo`'s: `sm` in a list, `md` on the map's boxes. */
  size?: "sm" | "md"
  className?: string
}) {
  const [failed, setFailed] = useState(false)
  const tile = TILE[size]
  if (PEOPLE.has(kind)) {
    return (
      <PersonMark item={item} round={kind === "accounts"} className={cn(tile.box, className)} />
    )
  }
  if (item.icon && !failed) {
    return (
      <span
        aria-hidden="true"
        className={cn(
          "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
          tile.box,
          className,
        )}
      >
        {/* Same-origin, served by the dashboard: a plain element, as ProjectMark's is. */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={item.icon}
          alt=""
          className={cn("object-contain", tile.art)}
          onError={() => setFailed(true)}
        />
      </span>
    )
  }
  if (kind === "dashboard") {
    return (
      <span
        aria-hidden="true"
        className={cn(
          "flex shrink-0 items-center justify-center rounded-lg border border-rule-brand bg-plot-brand text-brand",
          tile.box,
          className,
        )}
      >
        <LogoGlyph className={tile.glyph} />
      </span>
    )
  }
  const product = itemProduct(item, kind)
  if (hasProductLogo(product)) {
    return (
      <ProductLogo
        id={product}
        size={size}
        fallback={KIND_GLYPH[kind] ?? Box}
        className={className}
      />
    )
  }
  return <HueTile kind={kind} className={cn(tile.box, className)} glyph={tile.glyph} />
}

/**
 * A kind's glyph on a tile of its branch's hue: what a thing that is no
 * product is drawn as, and a shelf's or a tool's mark. It names its own
 * branch, so it takes the hue wherever it is drawn — on the canvas or in the
 * inspector.
 */
export function HueTile({
  kind,
  glyph = "size-5",
  className,
}: {
  kind: string
  /** The glyph's size inside the tile. */
  glyph?: string
  className?: string
}) {
  const Glyph = KIND_GLYPH[kind] ?? Box
  return (
    <span
      aria-hidden="true"
      data-branch={branchOf(kind)}
      className={cn(
        "flex size-10 shrink-0 items-center justify-center rounded-lg border border-(--hue-rule) bg-(--hue-plot) text-(--hue)",
        className,
      )}
    >
      <Glyph className={glyph} />
    </span>
  )
}

/**
 * The mark alone, with no tile: what a thing is drawn as far out, beside its
 * name held to the screen, where a tile's frame would be a speck. The caller
 * sizes it.
 */
export function ItemGlyph({
  item,
  kind,
  className,
}: {
  item: MapItem
  kind: string
  className?: string
}) {
  const [failed, setFailed] = useState(false)
  if (PEOPLE.has(kind)) {
    return <PersonMark item={item} round={kind === "accounts"} className={className} />
  }
  if (item.icon && !failed) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={item.icon}
        alt=""
        aria-hidden="true"
        className={cn("shrink-0 object-contain", className)}
        onError={() => setFailed(true)}
      />
    )
  }
  if (kind === "dashboard") return <LogoGlyph className={cn("shrink-0 text-brand", className)} />
  const product = itemProduct(item, kind)
  if (hasProductLogo(product)) return <ProductGlyph id={product} className={className} />
  const Glyph = KIND_GLYPH[kind] ?? Box
  return (
    <span
      aria-hidden="true"
      data-branch={branchOf(kind)}
      className={cn("flex text-(--hue)", className)}
    >
      <Glyph className="size-full" />
    </span>
  )
}

/**
 * A person: their picture where the server sends one (an account's, from the
 * users table's own route), otherwise the face `InitialsMark` gives the name —
 * the same hue the rail and the users list give it.
 */
function PersonMark({
  item,
  round,
  className,
}: {
  item: MapItem
  round: boolean
  className?: string
}) {
  const [failed, setFailed] = useState(false)
  if (item.icon && !failed) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={item.icon}
        alt=""
        aria-hidden="true"
        className={cn("shrink-0 object-cover", round ? "rounded-full" : "rounded-lg", className)}
        onError={() => setFailed(true)}
      />
    )
  }
  return (
    <InitialsMark
      name={item.name}
      size="md"
      slot={round ? "user-avatar" : undefined}
      className={cn(round ? "rounded-full" : "rounded-lg", className)}
    />
  )
}
