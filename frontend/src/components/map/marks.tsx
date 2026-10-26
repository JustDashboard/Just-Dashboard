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
import { ExcalidrawGlyph } from "@/components/boards/glyphs"
import {
  ProductLogo,
  channelProduct,
  frameworkProduct,
  hasProductLogo,
  imageProduct,
  platformProduct,
  unitProduct,
} from "@/components/product-logo"
import type { DotTone } from "@/components/status-dot"
import type { BranchKey } from "@/lib/server-map"
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

/**
 * The server names a thing by what it is — a unit, a channel's kind, a
 * computer's system — and the product table is keyed by the logo; these are
 * the kinds whose name needs the same reading their own pages give it.
 */
const PRODUCT_OF: Record<string, (raw: string) => string | undefined> = {
  containers: imageProduct,
  images: imageProduct,
  stacks: imageProduct,
  projects: (raw) => frameworkProduct(raw) ?? (hasProductLogo(raw) ? raw : imageProduct(raw)),
  services: unitProduct,
  scheduled: unitProduct,
  channels: (raw) => channelProduct(raw as NotificationChannelKind),
  computers: platformProduct,
}

export function itemProduct(item: MapItem, kind: string) {
  if (!item.product) return undefined
  return PRODUCT_OF[kind]?.(item.product) ?? item.product
}

/**
 * A thing drawn as itself: a project as the icon its website declares, a
 * container as the product its image is, anything else as its kind's glyph
 * on the same tile, so a shelf of mixed marks still lines up.
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
  if (item.icon && !failed) {
    return (
      <span
        aria-hidden="true"
        className={cn(
          "flex shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
          size === "sm" ? "size-8" : "size-10",
          className,
        )}
      >
        {/* Same-origin, served by the dashboard: a plain element, as ProjectMark's is. */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={item.icon}
          alt=""
          className={cn("object-contain", size === "sm" ? "size-4.5" : "size-6")}
          onError={() => setFailed(true)}
        />
      </span>
    )
  }
  return (
    <ProductLogo
      id={itemProduct(item, kind)}
      size={size}
      fallback={KIND_GLYPH[kind] ?? Box}
      className={className}
    />
  )
}
