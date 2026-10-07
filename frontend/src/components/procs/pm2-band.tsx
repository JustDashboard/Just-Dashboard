"use client"

import { ChartActivity, Filter } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, pm2Product } from "@/components/product-logo"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ShareBar, shade } from "@/components/procs/workloads"
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
 * nothing about which one is crashing — the rows say that, crashing first;
 * and Memory was one number for all of them. The band says the same total
 * and who it is: a cluster summed into one application, its share of the
 * host against everything else running, each span easing to the next poll
 * and each figure gliding to it. A row narrows the table to its application.
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

  // The host's own readings size the bars, as on the Processes page: a core
  // is a hundred points of the processor bar, the memory bar is the host's
  // total, and what the host uses beyond PM2 is the muted span.
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const busy = snapshot?.cpu ? snapshot.cpu.totalPercent * coreCount : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0

  return (
    <div data-slot="pm2-band" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <Panel plain aria-label="Processor by application">
        <PanelHeader
          title="Processor"
          actions={
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
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : cpu}
            rest={busy !== undefined ? Math.max(busy - cpu, 0) : 0}
            parts={byCPU.map((a, rank) => ({
              key: a.key,
              value: a.cpu,
              color: shade(HUE.cpu, rank),
              label: `${a.name} ${cores(a.cpu)}`,
            }))}
            format={cores}
          />
          {byCPU.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {apps.some((a) => a.online > 0)
                ? "Every application is idle."
                : "Nothing PM2 runs is online."}
            </p>
          ) : (
            <AppRows
              apps={byCPU}
              color={HUE.cpu}
              selected={selected}
              onSelect={onSelect}
              figure={(a) =>
                a.cpu / 100 >= 0.005 ? (
                  <LiveFigure value={a.cpu / 100} decimals={a.cpu >= 99.5 ? 1 : 2} unit=" cores" />
                ) : (
                  "idle"
                )
              }
              title={(a) => `${percent(a.cpu)} of one core, summed over its instances`}
            />
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by application">
        <PanelHeader
          title="Memory"
          actions={
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              <span className="font-medium text-foreground">
                <LiveBytes value={memory} />
              </span>
              {memTotal > 0 && `of ${bytes(memTotal, 0)}`}
            </span>
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : memory}
            rest={Math.max(memUsed - memory, 0)}
            parts={byMemory.map((a, rank) => ({
              key: a.key,
              value: a.memory,
              color: shade(HUE.mem, rank),
              label: `${a.name} ${bytes(a.memory)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">Nothing PM2 runs is online.</p>
          ) : (
            <AppRows
              apps={byMemory}
              color={HUE.mem}
              selected={selected}
              onSelect={onSelect}
              figure={(a) => <LiveBytes value={a.memory} />}
              title={(a) =>
                `${bytes(a.memory)} resident across its instances` +
                (memTotal > 0 ? ` — ${percent((a.memory / memTotal) * 100)} of the host` : "")
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
 * The named spans of a bar, one line each: the key that finds it on the bar,
 * what runs it, its instances, and its figure. A cluster with an instance
 * down says so in amber, because "api ×4" at three-quarters of its usual
 * share is otherwise a figure that merely looks low.
 */
function AppRows({
  apps,
  color,
  selected,
  onSelect,
  figure,
  title,
}: {
  apps: PM2App[]
  color: string
  selected: string
  onSelect: (key: string) => void
  figure: (app: PM2App) => React.ReactNode
  title: (app: PM2App) => string
}) {
  return (
    <ul className="-mx-2">
      {apps.map((app, rank) => {
        const product = pm2Product(app.interpreter)
        const pressed = selected === app.key
        const count = app.instances.length
        return (
          <li key={app.key}>
            <button
              type="button"
              aria-pressed={pressed}
              aria-label={`Only ${app.name}`}
              title={title(app)}
              onClick={() => onSelect(pressed ? "" : app.key)}
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
                  <ChartActivity aria-hidden className="size-3.5 text-muted-foreground" />
                )}
              </span>
              <span className="min-w-0 truncate font-medium">{app.name}</span>
              <span
                className={cn(
                  "numeric shrink-0 truncate text-hint",
                  app.online < count ? "text-warning" : "text-muted-foreground",
                )}
              >
                {count > 1
                  ? app.online < count
                    ? `${app.online} of ${count} online`
                    : `×${count}`
                  : `#${app.instances[0].id}`}
              </span>
              <span className="numeric ml-auto shrink-0 font-medium text-foreground">
                {figure(app)}
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
