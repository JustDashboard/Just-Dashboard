"use client"

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
import { hasProductLogo, ProductGlyph } from "@/components/product-logo"
import type { SearchKind } from "./model"

/**
 * What each kind of result is drawn as when it has no product of its own, and
 * what one of it is called above its name in the preview.
 *
 * There is no hue per kind. Every row used to stand on a tile tinted in its
 * kind's `--tag-*` lane, so a list of fifty pages and resources was fifty
 * faded squares in seven colours, and the colour said nothing the group
 * heading above it had not. The colour on the list now is the products' own,
 * from their artwork (§14), and a kind with no product is its glyph in the
 * rail's grey.
 */
export const KINDS: Record<SearchKind, { icon: Icon; noun: string }> = {
  recent: { icon: Clock, noun: "Visited" },
  command: { icon: Lightning, noun: "Command" },
  page: { icon: Layers, noun: "Page" },
  project: { icon: Rocket, noun: "Project" },
  site: { icon: Globe, noun: "Site" },
  database: { icon: Database, noun: "Database" },
  container: { icon: Box, noun: "Container" },
  stack: { icon: Layers, noun: "Compose stack" },
  repo: { icon: GitBranch, noun: "Repository" },
  service: { icon: Servers, noun: "systemd service" },
  app: { icon: ChartActivity, noun: "PM2 app" },
  backup: { icon: Archive, noun: "Backup job" },
  board: { icon: Pencil, noun: "Board" },
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

/**
 * A result's mark: the product's own logo bare, or its glyph as the rail draws
 * it — grey at rest, ink on the selected row. One width for both, so every
 * title starts on one line whichever kind the row is.
 */
export function ResultMark({
  product,
  icon: Glyph,
}: {
  product?: string
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <span
      aria-hidden="true"
      className="flex size-5 shrink-0 items-center justify-center text-muted-foreground transition-colors group-data-[selected=true]/result:text-foreground"
    >
      {product && hasProductLogo(product) ? (
        <ProductGlyph id={product} className="size-4" />
      ) : (
        <Glyph className="size-4" />
      )}
    </span>
  )
}
