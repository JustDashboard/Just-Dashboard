"use client"

import { useMemo, useState } from "react"
import { Archive, Box, Cpu, FileText, Globe, Logs, Monorepo } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, relativeTime } from "@/lib/format"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"

/**
 * The groups, in the order somebody actually looks. The raw kind strings are
 * the API's vocabulary, not a reader's: "nginx" is a group of two files that a
 * person thinks of as their web server, and "app" is whatever was dropped into
 * the log roots.
 */
const GROUPS: { kind: LogSource["kind"]; label: string; icon: typeof FileText }[] = [
  { kind: "system", label: "System", icon: Cpu },
  { kind: "journal", label: "Systemd journal", icon: Monorepo },
  { kind: "docker", label: "Containers", icon: Box },
  { kind: "nginx", label: "Web server", icon: Globe },
  { kind: "pm2", label: "PM2", icon: FileText },
  { kind: "app", label: "Applications", icon: FileText },
]

export function SourceRail({
  index,
  loading,
  error,
  selectedId,
  onSelect,
}: {
  index: LogSourceIndex | undefined
  loading: boolean
  error: Error | undefined
  selectedId: string | null
  onSelect: (source: LogSource) => void
}) {
  const [filter, setFilter] = useState("")

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
    // Below lg the grid stacks, and an uncapped source list would take half the
    // window from the lines you came to read.
    <Panel className="max-h-64 min-h-0 lg:max-h-full">
      <PanelHeader
        icon={Logs}
        title="Sources"
      />
      <PanelToolbar>
        <SearchInput
          containerClassName="w-full"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter sources"
        />
      </PanelToolbar>
      <PanelBody flush scroll className="p-2">
        {loading && !index && <LoadingRows className="p-2" />}
        {error && <ErrorState error={error} className="m-2" />}
        <div className="space-y-3">
          {groups.map((group) => (
            <div key={group.kind}>
              <p className="eyebrow mb-1 flex items-center gap-1.5 px-2">
                <group.icon className="size-3" />
                {group.label}
                {group.items.length > 0 && (
                  <span className="numeric font-normal opacity-60">{group.items.length}</span>
                )}
              </p>
              {group.items.length === 0 ? (
                // An absent kind explains itself rather than simply not being
                // there: "no containers" and "no Docker on this host" call for
                // completely different next moves.
                <p className="px-2 pb-1 text-hint leading-snug text-muted-foreground">
                  {index?.missing[group.kind]}
                </p>
              ) : (
                <div className="space-y-0.5">
                  {group.items.map((source) => (
                    <SourceRow
                      key={source.id}
                      source={source}
                      selected={selectedId === source.id}
                      onSelect={() => onSelect(source)}
                    />
                  ))}
                </div>
              )}
            </div>
          ))}
          {!loading && groups.length === 0 && (
            <p className="px-2 py-6 text-center text-xs text-muted-foreground">
              Nothing matches that filter.
            </p>
          )}
        </div>
      </PanelBody>
    </Panel>
  )
}

function SourceRow({
  source,
  selected,
  onSelect,
}: {
  source: LogSource
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      onClick={onSelect}
      title={source.detail ?? source.path}
      // The selected source says so the same way the active nav item does —
      // one "you are here" language for both.
      className={cn(
        "flex w-full min-w-0 flex-col rounded-md px-2 py-1.5 text-left transition-colors",
        selected ? "bg-plot-primary font-medium text-foreground" : "hover:bg-accent",
      )}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        {source.status && <StatusDot state={source.status} />}
        <span className="truncate text-body">{source.label}</span>
        {(source.archives ?? 0) > 0 && (
          <span
            className="ml-auto flex shrink-0 items-center gap-0.5 text-micro text-muted-foreground"
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
    </button>
  )
}
