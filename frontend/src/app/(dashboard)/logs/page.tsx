"use client"

import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Logs, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import { EMPTY_FILTER, readLogWindow } from "@/lib/log-filter"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { SourceRail } from "@/components/logs/source-rail"
import { ExportDialog } from "@/components/logs/export-dialog"
import { LogWorkspace } from "@/components/logs/log-workspace"

function toLocalInput(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 23)
}

export default function LogsPage() {
  const params = useSearchParams()
  const sources = usePoll(
    (signal) => get<LogSourceIndex>("/logs/sources", undefined, signal),
    60000,
  )

  // Keep exact instants from shared links; converting them to local input values
  // before searching would lose the offset during a repeated daylight-saving hour.
  const [initialWindow] = useState(() => readLogWindow(params))
  const [windowError, setWindowError] = useState(initialWindow.error)
  const [picked, setPicked] = useState<string | null>(() => params.get("source"))
  const [mode, setMode] = useState<LogMode>(() =>
    params.get("mode") === "search" || initialWindow.since || initialWindow.until
      ? "search"
      : "live",
  )
  const [filter, setFilter] = useState<LogFilterState>(() => ({
    ...EMPTY_FILTER,
    q: params.get("q") ?? "",
  }))
  const [unit, setUnit] = useState(
    () =>
      params.get("unit") ??
      (params.get("source")?.startsWith("journal:")
        ? params.get("source")!.slice("journal:".length)
        : ""),
  )
  const [range, setRange] = useState<LogTimeRange>(
    initialWindow.since || initialWindow.until ? "custom" : "24h",
  )
  const [since, setSince] = useState(initialWindow.since)
  const [until, setUntil] = useState(initialWindow.until)
  const [context, setContext] = useState(0)
  const [archives, setArchives] = useState(false)
  const [boot, setBoot] = useState(false)

  // The first source is streaming before you choose one. Landing on an empty
  // pane and a "pick something" sign wastes the visit: nine times out of ten
  // the answer is in syslog, and the operator can switch in one click if it is
  // not. Derived rather than stored, so no effect has to sync it.
  const selected: LogSource | null = useMemo(() => {
    const list = sources.data?.sources ?? []
    if (!picked) return list[0] ?? null
    return (
      list.find(
        (s) => s.id === picked || (picked.startsWith("journal:") && s.kind === "journal"),
      ) ?? null
    )
  }, [sources.data, picked])

  // The journal is one source with a thousand faces, so the unit rides on the
  // id rather than filling the rail with systemd's inventory.
  const sourceId = useMemo(() => {
    if (!selected) return ""
    if (selected.kind === "journal" && unit) return `journal:${unit}`
    return selected.id
  }, [selected, unit])

  useEffect(() => {
    if (!sourceId || windowError) return
    const url = new URL(window.location.href)
    url.searchParams.set("source", sourceId)
    if (mode === "search") url.searchParams.set("mode", "search")
    else url.searchParams.delete("mode")
    if (filter.q) url.searchParams.set("q", filter.q)
    else url.searchParams.delete("q")
    for (const [key, value] of Object.entries(
      range === "custom" ? { since, until } : { since: "", until: "" },
    )) {
      if (value && Number.isFinite(Date.parse(value)))
        url.searchParams.set(key, new Date(value).toISOString())
      else url.searchParams.delete(key)
    }
    window.history.replaceState(null, "", url)
  }, [sourceId, mode, filter.q, range, since, until, windowError])

  return (
    <Page fill>
      <PageHeader
        eyebrow="Server"
        title="Logs"
        actions={
          <>
            {sourceId && (
              <ExportDialog sourceId={sourceId} source={selected} filter={filter} boot={boot} />
            )}
            <Button variant="outline" size="sm" onClick={() => sources.refresh()}>
              <RefreshClockwise className="size-4" />
              Rescan
            </Button>
          </>
        }
      />

      <div className="grid min-h-0 flex-1 gap-5 lg:grid-cols-[17rem_minmax(0,1fr)] [&>*]:min-w-0">
        <SourceRail
          index={sources.data}
          loading={sources.loading}
          error={sources.error}
          selectedId={selected?.id ?? null}
          onSelect={(source) => {
            setPicked(source.id)
            if (source.kind !== "journal") setUnit("")
          }}
        />

        {windowError ? (
          <EmptyState
            icon={Logs}
            title="Invalid log window"
            description={windowError}
            action={
              <Button
                variant="outline"
                onClick={() => {
                  setWindowError(undefined)
                  setRange("24h")
                  setSince("")
                  setUntil("")
                }}
              >
                Use last 24 hours
              </Button>
            }
          />
        ) : selected ? (
          <LogWorkspace
            key={sourceId}
            source={selected}
            sourceId={sourceId}
            units={sources.data?.units ?? []}
            mode={mode}
            onModeChange={setMode}
            filter={filter}
            onFilterChange={setFilter}
            unit={unit}
            onUnitChange={setUnit}
            range={range}
            onRangeChange={setRange}
            since={since}
            until={until}
            onSinceChange={setSince}
            onUntilChange={setUntil}
            onCustomRange={(from, to) => {
              setRange("custom")
              setSince(toLocalInput(from))
              setUntil(toLocalInput(to))
            }}
            context={context}
            onContextChange={setContext}
            archives={archives}
            onArchivesChange={setArchives}
            boot={boot}
            onBootChange={setBoot}
          />
        ) : (
          <EmptyState
            className="flex-1"
            icon={Logs}
            title={
              sources.loading
                ? "Looking for logs…"
                : picked
                  ? "Requested log source unavailable"
                  : "No log sources on this host"
            }
            description={
              sources.loading
                ? undefined
                : picked
                  ? `The requested source (${picked}) is not in the current inventory. It may have been removed or its owner may be unavailable. Rescan or choose another source.`
                  : `Nothing readable was found under ${(sources.data?.roots ?? []).join(", ") || "the configured log roots"}. Containers, PM2 processes and the journal appear here too when they are present.`
            }
          />
        )}
      </div>
    </Page>
  )
}
