"use client"

import { useCallback } from "react"
import Link from "next/link"
import { ArrowRight, Cpu } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent, rate } from "@/lib/format"
import type { ProcessRow } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { ProductGlyph, hasProductLogo, processProduct } from "@/components/product-logo"
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
 *
 * A `RankList`: one line a process, its name the way to its own page, the
 * figure the list is ordered by in the last column and the other resource in
 * the column before it. They were two-line rows that opened nothing. The
 * block is eight rows tall waiting, empty and failed alike, so what is under
 * it does not move when the walk answers.
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
      <PanelBody flush className="min-h-66">
        {loading && !data ? (
          <RankSkeleton rows={SHOWN} />
        ) : error && !data ? (
          <p className="py-3 text-hint text-muted-foreground">
            The process table could not be read.
          </p>
        ) : (
          <RankList
            key={sort}
            className="animate-rise"
            emptyLabel="No processes reported."
            items={rows.map((p) => item(p, sort))}
          />
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * One process as a rank row: the product it is where it is one, its name, and
 * after it what manages it, its pid, its account and its command line, which
 * give way before the name does. The figure the list is ordered by is the
 * value; the other resource stands in the column before it, so a process high
 * on both is findable.
 */
function item(p: ProcessRow, sort: Sort): RankItem {
  const product = processProduct(p.name)
  const cpu = percent(p.cpuPercent, 1)
  const mem = bytes(p.rss, 0)
  const io = rate((p.ioReadRate ?? 0) + (p.ioWriteRate ?? 0))
  const managed = p.manager !== "unmanaged" && p.manager !== "kernel"
  const facts = [managed && (p.managerName ?? p.manager), p.pid, p.username, p.cmdline]
    .filter(Boolean)
    .join(" · ")
  return {
    key: String(p.pid),
    href: `/processes/${p.pid}`,
    title: `Open ${p.name}, process ${p.pid}`,
    mark: hasProductLogo(product) ? (
      <ProductGlyph id={product} />
    ) : (
      <Cpu className="size-3.5 text-muted-foreground" />
    ),
    label: p.name,
    hint: <span title={facts}>{facts}</span>,
    trailing: (
      <span className="numeric w-16 text-right text-hint text-muted-foreground">
        {sort === "cpu" ? mem : cpu}
      </span>
    ),
    value: sort === "cpu" ? cpu : sort === "memory" ? mem : io,
  }
}
