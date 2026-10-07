"use client"

import { Cpu, Filter } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LiveBytes, LiveFigure, HUE } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { ProcessGroup, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores, groupName, groupProduct, managerName } from "@/components/procs/shared"

/** How many workloads each half names; the rest of the machine is one muted span. */
const SHOWN = 5

/**
 * The share each rank takes of its measurement's hue, heaviest darkest. The
 * workloads are told apart by their marks and names; the bar's colour says
 * which measurement it is, as every chart of it does (§10), and the steps say
 * which part of the bar a row is.
 */
const STEPS = [100, 74, 54, 40, 30]

export function shade(color: string, rank: number) {
  return `color-mix(in oklab, ${color} ${STEPS[rank] ?? 24}%, transparent)`
}

/**
 * What is using the machine: the five heaviest workloads by processor and by
 * memory, each a span of one bar as wide as the machine.
 *
 * The four figures this replaced (processes, blocked, zombies, unmanaged) each
 * said what a chip on the table already said — the state chips count blocked
 * and zombies, the owner chips the unmanaged — and none answered the question
 * a reader opens the page with, which is *who*. A table answers it one PID at
 * a time, and forty Chrome renderers or twenty Postgres backends are forty and
 * twenty rows of the same answer. The backend groups the whole snapshot by
 * supervisor, or by program for what was started by hand, with shared pages
 * counted once, so these sums are over every process and not the listed ones.
 *
 * Each span eases to the next poll's width and each figure glides to it, so
 * the bar is seen to move. A row narrows the table to its workload.
 */
