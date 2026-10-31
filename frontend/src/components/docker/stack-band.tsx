"use client"

import { Layers } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, imageProduct, imageProducts } from "@/components/product-logo"
import { DOT_COLOR } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import {
  DASHBOARD_STACK,
  stackLane,
  type StackChange,
  type StackLine,
} from "@/components/docker/stack-readings"

/** How many stacks each block names; the rest of the machine is one muted span. */
const SHOWN = 5

/** Recent's axis, and what its count is of. Older changes sit at the far end. */
const DAY = 86_400_000

/**
 * What the stacks are doing: the five using the most processor and the most
 * memory, each a span of one bar as wide as the machine, and the last things
 * that happened to their containers, placed on the last day.
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
  loaded,
  eventsRead,
  listening,
  selected,
  onSelect,
  onOpen,
}: {
  lines: StackLine[]
  snapshot?: Snapshot
  changes: StackChange[]
  /** Whether the stack list has been read, so none running means none rather than not yet. */
  loaded: boolean
  /** Whether the events feed has answered yet. */
  eventsRead: boolean
  /** Whether the events feed is recording, so an empty Recent says which empty it is. */
  listening: boolean
  selected: string
  onSelect: (stack: string) => void
  onOpen: (stack: string) => void
}) {
  const now = useNow(15_000)
  const up = lines.filter((l) => l.stack.running > 0)
  const measured = up.some((l) => l.measured)
  const byCPU = up
    .filter((l) => l.measured && l.cpu > 0)
    .sort((a, b) => b.cpu - a.cpu)
    .slice(0, SHOWN)
  const byMemory = up
    .filter((l) => l.memory > 0)
    .sort((a, b) => b.memory - a.memory)
    .slice(0, SHOWN)

  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? (snapshot.cpu.totalPercent * coreCount) / 100 : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const cpuSum = sum(byCPU, (l) => l.cpu)
  const memSum = sum(byMemory, (l) => l.memory)
  const today = changes.filter((c) => now - c.at < DAY).length

  const nothing = loaded ? "No stack is running." : <TextShimmer>Reading the stacks…</TextShimmer>

  const share = (l: StackLine): Pick<ReadingPart, "key" | "mark" | "name" | "press"> => {
    const pressed = selected === l.stack.name
    return {
      key: l.stack.name,
      mark: <StackGlyph line={l} />,
      name: l.stack.name,
      press: {
        label: `Only ${l.stack.name}'s containers`,
        pressed,
        onPress: () => onSelect(pressed ? "" : l.stack.name),
      },
    }
  }

  return (
    <div
      data-slot="stack-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <BarReading
        title="Processor"
        label="Processor by stack"
        list="Busiest stacks"
        hue={HUE.cpu}
        trail
        live
        capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
        rest={busy !== undefined ? Math.max(busy * 100 - cpuSum, 0) : 0}
        format={cores}
        figure={
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
        message={
          up.length === 0 ? (
            nothing
          ) : !measured ? (
            <TextShimmer>Measuring the first interval…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "No stack is using the processor."
          ) : undefined
        }
        parts={byCPU.map((l) => ({
          ...share(l),
          value: l.cpu,
          figure:
            l.cpu / 100 >= 0.005 ? (
              <LiveFigure value={l.cpu / 100} decimals={l.cpu >= 99.5 ? 1 : 2} unit=" cores" />
            ) : (
              "idle"
            ),
          label: `${l.stack.name} ${cores(l.cpu)}`,
          title: `${percent(l.cpu)} of one core, summed over its containers`,
        }))}
      />

      <BarReading
        title="Memory"
        label="Memory by stack"
        list="Stacks using the most memory"
        hue={HUE.mem}
        trail
        live
        capacity={memTotal > 0 ? memTotal : memSum}
        rest={Math.max(memUsed - memSum, 0)}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          memTotal > 0 && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={memUsed} />
              </span>
              of {bytes(memTotal, 0)} in use
            </span>
          )
        }
        message={
          up.length === 0 ? (
            nothing
          ) : byMemory.length === 0 ? (
            <TextShimmer>Waiting for the first reading…</TextShimmer>
          ) : undefined
        }
        parts={byMemory.map((l) => ({
          ...share(l),
          value: l.memory,
          figure: <LiveBytes value={l.memory} />,
          label: `${l.stack.name} ${bytes(l.memory)}`,
          title:
            `${bytes(l.memory)} across its containers` +
            (memTotal > 0 ? ` — ${percent((l.memory / memTotal) * 100)} of the host` : ""),
        }))}
      />

      <BarReading
        title="Recent"
        label="Recent changes"
        list="Latest changes"
        className="lg:col-span-2 xl:col-span-1"
        ticks
        figure={
          <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
            {today > 0 ? `${plural(today, "change")} in the last day` : "last day"}
          </span>
        }
        message={
          !eventsRead ? (
            <TextShimmer>Reading what Docker did…</TextShimmer>
          ) : changes.length > 0 ? undefined : listening ? (
            "Nothing has started, stopped or failed since the dashboard began listening."
          ) : (
            "The dashboard is not receiving Docker's events."
          )
        }
        parts={changes.slice(0, SHOWN + 1).map((change) => {
          const product = change.image ? imageProduct(change.image) : undefined
          return {
            key: change.key,
            value: 1,
            at: timeAt(change.at, now, DAY),
            color: DOT_COLOR[change.tone],
            mark: product ? (
              <ProductGlyph id={product} className="size-3.5" />
            ) : (
              <Layers aria-hidden className="size-3.5 text-muted-foreground" />
            ),
            name: change.service,
            // What happened leads; the stack gives way first when the line is short.
            detail: (
              <>
                <span
                  className={cn(
                    change.tone === "danger" && "text-destructive",
                    change.tone === "warning" && "text-warning",
                    change.tone === "running" && "text-success",
                  )}
                >
                  {change.verb}
                </span>
                {" · "}
                <span style={{ color: stackLane(change.stack) }}>{change.stack}</span>
              </>
            ),
            figure: (
              <span className="text-hint font-normal text-muted-foreground">
                {ago(change.at / 1000, now)}
              </span>
            ),
            label: `${change.service} of ${change.stack} ${change.verb} ${ago(change.at / 1000, now)}`,
            title: new Date(change.at).toLocaleString(),
            press: {
              label: `Open ${change.stack}: ${change.service} ${change.verb}`,
              opens: true,
              onPress: () => onOpen(change.stack),
            },
          }
        })}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}

/**
 * A stack as the products it runs: the one it is named for. The dashboard's
 * own stack is the dashboard, not the Caddy its images lead with.
 */
function StackGlyph({ line }: { line: StackLine }) {
  const images = line.stack.services.map((s) => s.image).filter(Boolean)
  const product = images.length > 0 ? imageProducts(images)[0] : "docker-compose"
  return line.stack.name === DASHBOARD_STACK ? (
    <LogoGlyph small className="h-3.5 w-auto text-brand" />
  ) : product ? (
    <ProductGlyph id={product} className="size-3.5" />
  ) : (
    <Layers aria-hidden className="size-3.5 text-muted-foreground" />
  )
}
