"use client"

import { useMemo, useState } from "react"
import { Archive, Box, Cpu, FileText, Globe, Monorepo, RefreshClockwise } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, relativeTime } from "@/lib/format"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import { SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { IconAction } from "@/components/icon-action"
import { ChipCount } from "@/components/tabs"
import { ProductLogo, imageProduct, platformProduct } from "@/components/product-logo"

/**
 * The groups, in the order somebody actually looks. The raw kind strings are
 * the API's vocabulary, not a reader's: "nginx" is a group of two files that a
 * person thinks of as their web server, and "app" is whatever was dropped into
 * the log roots.
 *
 * The glyph before each group is wayfinding — the same mark the sidebar and
 * the Overview use for the module the logs belong to — so the eye finds
 * "Containers" in the rail without reading down it.
 */
const GROUPS: { kind: LogSource["kind"]; label: string; icon: typeof FileText }[] = [
  { kind: "system", label: "System", icon: Cpu },
  { kind: "journal", label: "Systemd journal", icon: Monorepo },
  { kind: "docker", label: "Containers", icon: Box },
  { kind: "nginx", label: "Web server", icon: Globe },
  { kind: "pm2", label: "PM2", icon: FileText },
  { kind: "app", label: "Applications", icon: FileText },
]

/** What one source is, as a word beside its name. */
export const KIND_TAG: Record<LogSource["kind"], string> = {
  system: "system log",
  journal: "journal",
  docker: "container",
  nginx: "web server",
  pm2: "pm2 process",
  app: "application",
}

/**
 * Every log on the host, grouped by what writes it.
 *
 * One column of the logs workbench, sharing its frame with the lines beside
 * it: a rail with its own border next to a console with its own was two
 * boxes floating on the page, and the screen is one working surface.
 */
export function SourceRail({
  index,
  loading,
  error,
  selectedId,
  onSelect,
  onRescan,
  platform,
  className,
}: {
  index: LogSourceIndex | undefined
  /** The host's distribution, whose mark the system logs carry. */
  platform?: string
  loading: boolean
  error: Error | undefined
  selectedId: string | null
  onSelect: (source: LogSource) => void
  onRescan: () => void
  className?: string
}) {
  const [filter, setFilter] = useState("")
  const total = index?.sources.length ?? 0

  const groups = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const matches = (s: LogSource) =>
      !needle ||
      s.label.toLowerCase().includes(needle) ||
      s.path?.toLowerCase().includes(needle) ||
      s.detail?.toLowerCase().includes(needle)

    return GROUPS.map((group) => {
      const items = (index?.sources ?? []).filter((s) => s.kind === group.kind && matches(s))
      // A running container is the one somebody came here for; a stopped one
      // still has its last words and belongs underneath rather than missing.
      items.sort((a, b) => {
        const live = (s: LogSource) => (s.status === "running" || s.status === "online" ? 0 : 1)
        return live(a) - live(b) || a.label.localeCompare(b.label)
      })
      return { ...group, items }
    }).filter((g) => g.items.length > 0 || (index?.missing[g.kind] && !needle))
  }, [index, filter])

  return (
    <Pane flush aria-label="Log sources" className={cn("w-full", className)}>
      <PaneHeader className="gap-1.5 pl-3">
        <span className="text-xs font-medium">Sources</span>
        {total > 0 && <ChipCount>{total}</ChipCount>}
        <span className="flex-1" />
        <IconAction
          label="Rescan sources"
          className="size-7"
          pending={loading && Boolean(index)}
          onClick={onRescan}
        >
          <RefreshClockwise />
        </IconAction>
      </PaneHeader>
      {/* A filter over five sources is a box with nothing to do. It appears
          once the list is long enough that scanning it stops being faster
          than typing. */}
      {(total > 5 || filter) && (
        <div className="border-b border-hairline px-2 py-1.5">
          <SearchInput
            dense
            value={filter}
            spellCheck={false}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter sources"
            aria-label="Filter sources"
            containerClassName="sm:w-full"
          />
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {loading && !index && <LoadingRows className="p-1" />}
        {error && <ErrorState error={error} className="m-1" />}
        {index && (
          <div className="animate-rise space-y-3">
            {groups.map((group) => (
              <div key={group.kind}>
                <p className="eyebrow mb-0.5 flex items-center gap-1.5 px-2 py-1">
                  <group.icon aria-hidden className="size-3 shrink-0" />
                  <span className="truncate">{group.label}</span>
                  {group.items.length > 0 && <ChipCount>{group.items.length}</ChipCount>}
                </p>
                {group.items.length === 0 ? (
                  // An absent kind explains itself rather than simply not being
                  // there: "no containers" and "no Docker on this host" call for
                  // completely different next moves.
                  <p className="px-2 pb-1 text-hint leading-snug text-muted-foreground">
                    {index.missing[group.kind]}
                  </p>
                ) : (
                  <div className="space-y-px">
                    {group.items.map((source) => (
                      <SourceRow
                        key={source.id}
                        source={source}
                        platform={platform}
                        selected={selectedId === source.id}
                        onSelect={() => onSelect(source)}
                      />
                    ))}
                  </div>
                )}
              </div>
            ))}
            {groups.length === 0 && (
              <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                Nothing matches that filter.
              </p>
            )}
          </div>
        )}
      </div>
    </Pane>
  )
}

/**
 * What wrote a source, as the product it is: a container as its image, the
 * web server as nginx, a PM2 process as PM2, and the system's own files as
 * the distribution that writes them (§14). The journal and a stray
 * application log are no product and keep their group's glyph on the same
 * tile, so every name in the rail starts on one line.
 */
function sourceProduct(source: LogSource, platform: string | undefined) {
  switch (source.kind) {
    case "docker":
      // A compose service's detail is "stack · image"; the image is the product.
      return imageProduct(source.detail?.split(" · ").pop() || source.label)
    case "nginx":
      return "nginx-static"
    case "pm2":
      return "pm2"
    case "system":
      return platformProduct(platform)
    default:
      return undefined
  }
}

function SourceRow({
  source,
  platform,
  selected,
  onSelect,
}: {
  source: LogSource
  platform?: string
  selected: boolean
  onSelect: () => void
}) {
  const group = GROUPS.find((g) => g.kind === source.kind)
  return (
    <button
      type="button"
      onClick={onSelect}
      title={source.detail ?? source.path}
      aria-current={selected ? "true" : undefined}
      // The chosen source is the neutral fill every selection in the product
      // takes — the terminal's active session, a table's chosen row — so
      // "you are here" reads the same way on every rail.
      className={cn(
        "flex w-full min-w-0 items-center gap-2.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
        selected ? "bg-accent text-foreground" : "hover:bg-row-hover",
      )}
    >
      <ProductLogo id={sourceProduct(source, platform)} size="sm" fallback={group?.icon} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cn("truncate text-body leading-tight", selected && "font-medium")}>
            {source.label}
          </span>
          {source.status && <StatusDot state={source.status} className="shrink-0" />}
          {(source.archives ?? 0) > 0 && (
            <span
              className="numeric ml-auto flex shrink-0 items-center gap-0.5 text-micro text-muted-foreground"
              title={`${source.archives} rotated archives, ${bytes(source.archiveBytes)} — searchable`}
            >
              <Archive className="size-2.5" />
              {source.archives}
            </span>
          )}
        </span>
        <span className="truncate text-hint text-muted-foreground">
          {source.size !== undefined && source.size > 0
            ? `${bytes(source.size)} · ${relativeTime(source.modified)}`
            : (source.detail ?? source.status)}
        </span>
      </span>
    </button>
  )
}
