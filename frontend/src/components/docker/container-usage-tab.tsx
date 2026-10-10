"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { Pencil } from "@/components/icons"
import { get, patch, ApiError } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { notify } from "@/lib/toast"
import { containerRateLabel, containerRates } from "@/lib/container-usage"
import type { ContainerDetail, ContainerStats, DockerEventFeed } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useContainerFrame } from "@/hooks/use-container-live"
import { Field } from "@/components/form"
import { Meter, utilisationTone } from "@/components/meter"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote } from "@/components/state"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TrafficBar } from "@/components/metrics/hardware-panels"
import { SeriesKey } from "@/components/overview/readings"
import { RuntimeUsage } from "@/components/deploy/runtime-usage"
import { Hint, Term } from "@/components/docker/explain"
import { usageMarkers } from "@/components/docker/usage-markers"

/** The charts' own hues (`deploy/runtime-usage.tsx`), so a breakdown reads as its chart. */
const MEMORY = "var(--chart-4)"
const RECEIVED = "var(--chart-2)"
const SENT = "var(--chart-5)"

type Rates = ReturnType<typeof containerRates>

/**
 * What the container is using, as a project's Runtime page reads a release:
 * each chart headed by its reading now, the Live range off Docker's stats
 * socket and the recorded ranges behind it, the processes, and what it has
 * done since it started (`RuntimeUsage`) — with the container's own exits,
 * kills and restart loops marked where they fell on the charts.
 *
 * Under them, what the figures in those heads are made of, the way the
 * Metrics page breaks the host down: where the memory is, how much of its
 * processor quota and of the machine it takes and whether the quota is
 * biting, what it may use and the way to change it, and each interface's
 * traffic as a bar against the busiest.
 *
 * It was a row of four grey tiles over three columns of label-and-value
 * pairs, an interface table of slashed pairs, a fenced "Change limits" strip
 * and four charts at the foot, so a figure and the line it moves on were a
 * screen apart and nothing said what any of it was a share of.
 */
export function ContainerUsageTab({
  detail,
  onLimitsSaved,
}: {
  detail: ContainerDetail
  onLimitsSaved: () => void
}) {
  const running = detail.state === "running"
  const feed = usePoll<DockerEventFeed>(
    (signal) =>
      get<DockerEventFeed>("/docker/events", { container: detail.id, limit: 200 }, signal),
    30_000,
    [detail.id],
  )
  const markers = useMemo(() => usageMarkers(feed.data?.events ?? []), [feed.data?.events])

  return (
    <div className="flex min-w-0 flex-col gap-8" data-testid="container-usage">
      <RuntimeUsage containerId={detail.id} name={detail.name} events={markers} running={running} />
      <Breakdown detail={detail} running={running} onLimitsSaved={onLimitsSaved} />
    </div>
  )
}

/**
 * The readings broken down. They read the frame the charts' socket already
 * holds (`useContainerFrame`) rather than opening a second one, and keep the
 * frame before it, because throttling and an interface's rate are measured
 * between two.
 */
function Breakdown({
  detail,
  running,
  onLimitsSaved,
}: {
  detail: ContainerDetail
  running: boolean
  onLimitsSaved: () => void
}) {
  const frame = useContainerFrame(detail.id)
  const [pair, setPair] = useState<{ current?: ContainerStats; previous?: ContainerStats }>({})
  if (frame.stats !== pair.current) setPair({ current: frame.stats, previous: pair.current })
  // Not running, a frame in the store is an earlier visit's.
  const stats = running ? pair.current : undefined
  const rates = stats ? containerRates(stats, pair.previous) : undefined

  return (
    <Section title="Breakdown">
      <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <MemoryAllocation stats={stats} running={running} />
        <Processor stats={stats} rates={rates} running={running} />
      </div>
      <Limits detail={detail} stats={stats} onSaved={onLimitsSaved} />
      <Interfaces detail={detail} stats={stats} rates={rates} running={running} />
    </Section>
  )
}

