"use client"

import { Clock, ClockRewind, Cpu, Layers, Servers } from "@/components/icons"
import { WireMark } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { plural } from "@/lib/format"
import { flowBytes, observerReading, type FlowReport } from "@/lib/network-flows"
import { UTCTime, dayWords } from "@/components/network/flows/marks"

const SOURCES_DRAWN = 4

/**
 * What the recorder samples, the recorder, and what it keeps.
 *
 * With recording on, the left is the latest sample's sources, each wired by
 * how its read went; the right is the socket-hour rows, the recorded hours
 * and the kernel observer, and the lines carry a pulse while samples are
 * landing. With recording off and nothing kept, the same picture says what
 * would be sampled and kept, every line dashed and still.
 */
export function FlowsPicture({
  data,
  day,
  loading,
  stale,
}: {
  data?: FlowReport
  /** The UTC day the page is reading, `2026-10-07`. */
  day: string
  loading?: boolean
  /** The reading on screen is the last known one: nothing is drawn as moving. */
  stale?: boolean
}) {
  const settings = data?.settings
  const recording =
    !stale && Boolean(settings?.enabled) && ["recording", "partial"].includes(data?.status ?? "")
  const flowing = (has: boolean): WireLine =>
    has ? { state: "up", moving: recording } : { state: "idle" }
  const cycle = data?.lastCycle
  const jump = (id: string) => () =>
    document.getElementById(id)?.scrollIntoView({ block: "start", behavior: "smooth" })

  const sampled: WireFlowNode[] = cycle?.sources.length
    ? cycle.sources.map((source) => {
        const container = /^[0-9a-f]{64}$/.test(source.id)
        return {
          id: `source:${source.id}`,
          mark: (size) => (
            <WireMark size={size} tone="logo">
              {container ? <ProductGlyph id="docker" /> : <Servers aria-hidden />}
            </WireMark>
          ),
          eyebrow: source.status.replaceAll("_", " "),
          title: source.name,
          name: source.name,
          hint: (
            <span className="numeric">
              {source.tcp} TCP · {source.udp} UDP
            </span>
          ),
          line: source.error
            ? { state: "danger" }
            : source.status === "observed"
              ? { state: "up", moving: recording }
              : { state: "warning" },
        }
      })
    : [
        {
          id: "would:host",
          mark: (size) => (
            <WireMark size={size}>
              <Servers aria-hidden />
            </WireMark>
          ),
          title: "This server",
          name: "This server",
          hint: "its TCP and UDP sockets",
          line: { state: "idle" },
        },
        {
          id: "would:containers",
          mark: (size) => (
            <WireMark size={size} tone="logo">
              <ProductGlyph id="docker" />
            </WireMark>
          ),
          title: "Each container",
          name: "Each container",
          hint: "the sockets in its namespace",
          line: { state: "idle" },
        },
      ]

  const rows = data?.retainedRows ?? 0
  const hours = data?.coverageHours.length ?? 0
  const observer = data ? observerReading(data.kernelObserver.status) : undefined
  const kept: WireFlowNode[] = [
    {
      id: "kept:rows",
      mark: (size) => (
        <WireMark size={size} hue={rows > 0 ? "var(--chart-1)" : undefined}>
          <Layers aria-hidden />
        </WireMark>
      ),
      title: "Socket hours",
      name: "Socket hours",
      hint:
        rows > 0 && data ? (
          <>
            <span className="numeric block">
              {plural(rows, "row")} · {flowBytes(BigInt(data.retainedBytes))}
              {data.prunedRows > 0 && ` · ${data.prunedRows} pruned`}
            </span>
            {data.retainedFrom && (
              <span className="block">
                from <UTCTime value={data.retainedFrom} form="short" />
              </span>
            )}
          </>
        ) : settings?.enabled ? (
          "nothing kept yet"
        ) : (
          "a row for each socket and hour"
        ),
      line: flowing(rows > 0),
      onPress: rows > 0 ? jump("flow-rows") : undefined,
      label: "Go to the socket hours",
    },
    {
      id: "kept:hours",
      mark: (size) => (
        <WireMark size={size} hue={hours > 0 ? "var(--chart-2)" : undefined}>
          <Clock aria-hidden />
        </WireMark>
      ),
      title: "Recorded hours",
      name: "Recorded hours",
      hint:
        hours > 0 ? (
          <span className="numeric">
            {hours} of 24 on {dayWords(day)}
          </span>
        ) : settings?.enabled ? (
          `none on ${dayWords(day)}`
        ) : (
          "which hours were sampled"
        ),
      line: flowing(hours > 0),
      onPress: hours > 0 ? jump("flow-hours") : undefined,
      label: "Go to the recorded hours",
    },
    {
      id: "kept:observer",
      mark: (size) => (
        <WireMark
          size={size}
          hue={data?.kernelObserver.status === "recording" ? "var(--chart-5)" : undefined}
        >
          <Cpu aria-hidden />
        </WireMark>
      ),
      eyebrow: "Opt-in",
      title: "Kernel observer",
      name: "Kernel observer",
      hint: observer ? observer.label.toLowerCase() : "payload bytes, when switched on",
      line:
        data?.kernelObserver.status === "recording"
          ? { state: "up", moving: recording }
          : data?.kernelObserver.status === "partial"
            ? { state: "warning" }
            : { state: "idle" },
      onPress: data ? jump("flow-observer") : undefined,
      label: "Go to the kernel observer",
    },
  ]

  return (
    <WireFlow
      loading={loading}
      start={{
        title: "Sampled",
        nodes: sampled,
        max: SOURCES_DRAWN,
        loadingRows: 2,
        more: { words: (hidden) => `${hidden} more sources` },
      }}
      end={{ title: "Kept", nodes: kept, loadingRows: 3 }}
      hub={{
        mark: (
          <WireMark tone={recording ? "brand" : "neutral"} size="lg">
            <ClockRewind aria-hidden />
          </WireMark>
        ),
        eyebrow: "Recorder",
        title: settings ? `every ${settings.intervalSeconds}s` : undefined,
        hint: settings && data && (
          <>
            <span className="block">keeps {plural(settings.retentionDays, "day")}</span>
            <span className="block" title="Every restart begins a fresh byte baseline">
              collector started <UTCTime value={data.collectorStartedAt} form="short" />
            </span>
          </>
        ),
      }}
    />
  )
}