export function WorkloadBand({
  groups,
  snapshot,
  ratesReady,
  selected,
  onSelect,
}: {
  groups: ProcessGroup[]
  snapshot?: Snapshot
  ratesReady: boolean
  selected: string
  onSelect: (key: string) => void
}) {
  const byCPU = [...groups]
    .filter((g) => g.cpuPercent > 0)
    .sort((a, b) => b.cpuPercent - a.cpuPercent)
    .slice(0, SHOWN)
  const byMemory = [...groups]
    .filter((g) => g.memory > 0)
    .sort((a, b) => b.memory - a.memory)
    .slice(0, SHOWN)

  // The machine's own readings, from the metrics socket, size the bars: a
  // core is a hundred points of the processor bar, and the memory bar is the
  // host's total with what it reports in use beyond these five drawn muted.
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0

  return (
    <div data-slot="workloads" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <Panel plain aria-label="Processor by workload">
        <PanelHeader
          title="Processor"
          actions={
            coreCount > 0 &&
            busy !== undefined && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveFigure value={busy} decimals={1} />
                </span>
                of {plural(coreCount, "core")} busy
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : sum(byCPU, (g) => g.cpuPercent)}
            rest={
              busy !== undefined ? Math.max(busy * 100 - sum(byCPU, (g) => g.cpuPercent), 0) : 0
            }
            parts={byCPU.map((g, rank) => ({
              key: g.key,
              value: g.cpuPercent,
              color: shade(HUE.cpu, rank),
              label: `${groupName(g)} ${cores(g.cpuPercent)}`,
            }))}
            format={cores}
          />
          {!ratesReady ? (
            <p className="py-2 text-body">
              <TextShimmer>Measuring the first interval…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">Nothing is using the processor.</p>
          ) : (
            <WorkloadRows
              groups={byCPU}
              color={HUE.cpu}
              selected={selected}
              onSelect={onSelect}
              figure={(g) =>
                g.cpuPercent / 100 >= 0.005 ? (
                  <LiveFigure
                    value={g.cpuPercent / 100}
                    decimals={g.cpuPercent >= 1000 ? 0 : g.cpuPercent >= 99.5 ? 1 : 2}
                    unit=" cores"
                  />
                ) : (
                  "idle"
                )
              }
              title={(g) => `${percent(g.cpuPercent)} of one core, summed over its processes`}
            />
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by workload">
        <PanelHeader
          title="Memory"
          actions={
            memTotal > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveBytes value={memUsed} />
                </span>
                of {bytes(memTotal, 0)} in use
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : sum(byMemory, (g) => g.memory)}
            rest={Math.max(memUsed - sum(byMemory, (g) => g.memory), 0)}
            parts={byMemory.map((g, rank) => ({
              key: g.key,
              value: g.memory,
              color: shade(HUE.mem, rank),
              label: `${groupName(g)} ${bytes(g.memory)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">No memory is reported.</p>
          ) : (
            <WorkloadRows
              groups={byMemory}
              color={HUE.mem}
              selected={selected}
              onSelect={onSelect}
              figure={(g) => <LiveBytes value={g.memory} />}
              title={(g) =>
                `${bytes(g.memory)} resident, pages its processes share counted once` +
                (memTotal > 0 ? ` — ${percent((g.memory / memTotal) * 100)} of the host` : "")
              }
            />
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/**
 * One bar as wide as the machine: a span per workload, heaviest first, then
 * everything else the host reports in use, muted, then the track for what is
 * free. Each span eases to its next width rather than jumping. The Services
 * page draws its units on the same bar.
 */
export function ShareBar({
  label,
  capacity,
  rest,
  parts,
  format,
}: {
  label: string
  capacity: number
  rest: number
  parts: { key: string; value: number; color: string; label: string }[]
  format: (value: number) => string
}) {
  const width = (value: number) =>
    capacity > 0 ? `${Math.min((value / capacity) * 100, 100)}%` : "0%"
  return (
    <div
      role="img"
      aria-label={`${label}: ${parts.map((p) => p.label).join(", ")}${rest > 0 ? `, everything else ${format(rest)}` : ""}`}
      className="flex h-2.5 w-full overflow-hidden rounded-sm bg-meter-track"
    >
      {parts.map((part) => (
        <span
          key={part.key}
          title={part.label}
          className="h-full shrink-0 border-r border-background transition-[width] duration-700 ease-out last:border-r-0"
          style={{ width: width(part.value), background: part.color }}
        />
      ))}
      {rest > 0 && (
        <span
          title={`Everything else ${format(rest)}`}
          className="h-full shrink-0 bg-muted-foreground/25 transition-[width] duration-700 ease-out"
          style={{ width: width(rest) }}
        />
      )}
    </div>
  )
}

/**
 * The named spans of a bar, one line each: the key that finds it on the bar,
 * what it runs, how many processes and under what, and its figure. A press
 * narrows the table to it — the funnel a `StatButton` reveals says so — and
 * the workload on screen is the selected row.
 */
function WorkloadRows({
  groups,
  color,
  selected,
  onSelect,
  figure,
  title,
}: {
  groups: ProcessGroup[]
  color: string
  selected: string
  onSelect: (key: string) => void
  figure: (group: ProcessGroup) => React.ReactNode
  title: (group: ProcessGroup) => string
}) {
  return (
    <ul className="-mx-2">
      {groups.map((g, rank) => {
        const product = groupProduct(g)
        const pressed = selected === g.key
        return (
          <li key={g.key}>
            <button
              type="button"
              aria-pressed={pressed}
              aria-label={`Only ${groupName(g)}'s processes`}
              title={title(g)}
              onClick={() => onSelect(pressed ? "" : g.key)}
              className={cn(
                "group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
                pressed ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              <span
                aria-hidden
                className="h-2.5 w-0.5 shrink-0 rounded-full"
                style={{ background: shade(color, rank) }}
              />
              <span className="flex size-4 shrink-0 items-center justify-center">
                {product ? (
                  <ProductGlyph id={product} className="size-3.5" />
                ) : (
                  <Cpu aria-hidden className="size-3.5 text-muted-foreground" />
                )}
              </span>
              <span className="min-w-0 truncate font-medium">{groupName(g)}</span>
              <span className="numeric shrink-0 truncate text-hint text-muted-foreground">
                {g.count > 1 ? `×${g.count}` : `PID ${g.pid}`}
                {g.manager !== "session" && g.manager !== "unmanaged" && g.manager !== "kernel"
                  ? ` · ${managerName(g.manager)}`
                  : ""}
              </span>
              <span className="numeric ml-auto shrink-0 font-medium text-foreground">
                {figure(g)}
              </span>
              <Filter
                aria-hidden
                className={cn(
                  "size-3.5 shrink-0 text-muted-foreground",
                  pressed ? "text-foreground" : rowReveal(),
                )}
              />
            </button>
          </li>
        )
      })}
    </ul>
  )
}