function waiting(running: boolean) {
  return running
    ? "Waiting for Docker's first reading."
    : "Not running. These are read while it runs."
}

/**
 * Where the memory is, as one bar: what its programs hold, the files they
 * are working with, the cache the kernel can drop, and what is left — of its
 * limit where it has one, of the machine where it has none. The first two are
 * the working set Docker's own stats report; the third is why a container's
 * memory "used" and its working set disagree.
 */
function MemoryAllocation({ stats, running }: { stats?: ContainerStats; running: boolean }) {
  if (!stats) {
    return (
      <Panel plain>
        <PanelHeader title="Allocation" />
        <EmptyNote className="pt-3">{waiting(running)}</EmptyNote>
      </Panel>
    )
  }
  const raw = stats.memRaw ?? stats.memUsage + (stats.memCache ?? 0)
  const programs = Math.min(stats.memRss ?? stats.memUsage, stats.memUsage)
  const active = Math.max(stats.memUsage - programs, 0)
  const reclaimable = Math.max(stats.memCache ?? raw - stats.memUsage, 0)
  // Docker reports the machine as the limit of a container that has none;
  // its share of the host is the honest denominator there.
  const host = stats.memHostPercent ? stats.memUsage / (stats.memHostPercent / 100) : 0
  const total = Math.max(stats.memLimited ? stats.memLimit : host, raw, 1)
  const parts = [
    { key: "programs", label: "Programs", value: programs, color: MEMORY },
    {
      key: "active",
      label: "Active files",
      value: active,
      color: `color-mix(in oklab, ${MEMORY} 55%, transparent)`,
    },
    {
      key: "cache",
      label: "Reclaimable",
      value: reclaimable,
      color: `color-mix(in oklab, ${MEMORY} 25%, transparent)`,
    },
    {
      key: "free",
      label: stats.memLimited ? "Left to its limit" : "Free on the host",
      value: Math.max(total - raw, 0),
      color: "var(--meter-track)",
    },
  ]
  const tone: Tone = stats.memLimited ? utilisationTone(stats.memPercent) : "default"

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Allocation"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            <span
              className={cn(
                "font-medium text-foreground",
                tone === "warning" && "text-warning",
                tone === "danger" && "text-destructive",
              )}
            >
              {bytes(stats.memUsage)}
            </span>{" "}
            working set ·{" "}
            {stats.memLimited
              ? `${bytes(stats.memLimit)} limit`
              : `no limit · ${percent(stats.memHostPercent)} of the host`}
          </span>
        }
      />
      <PanelBody className="flex flex-col gap-5 pt-4">
        <div
          role="img"
          aria-label={parts.map((part) => `${part.label} ${bytes(part.value)}`).join(", ")}
          className="flex h-4 w-full overflow-hidden rounded-md bg-meter-track"
        >
          {parts.slice(0, 3).map((part) => (
            <span
              key={part.key}
              className="h-full transition-[width] duration-700 ease-out"
              style={{ width: `${(part.value / total) * 100}%`, background: part.color }}
            />
          ))}
        </div>
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          {parts.map((part) => (
            <div key={part.key} className="min-w-0 space-y-0.5">
              <dt className="eyebrow truncate">
                <SeriesKey color={part.color} />
                {part.label}
              </dt>
              <dd className="numeric text-title font-semibold tracking-tight">
                {bytes(part.value)}
              </dd>
              <dd className="numeric text-hint text-muted-foreground">
                {percent((part.value / total) * 100, 0)}
              </dd>
            </div>
          ))}
        </dl>
        <div className="flex min-w-0 items-baseline justify-between gap-3 border-t border-hairline pt-3 text-hint">
          <span className="text-muted-foreground">Swap</span>
          <span className="numeric font-medium">
            {stats.memSwap == null ? "not reported" : bytes(stats.memSwap)}
          </span>
        </div>
        <Hint>
          The working set — programs and active files — excludes the inactive file cache, matching
          Docker stats. The kernel reclaims that cache under pressure before anything is killed.
        </Hint>
      </PanelBody>
    </Panel>
  )
}

