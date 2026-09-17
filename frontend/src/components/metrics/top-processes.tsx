"use client"

import { useCallback } from "react"
import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent, rate } from "@/lib/format"
import type { ProcessRow } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type Sort = "cpu" | "memory" | "io"

const SHOWN = 8

/**
 * Who is spending the machine, right now.
 *
 * Every utilisation chart raises the same next question — "busy doing what?"
 * — and the answer is a process, not a percentage. DigitalOcean, Cockpit and
 * Glances all put a short process table beside the graphs for that reason;
 * this one is the top eight by whichever resource the reader picks, and the
 * way through to the full table is the header's link rather than a ninth row.
 *
 * Polled on its own cadence rather than read from the live socket: the
 * process table is a walk of /proc, which is not work to do twice a second.
 */
export function TopProcesses({ className }: { className?: string }) {
  // Persisted: somebody chasing a memory leak wants the memory order every
  // time they open the page.
  const [sort, setSort] = useViewState<Sort>("metrics.processes.sort", "cpu")

  const fetcher = useCallback(
    (signal: AbortSignal) => get<ProcessRow[]>("/processes/", { sort, limit: 50 }, signal),
    [sort],
  )
  const { data, error, loading } = usePoll<ProcessRow[]>(fetcher, 10_000, [sort])
  const rows = (data ?? []).slice(0, SHOWN)

  return (
    <Panel plain className={className}>
      <PanelHeader
        title="Top processes"
        actions={
          <div className="flex items-center gap-2">
            <ToggleGroup
              type="single"
              value={sort}
              onValueChange={(next) => setSort((next as Sort) || sort)}
              variant="outline"
              size="sm"
              aria-label="Order processes by"
            >
              <ToggleGroupItem value="cpu" className="px-2 text-hint">
                CPU
              </ToggleGroupItem>
              <ToggleGroupItem value="memory" className="px-2 text-hint">
                Memory
              </ToggleGroupItem>
              <ToggleGroupItem value="io" className="px-2 text-hint">
                I/O
              </ToggleGroupItem>
            </ToggleGroup>
            <Link
              href="/processes"
              className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
            >
              Processes <ArrowRight className="size-3" />
            </Link>
          </div>
        }
      />
      <PanelBody flush className={rows.length === 0 ? "py-4" : "-mx-3 px-3 py-1"}>
        {loading && !data ? (
          <LoadingRows rows={6} />
        ) : error && !data ? (
          <p className="text-body text-muted-foreground">The process table could not be read.</p>
        ) : rows.length === 0 ? (
          <p className="text-body text-muted-foreground">No processes reported.</p>
        ) : (
          <RowList key={sort} className="animate-rise">
            {rows.map((p) => (
              <Row
                key={p.pid}
                title={
                  <span className="flex min-w-0 items-baseline gap-2">
                    <span className="truncate">{p.name}</span>
                    {p.manager !== "unmanaged" && p.manager !== "kernel" && (
                      <Tag>{p.managerName ?? p.manager}</Tag>
                    )}
                  </span>
                }
                subtitle={`${p.pid} · ${p.username}${p.cmdline ? ` · ${p.cmdline}` : ""}`}
                mono
                trailing={<Figures process={p} sort={sort} />}
                className="py-2"
              />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Two figures per row: the one the list is ordered by, drawn at full weight,
 * and the other resource beside it so a process high on both is findable.
 */
function Figures({ process: p, sort }: { process: ProcessRow; sort: Sort }) {
  const cpu = percent(p.cpuPercent, 1)
  const mem = bytes(p.rss, 0)
  const io = rate((p.ioReadRate ?? 0) + (p.ioWriteRate ?? 0))
  const primary = sort === "cpu" ? cpu : sort === "memory" ? mem : io
  const secondary = sort === "cpu" ? mem : cpu
  return (
    <>
      <span className="numeric w-16 text-right text-hint text-muted-foreground">{secondary}</span>
      <span className="numeric w-16 text-right text-body font-medium">{primary}</span>
    </>
  )
}
