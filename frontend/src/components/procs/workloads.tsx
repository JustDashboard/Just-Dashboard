"use client"

import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { Cpu } from "@/components/icons"
import { LiveBytes, LiveFigure, HUE } from "@/components/overview/readings"
import { ProductGlyph } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { ProcessGroup, Snapshot } from "@/lib/types"
import { cores, groupName, groupProduct, managerName } from "@/components/procs/shared"

/** How many workloads each half names; the rest of the machine is one muted span. */
const SHOWN = 5

/**
 * What is using the machine: the five heaviest workloads by processor and by
 * memory, each a span of one bar as wide as the machine, one of them on the
 * line beneath it and the marks of all five under that.
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
 * The workloads are polled with the table rather than pushed, so neither bar
 * is marked live even though the host's own figures in the heads are. A press
 * narrows the table to its workload. The block is drawn before the first read,
 * so the table under it does not move when that read lands.
 */
export function WorkloadBand({
  groups,
  loading,
  snapshot,
  ratesReady,
  selected,
  onSelect,
}: {
  /** The last answer; nothing until the first read has landed. */
  groups?: ProcessGroup[]
  /** The first read is still out, as against having failed. */
  loading: boolean
  snapshot?: Snapshot
  ratesReady: boolean
  selected: string
  onSelect: (key: string) => void
}) {
  const byCPU = [...(groups ?? [])]
    .filter((g) => g.cpuPercent > 0)
    .sort((a, b) => b.cpuPercent - a.cpuPercent)
    .slice(0, SHOWN)
  const byMemory = [...(groups ?? [])]
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
  const cpuShown = sum(byCPU, (g) => g.cpuPercent)
  const memShown = sum(byMemory, (g) => g.memory)

  const unread = groups ? undefined : loading ? (
    <TextShimmer>Reading the workloads…</TextShimmer>
  ) : (
    "The workloads could not be read."
  )

  const line = (
    g: ProcessGroup,
  ): Pick<ReadingPart, "key" | "mark" | "name" | "detail" | "press"> => {
    const product = groupProduct(g)
    const pressed = selected === g.key
    return {
      key: g.key,
      mark: product ? (
        <ProductGlyph id={product} className="size-3.5" />
      ) : (
        <Cpu aria-hidden className="size-3.5 text-muted-foreground" />
      ),
      name: groupName(g),
      detail:
        (g.count > 1 ? `×${g.count}` : `PID ${g.pid}`) +
        (g.manager !== "session" && g.manager !== "unmanaged" && g.manager !== "kernel"
          ? ` · ${managerName(g.manager)}`
          : ""),
      press: {
        label: `Only ${groupName(g)}'s processes`,
        pressed,
        onPress: () => onSelect(pressed ? "" : g.key),
      },
    }
  }

  return (
    <div data-slot="workloads" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <BarReading
        title="Processor"
        label="Processor by workload"
        list="Busiest workloads"
        hue={HUE.cpu}
        trail
        capacity={coreCount > 0 ? coreCount * 100 : cpuShown}
        rest={busy !== undefined ? Math.max(busy * 100 - cpuShown, 0) : 0}
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
          unread ??
          (!ratesReady ? (
            <TextShimmer>Measuring the first interval…</TextShimmer>
          ) : byCPU.length === 0 ? (
            "Nothing is using the processor."
          ) : undefined)
        }
        parts={byCPU.map((g) => ({
          ...line(g),
          value: g.cpuPercent,
          figure:
            g.cpuPercent / 100 >= 0.005 ? (
              <LiveFigure
                value={g.cpuPercent / 100}
                decimals={g.cpuPercent >= 1000 ? 0 : g.cpuPercent >= 99.5 ? 1 : 2}
                unit=" cores"
              />
            ) : (
              "idle"
            ),
          label: `${groupName(g)} ${cores(g.cpuPercent)}`,
          title: `${percent(g.cpuPercent)} of one core, summed over its processes`,
        }))}
      />

      <BarReading
        title="Memory"
        label="Memory by workload"
        list="Workloads using the most memory"
        hue={HUE.mem}
        trail
        capacity={memTotal > 0 ? memTotal : memShown}
        rest={Math.max(memUsed - memShown, 0)}
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
        message={unread ?? (byMemory.length === 0 ? "No memory is reported." : undefined)}
        parts={byMemory.map((g) => ({
          ...line(g),
          value: g.memory,
          figure: <LiveBytes value={g.memory} />,
          label: `${groupName(g)} ${bytes(g.memory)}`,
          title:
            `${bytes(g.memory)} resident, pages its processes share counted once` +
            (memTotal > 0 ? ` — ${percent((g.memory / memTotal) * 100)} of the host` : ""),
        }))}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}