/**
 * How much processor it takes, of what it may have and of the machine, and
 * whether its quota is biting: a container at its quota is not slow because
 * the host is busy, and the throttled share is what says which.
 */
function Processor({
  stats,
  rates,
  running,
}: {
  stats?: ContainerStats
  rates?: Rates
  running: boolean
}) {
  if (!stats) {
    return (
      <Panel plain>
        <PanelHeader title="Quota and share" />
        <EmptyNote className="pt-3">{waiting(running)}</EmptyNote>
      </Panel>
    )
  }
  const cpu = stats.cpuReady === false ? undefined : stats.cpuPercent
  const quota = stats.cpuLimit ?? 0
  const ofQuota = cpu !== undefined && quota > 0 ? cpu / quota : undefined
  const ofHost = cpu !== undefined && stats.hostCpus ? cpu / stats.hostCpus : undefined
  const throttled = quota > 0 ? rates?.throttledPercent : undefined
  const pidsShare = stats.pidsLimit ? (stats.pids / stats.pidsLimit) * 100 : undefined
  const waitingFigure = <span className="text-muted-foreground">—</span>

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Quota and share"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              {cpu === undefined ? "—" : `${(cpu / 100).toFixed(2)}`}
            </span>{" "}
            cores in use · 100% is one core
          </span>
        }
      />
      <StatGrid columns={2} dense className="-mx-5">
        <StatTile
          label="Of its quota"
          value={
            quota > 0 ? (ofQuota === undefined ? waitingFigure : percent(ofQuota)) : "No quota"
          }
          tone={ofQuota === undefined ? "default" : utilisationTone(ofQuota)}
          meter={ofQuota}
          hint={
            quota > 0
              ? `${quota} ${quota === 1 ? "core" : "cores"} allowed`
              : "Competes with everything on this server"
          }
        />
        <StatTile
          label="Of the machine"
          value={ofHost === undefined ? waitingFigure : percent(ofHost)}
          meter={ofHost}
          hint={stats.hostCpus ? `${stats.hostCpus} cores on this server` : undefined}
        />
        <StatTile
          label="Throttled"
          meterLabel="Throttled periods"
          value={throttled == null ? waitingFigure : percent(throttled)}
          tone={
            throttled == null
              ? "default"
              : throttled >= 25
                ? "danger"
                : throttled >= 5
                  ? "warning"
                  : "default"
          }
          meter={throttled ?? undefined}
          hint={quota === 0 ? "No quota to hit" : "Of scheduling periods that hit the quota"}
        />
        <StatTile
          label="Processes"
          value={stats.pids.toLocaleString()}
          tone={pidsShare === undefined ? "default" : utilisationTone(pidsShare)}
          meter={pidsShare}
          hint={
            stats.pidsLimit
              ? `of a ${stats.pidsLimit.toLocaleString()} task limit`
              : "No task limit"
          }
        />
      </StatGrid>
      <Hint className="pt-3">
        Throttled periods are the share of CPU scheduling periods that hit the quota, not a share of
        CPU time.
      </Hint>
    </Panel>
  )
}

/**
 * What it may use, each limit beside the figure it limits, and the way to
 * change it — the one thing about a container Docker lets change without
 * replacing it, applied to the running cgroup at once. A missing memory limit
 * is amber, because its absence is the answer: the kernel then chooses what
 * to kill across the whole server.
 */
