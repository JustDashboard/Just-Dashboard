"use client"

import { ArrowRight, Filter, Layers } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, imageProduct, imageProducts } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import { ShareBar, shade } from "@/components/procs/workloads"
import { stackLane, type StackChange, type StackLine } from "@/components/docker/stack-readings"

/** How many stacks each block names; the rest of the machine is one muted span. */
const SHOWN = 5

/**
 * What the stacks are doing: the five using the most processor and the most
 * memory, each a span of one bar as wide as the machine, and the last things
 * that happened to their containers.
 *
 * A stack is an application, and "which application is eating the server"
 * is a question the containers list answers one container at a time — a
 * shop is an API, a database and a cache, and the three readings only mean
 * something added up. The sums are of the containers socket's own frames, so
 * each span eases and each figure glides as Docker reports. A press narrows
 * the table to that stack; a change opens it.
 */
export function StackBand({
  lines,
  snapshot,
  changes,
  listening,
  selected,
  onSelect,
  onOpen,
}: {
  lines: StackLine[]
  snapshot?: Snapshot
  changes: StackChange[]
  /** Whether the events feed is recording, so an empty Recent says which empty it is. */
  listening: boolean
  selected: string
  onSelect: (stack: string) => void
  onOpen: (stack: string) => void
}) {
  const now = useNow(15_000)
  const live = lines.filter((l) => l.stack.running > 0)
  const measured = live.some((l) => l.measured)
  const byCPU = live
    .filter((l) => l.measured && l.cpu > 0)
    .sort((a, b) => b.cpu - a.cpu)
    .slice(0, SHOWN)
  const byMemory = live
    .filter((l) => l.memory > 0)
    .sort((a, b) => b.memory - a.memory)
    .slice(0, SHOWN)

  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, (l) => l.cpu)
  const memSum = sum(byMemory, (l) => l.memory)
  const today = changes.filter((c) => now - c.at < 86_400_000).length

  return (
    <div
      data-slot="stack-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Processor by stack">
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
            capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
            rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
            parts={byCPU.map((l, rank) => ({
              key: l.stack.name,
              value: l.cpu,
              color: shade(HUE.cpu, rank),
              label: `${l.stack.name} ${cores(l.cpu)}`,
            }))}
            format={cores}
          />
          {live.length > 0 && !measured ? (
            <p className="py-2 text-body">
              <TextShimmer>Measuring the first interval…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">No stack is using the processor.</p>
          ) : (
            <ul className="-mx-2">
              {byCPU.map((l, rank) => (
                <StackShare
                  key={l.stack.name}
                  line={l}
                  color={shade(HUE.cpu, rank)}
                  selected={selected}
                  onSelect={onSelect}
                  title={`${percent(l.cpu)} of one core, summed over its containers`}
                  figure={
                    l.cpu / 100 >= 0.005 ? (
                      <LiveFigure
                        value={l.cpu / 100}
                        decimals={l.cpu >= 99.5 ? 1 : 2}
                        unit=" cores"
                      />
                    ) : (
                      "idle"
                    )
                  }
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by stack">
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
            capacity={memTotal > 0 ? memTotal : memSum}
            rest={Math.max(memUsed - memSum, 0)}
            parts={byMemory.map((l, rank) => ({
              key: l.stack.name,
              value: l.memory,
              color: shade(HUE.mem, rank),
              label: `${l.stack.name} ${bytes(l.memory)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {live.length > 0 ? (
                <TextShimmer>Waiting for the first reading…</TextShimmer>
              ) : (
                "No stack is running."
              )}
            </p>
          ) : (
            <ul className="-mx-2">
              {byMemory.map((l, rank) => (
                <StackShare
                  key={l.stack.name}
                  line={l}
                  color={shade(HUE.mem, rank)}
                  selected={selected}
                  onSelect={onSelect}
                  title={
                    `${bytes(l.memory)} across its containers` +
                    (memTotal > 0 ? ` — ${percent((l.memory / memTotal) * 100)} of the host` : "")
                  }
                  figure={<LiveBytes value={l.memory} />}
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Recent changes" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
              {today > 0 ? `${plural(today, "change")} in the last day` : "last day"}
            </span>
          }
        />
        <PanelBody className="pt-3">
          {changes.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {listening
                ? "Nothing has started, stopped or failed since the dashboard began listening."
                : "The dashboard is not receiving Docker's events."}
            </p>
          ) : (
            <ul className="-mx-2">
              {changes.slice(0, SHOWN + 1).map((change) => (
                <ChangeLine key={change.key} change={change} now={now} onOpen={onOpen} />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/** A stack as the products it runs, at a line's height: the one it is named for. */
function StackGlyph({ line }: { line: StackLine }) {
  const images = line.stack.services.map((s) => s.image).filter(Boolean)
  const product = images.length > 0 ? imageProducts(images)[0] : "docker-compose"
  return (
    <span className="flex size-4 shrink-0 items-center justify-center">
      {product ? (
        <ProductGlyph id={product} className="size-3.5" />
      ) : (
        <Layers aria-hidden className="size-3.5 text-muted-foreground" />
      )}
    </span>
  )
}

/**
 * A named span of a bar: the key that finds it on the bar, the stack, how many
 * of its containers are running, and its figure. A press narrows the table to
 * it — the funnel a `StatButton` reveals says so — and the stack on screen is
 * the selected row.
 */
function StackShare({
  line,
  color,
  selected,
  onSelect,
  figure,
  title,
}: {
  line: StackLine
  color: string
  selected: string
  onSelect: (stack: string) => void
  figure: React.ReactNode
  title: string
}) {
  const pressed = selected === line.stack.name
  const running = line.lines.filter((l) => l.state === "running").length
  return (
    <li>
      <button
        type="button"
        aria-pressed={pressed}
        aria-label={`Only ${line.stack.name}'s containers`}
        title={title}
        onClick={() => onSelect(pressed ? "" : line.stack.name)}
        className={cn(
          "group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
          pressed ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span
          aria-hidden
          className="h-2.5 w-0.5 shrink-0 rounded-full"
          style={{ background: color }}
        />
        <StackGlyph line={line} />
        <span className="min-w-0 truncate font-medium">{line.stack.name}</span>
        <span className="numeric shrink-0 truncate text-hint text-muted-foreground">
          {plural(running, "container")}
        </span>
        <span className="numeric ml-auto shrink-0 font-medium text-foreground">{figure}</span>
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
}

/**
 * A change: the state's dot, the service as the product it runs, its stack,
 * what happened and when. A press opens the stack, where its logs and its
 * history say why.
 */
function ChangeLine({
  change,
  now,
  onOpen,
}: {
  change: StackChange
  now: number
  onOpen: (stack: string) => void
}) {
  const product = change.image ? imageProduct(change.image) : undefined
  return (
    <li className="animate-rise">
      <button
        type="button"
        aria-label={`Open ${change.stack}: ${change.service} ${change.verb}`}
        title={new Date(change.at).toLocaleString()}
        onClick={() => onOpen(change.stack)}
        className="group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <StatusDot tone={change.tone} className="mx-px" />
        <span className="flex size-4 shrink-0 items-center justify-center">
          {product ? (
            <ProductGlyph id={product} className="size-3.5" />
          ) : (
            <Layers aria-hidden className="size-3.5 text-muted-foreground" />
          )}
        </span>
        <span className="max-w-[45%] shrink-0 truncate font-medium">{change.service}</span>
        {/* The stack gives way first: what happened and when are the line. */}
        <span
          className="min-w-0 flex-1 truncate text-hint"
          style={{ color: stackLane(change.stack) }}
        >
          {change.stack}
        </span>
        <span
          className={cn(
            "shrink-0 text-hint",
            change.tone === "danger"
              ? "text-destructive"
              : change.tone === "warning"
                ? "text-warning"
                : change.tone === "running"
                  ? "text-success"
                  : "text-muted-foreground",
          )}
        >
          {change.verb}
        </span>
        <span className="numeric w-13 shrink-0 text-right text-hint text-muted-foreground">
          {ago(change.at / 1000, now)}
        </span>
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      </button>
    </li>
  )
}
