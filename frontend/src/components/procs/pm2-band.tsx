"use client"

import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { ChartActivity } from "@/components/icons"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { ProductGlyph, pm2Product } from "@/components/product-logo"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cores } from "@/components/procs/shared"
import type { PM2App } from "@/components/procs/pm2-shared"

/** How many applications each half names; the rest of the machine is one muted span. */
const SHOWN = 5

/**
 * What PM2's applications take of the machine: the five heaviest by
 * processor and by memory, each a span of one bar as wide as the host, the
 * Processes page's band drawn for PM2 alone.
 *
 * The four tiles this replaced each said less. Online and Not running were
 * the state chips' counts, and the chips also narrow the table; Restarts was
 * a sum of every application's counter since PM2 last reset it, which says
 * nothing about which one is crashing — the table says that, crashing first;
 * and Memory was one number for all of them. The band says the same total
 * and who it is: a cluster summed into one application, its share of the
 * host against everything else running. A cluster with an instance down says
 * so in amber on its line, because "api ×4" at three-quarters of its usual
 * share is otherwise a figure that merely looks low.
 *
 * PM2's list is polled, so neither bar is marked live. A press narrows the
 * table to its application.
 */
export function PM2Band({
  apps,
  snapshot,
  selected,
  onSelect,
}: {
  apps: PM2App[]
  snapshot?: Snapshot
  selected: string
  onSelect: (key: string) => void
}) {
  const byCPU = [...apps]
    .filter((a) => a.cpu > 0)
    .sort((a, b) => b.cpu - a.cpu)
    .slice(0, SHOWN)
  const byMemory = [...apps]
    .filter((a) => a.memory > 0)
    .sort((a, b) => b.memory - a.memory)
    .slice(0, SHOWN)
  const cpu = sum(apps, (a) => a.cpu)
  const memory = sum(apps, (a) => a.memory)
  const cpuShown = sum(byCPU, (a) => a.cpu)
  const memShown = sum(byMemory, (a) => a.memory)

  // The host's own readings size the bars, as on the Processes page: a core
  // is a hundred points of the processor bar, the memory bar is the host's
  // total, and what the host uses beyond these five is the muted span.
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? snapshot.cpu.totalPercent * coreCount : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0

  const line = (app: PM2App): Pick<ReadingPart, "key" | "mark" | "name" | "detail" | "press"> => {
    const product = pm2Product(app.interpreter)
    const pressed = selected === app.key
    const count = app.instances.length
    return {
      key: app.key,
      mark: product ? (
        <ProductGlyph id={product} className="size-3.5" />
      ) : (
        <ChartActivity aria-hidden className="size-3.5 text-muted-foreground" />
      ),
      name: app.name,
      detail:
        count > 1 ? (
          app.online < count ? (
            <span className="text-warning">
              {app.online} of {count} online
            </span>
          ) : (
            `×${count}`
          )
        ) : (
          `#${app.instances[0].id}`
        ),
      press: {
        label: `Only ${app.name}`,
        pressed,
        onPress: () => onSelect(pressed ? "" : app.key),
      },
    }
  }

  return (
    <div data-slot="pm2-band" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <BarReading
        title="Processor"
        label="Processor by application"
        list="Busiest applications"
        hue={HUE.cpu}
        trail
        capacity={coreCount > 0 ? coreCount * 100 : cpuShown}
        rest={busy !== undefined ? Math.max(busy - cpuShown, 0) : 0}
        format={cores}
        figure={
          <span
            className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground"
            title={`${percent(cpu)} of one core, summed over every PM2 process`}
          >
            <span className="font-medium text-foreground">
              <LiveFigure value={cpu / 100} decimals={2} />
            </span>
            {coreCount > 0 ? `of ${plural(coreCount, "core")}` : "cores"}
          </span>
        }
        message={
          byCPU.length > 0
            ? undefined
            : apps.some((a) => a.online > 0)
              ? "Every application is idle."
              : "Nothing PM2 runs is online."
        }
        parts={byCPU.map((a) => ({
          ...line(a),
          value: a.cpu,
          figure:
            a.cpu / 100 >= 0.005 ? (
              <LiveFigure value={a.cpu / 100} decimals={a.cpu >= 99.5 ? 1 : 2} unit=" cores" />
            ) : (
              "idle"
            ),
          label: `${a.name} ${cores(a.cpu)}`,
          title: `${percent(a.cpu)} of one core, summed over its instances`,
        }))}
      />

      <BarReading
        title="Memory"
        label="Memory by application"
        list="Applications using the most memory"
        hue={HUE.mem}
        trail
        capacity={memTotal > 0 ? memTotal : memShown}
        rest={Math.max(memUsed - memShown, 0)}
        free="Free"
        format={(v) => bytes(v)}
        figure={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              <LiveBytes value={memory} />
            </span>
            {memTotal > 0 && `of ${bytes(memTotal, 0)}`}
          </span>
        }
        message={byMemory.length === 0 ? "Nothing PM2 runs is online." : undefined}
        parts={byMemory.map((a) => ({
          ...line(a),
          value: a.memory,
          figure: <LiveBytes value={a.memory} />,
          label: `${a.name} ${bytes(a.memory)}`,
          title:
            `${bytes(a.memory)} resident across its instances` +
            (memTotal > 0 ? ` — ${percent((a.memory / memTotal) * 100)} of the host` : ""),
        }))}
      />
    </div>
  )
}

function sum<T>(list: T[], value: (item: T) => number) {
  return list.reduce((total, item) => total + value(item), 0)
}