function Limits({
  detail,
  stats,
  onSaved,
}: {
  detail: ContainerDetail
  stats?: ContainerStats
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [open, setOpen] = useState(false)
  const [memory, setMemory] = useState("")
  const [cpus, setCpus] = useState("")
  const [busy, setBusy] = useState(false)
  const memoryLimit = detail.memoryLimit ?? 0
  const cpuLimit = detail.cpuLimit ?? 0
  const control = can("service.control")
  const cpu = stats?.cpuReady === false ? undefined : stats?.cpuPercent

  const save = async () => {
    setBusy(true)
    try {
      const res = await patch<{ warnings: string[] }>(
        `/docker/containers/${detail.id}/resources`,
        {
          memoryMb: Number(memory) || undefined,
          cpus: Number(cpus) || undefined,
        },
        {},
      )
      if (res.warnings?.length) {
        notify.warning("Applied, with a caveat", { description: res.warnings[0] })
      } else {
        notify.success("Limits updated")
      }
      setOpen(false)
      setMemory("")
      setCpus("")
      onSaved()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error("Could not change the limits", message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title={<Term name="memoryLimit">Limits</Term>}
        actions={
          control &&
          !open && (
            <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
              <Pencil className="size-3" />
              Change limits
            </Button>
          )
        }
      />
      <PanelBody className="space-y-4 pt-4">
        <div className="grid gap-x-10 gap-y-5 sm:grid-cols-2">
          <div className="min-w-0 space-y-1.5">
            <p className="eyebrow">Memory</p>
            <p
              className={cn(
                "numeric text-2xl leading-tight font-semibold tracking-tight",
                memoryLimit === 0 && "text-warning",
              )}
            >
              {memoryLimit > 0 ? bytes(memoryLimit) : "No limit"}
            </p>
            {stats && memoryLimit > 0 && (
              <Meter
                value={(stats.memUsage / memoryLimit) * 100}
                tone={utilisationTone((stats.memUsage / memoryLimit) * 100)}
                size="thin"
                label="Memory against its limit"
              />
            )}
            <p className="text-hint text-muted-foreground">
              {stats ? `${bytes(stats.memUsage)} in use now` : "Read while it runs"}
            </p>
            {open && (
              <Field label="New limit in MB" htmlFor="limit-memory" className="pt-2">
                <Input
                  id="limit-memory"
                  type="number"
                  value={memory}
                  placeholder={
                    memoryLimit > 0 ? String(Math.round(memoryLimit / 2 ** 20)) : "unlimited"
                  }
                  onChange={(e) => setMemory(e.target.value)}
                  className="h-8 w-40 text-xs"
                />
              </Field>
            )}
          </div>
          <div className="min-w-0 space-y-1.5">
            <p className="eyebrow">Processor</p>
            <p className="numeric text-2xl leading-tight font-semibold tracking-tight">
              {cpuLimit > 0 ? `${cpuLimit} ${cpuLimit === 1 ? "core" : "cores"}` : "No quota"}
            </p>
            {cpu !== undefined && cpuLimit > 0 && (
              <Meter
                value={cpu / cpuLimit}
                tone={utilisationTone(cpu / cpuLimit)}
                size="thin"
                label="Processor against its quota"
              />
            )}
            <p className="text-hint text-muted-foreground">
              {cpu !== undefined
                ? `${(cpu / 100).toFixed(2)} cores in use now`
                : "Read while it runs"}
            </p>
            {open && (
              <Field label="New quota in cores" htmlFor="limit-cpus" className="pt-2">
                <Input
                  id="limit-cpus"
                  type="number"
                  step="0.5"
                  value={cpus}
                  placeholder={cpuLimit > 0 ? String(cpuLimit) : "unlimited"}
                  onChange={(e) => setCpus(e.target.value)}
                  className="h-8 w-40 text-xs"
                />
              </Field>
            )}
          </div>
        </div>
        {open && (
          <div className="space-y-3 border-t border-hairline pt-4">
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" onClick={save} pending={busy}>
                Apply
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
                Cancel
              </Button>
            </div>
            {detail.composeStack && (
              <p className="text-hint text-muted-foreground">
                This changes the live container.{" "}
                <Link
                  className="underline"
                  href={`/docker/stacks/${encodeURIComponent(detail.composeStack)}?tab=compose&remedy=nomemorylimit`}
                >
                  Update the owning Compose service
                </Link>{" "}
                too, so the limit survives deployment.
              </p>
            )}
            <Hint>
              Leaving a field empty means no change. Docker applies a new limit to the running
              container; nothing is replaced. A memory limit is what makes the kernel kill this
              container rather than choosing a victim across the whole server; the trade is that it
              will be killed when it exceeds it.
            </Hint>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Each interface's traffic now, as the Metrics page draws the host's: in and
 * out as one bar against the busiest, in the network chart's two colours, so
 * which interface carries the traffic is seen before a figure is read.
 */
function Interfaces({
  detail,
  stats,
  rates,
  running,
}: {
  detail: ContainerDetail
  stats?: ContainerStats
  rates?: Rates
  running: boolean
}) {
  const mode = detail.networkMode
  const rows = rates?.interfaces ?? []
  const busiest = Math.max(1, ...rows.map((row) => (row.rx ?? 0) + (row.tx ?? 0)))
  const count = (value: number | null | undefined) =>
    value == null ? "—" : Math.round(value).toLocaleString()

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Panel className="animate-rise">
        <PanelHeader
          title="Interfaces"
          actions={
            rows.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">{rows.length}</span>
            )
          }
        />
        {rows.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">Interface</TableHead>
                <TableHead className="w-[24%]">Traffic</TableHead>
                <TableHead className="text-right">In</TableHead>
                <TableHead className="text-right">Out</TableHead>
                <TableHead className="text-right">Received / sent</TableHead>
                <TableHead className="text-right">Packets/s</TableHead>
                <TableHead className="text-right">Errors</TableHead>
                <TableHead className="pr-5 text-right">Drops</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => {
                const errors = row.rxErrors + row.txErrors
                const drops = row.rxDropped + row.txDropped
                return (
                  <TableRow key={row.name}>
                    <TableCell className="py-2.5 pl-5 font-mono">{row.name}</TableCell>
                    <TableCell className="py-2.5">
                      <TrafficBar
                        iface={{ recvRate: row.rx ?? 0, sendRate: row.tx ?? 0 }}
                        busiest={busiest}
                        colors={{ in: RECEIVED, out: SENT }}
                      />
                    </TableCell>
                    <TableCell className="numeric py-2.5 text-right font-mono">
                      {containerRateLabel(row.rx)}
                    </TableCell>
                    <TableCell className="numeric py-2.5 text-right font-mono">
                      {containerRateLabel(row.tx)}
                    </TableCell>
                    <TableCell className="numeric py-2.5 text-right font-mono text-muted-foreground">
                      {bytes(row.rxBytes)} / {bytes(row.txBytes)}
                    </TableCell>
                    <TableCell className="numeric py-2.5 text-right font-mono text-muted-foreground">
                      {row.rxPacketsRate == null ? "—" : row.rxPacketsRate.toFixed(1)} /{" "}
                      {row.txPacketsRate == null ? "—" : row.txPacketsRate.toFixed(1)}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "numeric py-2.5 text-right font-mono",
                        errors > 0 ? "text-warning" : "text-muted-foreground",
                      )}
                    >
                      {count(errors)}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "numeric py-2.5 pr-5 text-right font-mono",
                        drops > 0 ? "text-warning" : "text-muted-foreground",
                      )}
                    >
                      {count(drops)}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        ) : (
          <PanelBody>
            <p className="text-xs text-muted-foreground">
              {mode === "host" ? (
                <>
                  Host networking has no isolated container interface to measure.{" "}
                  <Link className="text-brand hover:underline" href="/metrics">
                    View host network usage
                  </Link>
                  .
                </>
              ) : mode === "none" ? (
                "Networking is disabled for this container."
              ) : !running ? (
                "Not running. Its interfaces exist only while it runs."
              ) : !stats ? (
                "Waiting for current interface readings."
              ) : (
                "Docker did not report network interface counters for this container."
              )}
            </p>
          </PanelBody>
        )}
      </Panel>
      {mode.startsWith("container:") && (
        <Hint>
          This container shares another container&rsquo;s network namespace. These counters cover
          that shared namespace, not this process alone.
        </Hint>
      )}
      {rows.length > 0 && (
        <Hint>
          Traffic includes local container and LAN traffic as well as internet traffic. Rates are
          measured between Docker&rsquo;s own timestamps; errors and drops are cumulative interface
          counters.
        </Hint>
      )}
    </div>
  )
}
