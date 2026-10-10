"use client"

import { useCallback, useState } from "react"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { containerRates } from "@/lib/container-usage"
import type { ContainerDetail, ContainerHistory, ContainerStats } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope, type SocketState } from "@/hooks/use-socket"
import { Section } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { utilisationTone } from "@/components/meter"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { peak, trendOf } from "@/components/docker/container"

/** The container's last two frames of Docker's stats, and whether more are coming. */
export type ContainerFrames = {
  current?: ContainerStats
  previous?: ContainerStats
  connection: SocketState
}

/**
 * One container's stats socket, for the page's readings and its picture. The
 * Usage tab keeps a socket of its own with its pause and its stale handling;
 * this one is open only while the Overview is, so the two never run together.
 */
export function useContainerFrames(containerId: string, enabled: boolean): ContainerFrames {
  const [frames, setFrames] = useState<{ current?: ContainerStats; previous?: ContainerStats }>({})
  const onMessage = useCallback(
    (envelope: Envelope) => {
      if (envelope.type !== "stats" || !envelope.data) return
      const next = envelope.data as ContainerStats
      if (next.id !== containerId) return
      setFrames((held) =>
        // A frame no newer than the last is a repeat, and measuring a rate
        // across it would divide by nothing.
        held.current && Date.parse(next.ts) <= Date.parse(held.current.ts)
          ? held
          : { current: next, previous: held.current },
      )
    },
    [containerId],
  )
  const { state } = useSocket(
    `/docker/containers/${encodeURIComponent(containerId)}/stats/stream`,
    {
      enabled,
      onOpen: () => setFrames({}),
      onMessage,
    },
  )
  return enabled ? { ...frames, connection: state } : { connection: "closed" }
}

/**
 * What the container is using, as the four readings the host Overview opens
 * on: each figure gliding to Docker's frame every second, keyed by its line's
 * colour and carrying its last hour where a meter would be, so "is it busy"
 * and "has it been" are one glance.
 *
 * Memory with a ceiling is drawn against it, because the ceiling is where the
 * kernel kills it; without one it is a share of the host, which is a fact,
 * rather than the host's RAM dressed up as a budget. A stopped container keeps
 * its hour and says what it peaked at.
 */
