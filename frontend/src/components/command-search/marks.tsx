"use client"

import type { CSSProperties } from "react"
import {
  Archive,
  Box,
  ChartActivity,
  Clock,
  Database,
  GitBranch,
  Globe,
  Layers,
  Lightning,
  MagnifyingGlass,
  Pencil,
  Question,
  RefreshClockwise,
  Rocket,
  Servers,
  type Icon,
} from "@/components/icons"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"
import type { SearchKind, SearchScope } from "./model"

/**
 * What each kind of result is drawn as when it has no product of its own, and
 * the hue that says which kind it is.
 *
 * The hues are the `--tag-*` lanes the log console and the build transcript
 * give a name — fixed, one lightness, never red or amber, so no kind of result
 * reads as a failure. A kind that has a product of its own (every container is
 * Docker's, every repository Git's) names it, and its scope chip carries that
 * mark rather than a glyph.
 */
export const KINDS: Record<SearchKind, { icon: Icon; hue: string; product?: string }> = {
  recent: { icon: Clock, hue: "var(--tag-slate)" },
  command: { icon: Lightning, hue: "var(--signal)" },
  page: { icon: Layers, hue: "var(--brand)" },
  project: { icon: Rocket, hue: "var(--tag-violet)" },
  site: { icon: Globe, hue: "var(--tag-cyan)" },
  database: { icon: Database, hue: "var(--tag-green)" },
  container: { icon: Box, hue: "var(--tag-blue)", product: "docker" },
  stack: { icon: Layers, hue: "var(--tag-blue)", product: "docker-compose" },
  repo: { icon: GitBranch, hue: "var(--tag-pink)", product: "git" },
  service: { icon: Servers, hue: "var(--tag-cyan)" },
  app: { icon: ChartActivity, hue: "var(--tag-green)", product: "pm2" },
  backup: { icon: Archive, hue: "var(--tag-slate)" },
  board: { icon: Pencil, hue: "var(--tag-pink)" },
}

/**
 * A page takes the hue of the rail group it sits in, so the fifty of them read
 * as six neighbourhoods rather than one grey column.
 */
export const GROUP_HUES: Record<string, string> = {
  Server: "var(--tag-blue)",
  Apps: "var(--tag-violet)",
  Workspace: "var(--tag-cyan)",
  Protection: "var(--tag-green)",
  Advanced: "var(--tag-pink)",
  System: "var(--tag-slate)",
  Account: "var(--brand)",
}

/** The pages that are one product's own, drawn as that product. */
export const PAGE_PRODUCTS: Record<string, string> = {
  "/docker": "docker",
  "/docker/stacks": "docker-compose",
  "/git": "git",
  "/processes/pm2": "pm2",
  "/terminal": "terminal",
}

/** The page's own commands, by the ids `Workspace` registers them under. */
export const COMMAND_ICONS: Record<string, Icon> = {
  find: MagnifyingGlass,
  refresh: RefreshClockwise,
  help: Question,
}

const hued = (hue: string) => ({ "--hue": hue }) as CSSProperties

/**
 * A result's mark: the product it is on ProductLogo's tile, or its glyph on a
 * tile of its kind's hue. Every row has one at one size, so the titles start
 * on one line whichever kind each row is.
 */
export function ResultMark({
  product,
  icon: Glyph,
  hue,
}: {
  product?: string
  icon: React.ComponentType<{ className?: string }>
  hue: string
}) {
  if (product) return <ProductLogo id={product} size="sm" className="bg-card" />
  return (
    <span aria-hidden="true" style={hued(hue)} className={cn("size-8 rounded-lg", HUE_TILE)}>
      <Glyph className="size-4" />
    </span>
  )
}

/** A scope's mark in its chip: the kind's product bare, or its glyph in its hue. */
export function ScopeMark({ scope }: { scope: SearchScope }) {
  if (scope === "all") return <MagnifyingGlass className="size-3.5" />
  const { product, icon: Glyph, hue } = KINDS[scope]
  if (product) return <ProductGlyph id={product} />
  return <Glyph style={hued(hue)} className="size-3.5 text-(--hue)" />
}

/** A dot of a kind's hue, beside its group's heading. */
export function HueDot({ hue }: { hue: string }) {
  return <span aria-hidden="true" style={hued(hue)} className="size-1.5 rounded-full bg-(--hue)" />
}

const HUE_TILE =
  "flex shrink-0 items-center justify-center bg-[color-mix(in_oklab,var(--hue)_15%,transparent)] text-(--hue) ring-1 ring-[color-mix(in_oklab,var(--hue)_22%,transparent)] ring-inset"
