"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { get } from "@/lib/api"
import { containerRates, containerRateLabel, CONTAINER_STALE_MS } from "@/lib/container-usage"
import { bytes, percent, timestamp } from "@/lib/format"
import type { ContainerDetail, ContainerStats } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useSocket } from "@/hooks/use-socket"
import { Detail, DetailList, Section } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { HUE, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const readingRate = containerRateLabel
const count = (value: number | null | undefined) => (value == null ? "—" : value.toLocaleString())
/** A rate that glides to each frame; the label's own rules decide when there is none to draw. */
const liveRate = (value: number | null | undefined) =>
  readingRate(value) === "—" || value == null || value < 1 ? (
    readingRate(value)
  ) : (
    <LiveBytes value={value} suffix="/s" />
  )

/** Live readings are separate from recorded charts: their timestamps and cadences differ. */
export function ContainerLiveUsage({ detail }: { detail: ContainerDetail }) {
  const [paused, setPaused] = useState(false)
  const [now, setNow] = useState(0)
  const [error, setError] = useState<string>()
  const [sample, setSample] = useState<{
    current: ContainerStats
    previous?: ContainerStats
    receivedAt: number
  }>()
  const previous = useRef<ContainerStats>(undefined)
  // Starting/stopping outside this page must also start/stop its stream.
  const observed = usePoll<ContainerDetail>(
    (signal) => get(`/docker/containers/${encodeURIComponent(detail.id)}`, undefined, signal),
    10_000,
    [detail.id],
  )
  const container = observed.data ?? detail
  const running = container.state === "running"
  const { state } = useSocket(`/docker/containers/${encodeURIComponent(detail.id)}/stats/stream`, {
    enabled: running && !paused,
    onOpen: () => {
      previous.current = undefined
      setSample(undefined)
      setError(undefined)
    },
    onClose: () => {
      previous.current = undefined
    },
    onMessage: (message) => {
      if (message.type === "error") {
        setError(message.error ?? "Docker could not provide usage readings.")
        previous.current = undefined
        return
      }
      if (message.type !== "stats" || !message.data) return
      const next = message.data as ContainerStats
      const ts = Date.parse(next.ts)
      if (next.id !== detail.id || !Number.isFinite(ts) || ts <= 0) return
      if (previous.current && ts <= Date.parse(previous.current.ts)) return
      setSample({ current: next, previous: previous.current, receivedAt: Date.now() })
      previous.current = next
      setError(undefined)
    },
  })
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])

  const stale = sample !== undefined && now - sample.receivedAt > CONTAINER_STALE_MS
  const fresh = running && !paused && state === "open" && !stale && !error && !!sample
  const current = fresh || (paused && running) ? sample?.current : undefined
  const rates = current ? containerRates(current, sample?.previous) : undefined
  const cpu = current?.cpuReady ? current.cpuPercent : undefined
  const label = !running
    ? `Container ${container.state}`
    : paused
      ? "Paused"
      : error
        ? "Usage unavailable"
        : stale
          ? "Readings stale"
          : fresh
            ? "Live"
            : state === "open"
              ? "Waiting for Docker"
              : "Connecting"
  const networkMode = container.networkMode
  const sharedNetwork = networkMode.startsWith("container:")

  return (
    <div className="space-y-6" data-testid="container-live-usage">
      <Section
        title="Current usage"
        actions={
          <div className="flex flex-wrap items-center gap-3">
            <Status tone={fresh ? "running" : "notice"} live={fresh} label={label} />
            {running && (
              <Button size="xs" variant="outline" onClick={() => setPaused((value) => !value)}>
                {paused ? "Resume readings" : "Pause readings"}
              </Button>
            )}
          </div>
        }
      >
        {error && (
          <p role="status" className="text-xs text-muted-foreground">
            {error}
          </p>
        )}
        {!running && (
          <p className="text-xs text-muted-foreground">
            Live readings resume when the container runs. Recorded history remains below.
          </p>
        )}
        <StatGrid columns={4} dense>
          {/* Keyed by the colour each measurement's chart draws below, and
              gliding to every frame, as the Overview tab's readings do. */}
          <StatTile
            label={
              <>
                <SeriesKey color={HUE.cpu} />
                CPU
              </>
            }
            meterLabel="CPU"
            value={cpu === undefined ? "—" : <LiveFigure value={cpu} decimals={1} unit="%" />}
            hint={
              cpu === undefined
                ? "Waiting for a CPU interval"
                : `${(cpu / 100).toFixed(2)} cores in use · 100% = 1 core`
            }
          />
          <StatTile
            label={
              <>
                <SeriesKey color={HUE.mem} />
                Memory working set
              </>
            }
            meterLabel="Memory working set"
            value={current ? <LiveBytes value={current.memUsage} /> : "—"}
            hint={
              current
                ? current.memLimited
                  ? `${percent(current.memPercent)} of ${bytes(current.memLimit)} limit`
                  : "No container memory limit"
                : "Waiting for a reading"
            }
            meter={current?.memLimited ? current.memPercent : undefined}
          />
          <StatTile
            label={
              <>
                <SeriesKey color="var(--chart-2)" />
                Network received
              </>
            }
            meterLabel="Network received"
            value={liveRate(rates?.rx)}
            hint={
              !current
                ? "Waiting for a reading"
                : current.networkAvailable
                  ? `${bytes(current.netRx)} total received`
                  : "No interface counters reported"
            }
          />
          <StatTile
            label={
              <>
                <SeriesKey color="var(--chart-5)" />
                Network sent
              </>
            }
            meterLabel="Network sent"
            value={liveRate(rates?.tx)}
            hint={
              !current
                ? "Waiting for a reading"
                : current.networkAvailable
                  ? `${bytes(current.netTx)} total sent`
                  : "No interface counters reported"
            }
          />
        </StatGrid>
        {sample && (
          <p className="text-hint text-muted-foreground">
            Docker sample: {timestamp(sample.current.ts)}. Rates use elapsed time between samples;
            totals reset with the container or its interfaces.
          </p>
        )}
      </Section>

      <div className="grid gap-6 lg:grid-cols-3 [&>*]:min-w-0">
        <Section title="Processor & tasks">
          <DetailList>
            <Detail label="CPU quota">
              {current ? (current.cpuLimit ? `${current.cpuLimit} cores` : "No quota") : "—"}
            </Detail>
            <Detail label="Host CPU share">
              {cpu !== undefined && current?.hostCpus ? percent(cpu / current.hostCpus) : "—"}
            </Detail>
            <Detail label="Throttled periods">{percent(rates?.throttledPercent)}</Detail>
            <Detail label="Processes & threads">{count(current?.pids)}</Detail>
            <Detail label="Task limit">
              {current ? (current.pidsLimit ? count(current.pidsLimit) : "No limit") : "—"}
            </Detail>
          </DetailList>
          <p className="text-hint text-muted-foreground">
            Throttled periods are the share of CPU scheduling periods that hit the quota, not a
            share of CPU time.
          </p>
        </Section>
        <Section title="Memory breakdown">
          <DetailList>
            <Detail label="Including cache">{bytes(current?.memRaw)}</Detail>
            <Detail label="Inactive file cache">{bytes(current?.memCache)}</Detail>
            <Detail label="Working set">{bytes(current?.memUsage)}</Detail>
            <Detail label="Anonymous memory">{bytes(current?.memRss)}</Detail>
            <Detail label="Swap">{bytes(current?.memSwap)}</Detail>
            <Detail label="Host memory share">{percent(current?.memHostPercent)}</Detail>
          </DetailList>
          <p className="text-hint text-muted-foreground">
            Working set excludes inactive file cache, matching Docker stats. An em dash means Docker
            did not report a measurement.
          </p>
        </Section>
        <Section title="Block I/O">
          <DetailList>
            <Detail label="Read speed">{readingRate(rates?.read)}</Detail>
            <Detail label="Write speed">{readingRate(rates?.write)}</Detail>
            <Detail label="Total read">
              {bytes(current?.blockAvailable ? current.blockRead : undefined)}
            </Detail>
            <Detail label="Total written">
              {bytes(current?.blockAvailable ? current.blockWrite : undefined)}
            </Detail>
          </DetailList>
          <p className="text-hint text-muted-foreground">
            Block device traffic reported by Docker. Cached file operations and filesystem space
            used are different measurements.
          </p>
        </Section>
      </div>

      <Section title="Network interfaces">
        {sharedNetwork && (
          <p className="text-xs text-muted-foreground">
            This container shares another container’s network namespace. These counters cover that
            shared namespace, not this process alone.
          </p>
        )}
        {rates && rates.interfaces.length > 0 ? (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Interface</TableHead>
                  <TableHead>Receive / send</TableHead>
                  <TableHead>Total received / sent</TableHead>
                  <TableHead>Packets/s in / out</TableHead>
                  <TableHead>Errors in / out</TableHead>
                  <TableHead>Dropped in / out</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rates.interfaces.map((entry) => (
                  <TableRow key={entry.name}>
                    <TableCell className="font-mono">{entry.name}</TableCell>
                    <TableCell className="numeric whitespace-nowrap">
                      {readingRate(entry.rx)} / {readingRate(entry.tx)}
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap">
                      {bytes(entry.rxBytes)} / {bytes(entry.txBytes)}
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap">
                      {entry.rxPacketsRate?.toFixed(1) ?? "—"} /{" "}
                      {entry.txPacketsRate?.toFixed(1) ?? "—"}
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap">
                      {count(entry.rxErrors)} / {count(entry.txErrors)}
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap">
                      {count(entry.rxDropped)} / {count(entry.txDropped)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="text-hint text-muted-foreground">
              Traffic includes local container and LAN traffic as well as internet traffic. Errors
              and drops are cumulative interface counters.
            </p>
          </>
        ) : (
          <p className="text-xs text-muted-foreground">
            {networkMode === "host" ? (
              <>
                Host networking has no isolated container interface to measure.{" "}
                <Link className="text-brand hover:underline" href="/metrics">
                  View host network usage
                </Link>
                .
              </>
            ) : networkMode === "none" ? (
              "Networking is disabled for this container."
            ) : !current ? (
              "Waiting for current interface readings."
            ) : (
              "Docker did not report network interface counters for this container."
            )}
          </p>
        )}
      </Section>
    </div>
  )
}