export function ContainerReadings({
  detail,
  frames,
  onUsage,
}: {
  detail: ContainerDetail
  frames: ContainerFrames
  /** Opens the Usage tab, where the same readings are broken down and charted. */
  onUsage: () => void
}) {
  const running = detail.state === "running"
  const history = usePoll<ContainerHistory>(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(detail.id)}/stats/history`,
        { range: "1h", points: 60 },
        signal,
      ),
    60_000,
    [detail.id],
  )
  const points = history.data?.points
  const cpuLine = trendOf(points, (point) => point.cpu)
  const memLine = trendOf(points, (point) => point.memBytes)
  const netLine = trendOf(points, (point) =>
    point.netRx == null || point.netTx == null ? null : point.netRx + point.netTx,
  )
  const diskLine = trendOf(points, (point) =>
    point.blockRead == null || point.blockWrite == null ? null : point.blockRead + point.blockWrite,
  )

  const { current, previous } = frames
  const rates = current ? containerRates(current, previous) : undefined
  const cpu = running && current?.cpuReady ? current.cpuPercent : undefined
  const cpuLimit = detail.cpuLimit || current?.cpuLimit || 0
  const memLimited = Boolean(current?.memLimited || (detail.memoryLimit ?? 0) > 0)
  const memLimit = detail.memoryLimit || current?.memLimit || 0
  const memShare = current && memLimited && memLimit > 0 ? (current.memUsage / memLimit) * 100 : 0
  const net = rates?.rx != null && rates.tx != null ? rates.rx + rates.tx : undefined
  const disk = rates?.read != null && rates.write != null ? rates.read + rates.write : undefined
  const shown = running ? current : undefined
  const idle = (line: number[], format: (value: number) => string) => {
    const top = peak(line)
    return top === undefined ? "Not running" : `Not running · peaked at ${format(top)}`
  }
  const waiting = <span className="text-muted-foreground">—</span>

  return (
    <Section
      title="Resources"
      actions={
        <div className="flex items-center gap-4">
          {running ? (
            frames.connection === "open" && current ? (
              <Status live tone="running" label="Live" />
            ) : (
              <Status tone="notice" label="Connecting…" />
            )
          ) : (
            <Status tone="stopped" label="Not running" />
          )}
          <button
            type="button"
            onClick={onUsage}
            className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
          >
            Usage and history <ArrowRight className="size-3" />
          </button>
        </div>
      }
    >
      <StatGrid columns={4} dense data-testid="container-readings">
        <StatTile
          label={
            <>
              <SeriesKey color={HUE.cpu} />
              CPU
            </>
          }
          meterLabel="CPU"
          value={cpu === undefined ? waiting : <LiveFigure value={cpu} decimals={1} unit="%" />}
          tone={cpuLimit > 0 && cpu !== undefined ? utilisationTone(cpu / cpuLimit) : "default"}
          trailing={
            cpuLimit > 0 ? `of ${cpuLimit} ${cpuLimit === 1 ? "core" : "cores"}` : "of a core"
          }
          trend={<TileTrend values={cpuLine} label="CPU over the last hour" color={HUE.cpu} />}
          hint={
            !running
              ? idle(cpuLine, (value) => percent(value))
              : shown
                ? `${shown.pids} ${shown.pids === 1 ? "process" : "processes"}${
                    shown.hostCpus ? ` · ${shown.hostCpus} cores on the host` : ""
                  }`
                : "Waiting for Docker"
          }
        />
        <StatTile
          label={
            <>
              <SeriesKey color={HUE.mem} />
              Memory
            </>
          }
          meterLabel="Memory against its limit"
          value={shown ? <LiveBytes value={shown.memUsage} /> : waiting}
          tone={memLimited ? utilisationTone(memShare) : "default"}
          trailing={memLimited ? `of ${bytes(memLimit)}` : shown ? "no limit" : undefined}
          meter={memLimited && shown ? memShare : undefined}
          trend={<TileTrend values={memLine} label="Memory over the last hour" color={HUE.mem} />}
          hint={
            !running
              ? idle(memLine, (value) => bytes(value))
              : shown
                ? `${percent(shown.memHostPercent)} of the host${
                    shown.memCache ? ` · ${bytes(shown.memCache)} cache` : ""
                  }`
                : "Waiting for Docker"
          }
        />
        <StatTile
          label={
            <>
              <SeriesKey color={HUE.net} />
              Network
            </>
          }
          meterLabel="Network"
          value={net === undefined || !running ? waiting : <LiveBytes value={net} suffix="/s" />}
          trend={<TileTrend values={netLine} label="Network over the last hour" color={HUE.net} />}
          hint={
            !running
              ? idle(netLine, (value) => `${bytes(value)}/s`)
              : rates?.rx != null && rates.tx != null
                ? `in ${bytes(rates.rx)}/s · out ${bytes(rates.tx)}/s`
                : detail.networkMode === "host"
                  ? "Shares the host's network"
                  : "Measuring"
          }
        />
        <StatTile
          label={
            <>
              <SeriesKey color={HUE.disk} />
              Disk I/O
            </>
          }
          meterLabel="Disk I/O"
          value={disk === undefined || !running ? waiting : <LiveBytes value={disk} suffix="/s" />}
          trend={
            <TileTrend values={diskLine} label="Disk I/O over the last hour" color={HUE.disk} />
          }
          hint={
            !running
              ? idle(diskLine, (value) => `${bytes(value)}/s`)
              : rates?.read != null && rates.write != null
                ? `read ${bytes(rates.read)}/s · write ${bytes(rates.write)}/s`
                : "Measuring"
          }
        />
      </StatGrid>
    </Section>
  )
}
