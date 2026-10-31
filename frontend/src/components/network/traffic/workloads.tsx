"use client"

import Link from "next/link"
import { useState } from "react"
import { BarReading } from "@/components/bar-reading"
import { Cpu } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, rate } from "@/lib/format"
import type { ContainerTraffic, ProcessTraffic } from "@/lib/types"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { HUE, LiveBytes } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, imageProduct, processProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { RX, TX } from "@/components/network/rate-pair"
import {
  coveringWindow,
  isRange,
  type TrafficSpan,
  type TrafficWindow,
} from "@/components/network/traffic/interface-charts"
import { ContainerTrafficSheet } from "@/components/network/traffic/container-detail"
import { useNow } from "@/components/deploy/vocabulary"
import { millis } from "@/lib/network-traffic"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/** How many programs and containers each bar names; the rest of what moved is one muted span. */
const SHOWN = 5

const WINDOW_WORDS: Record<Exclude<TrafficWindow, "live">, string> = {
  "1h": "the last hour",
  "6h": "the last 6 hours",
  "24h": "the last day",
  "7d": "the last 7 days",
}

const sum = <T,>(list: T[], value: (item: T) => number) =>
  list.reduce((total, item) => total + value(item), 0)

/**
 * Who is moving the bytes, as two bars: the programs by what they move right
 * now, and the containers by what they moved over the window the charts above
 * are set to. Each is a share of what moved rather than a fraction of the link:
 * a program moving two megabytes a second on a gigabit uplink would be a
 * hairline, and the question is *who*, which the link's speed cannot rank.
 *
 * Programs are read from `ss` on the host every three seconds and are the
 * administrators' to see — they name who connects. The first read has nothing
 * to difference against, so it says it is measuring rather than drawing zeros.
 * Pressing a program opens the peers it talks to under the bars, UDP ones
 * included and marked as having no byte counters; the strip's end says what
 * the read could not see — connections born and gone between two reads, sockets
 * that closed with their last bytes uncounted — and where the history before
 * this page opened is kept. Containers are the recorder's samples over the
 * window, with the live window mapped to the last hour because a container has
 * no two-second ring of its own, and a typed range to the named window that
 * covers it; every container can be listed from the strip's end, and pressing
 * one opens its own chart, service and the peers its socket history attributes
 * to it.
 *
 * Both are polled, so neither is marked live; a program's trail is its rate. A
 * failed read of data already on screen says so under the blocks rather than in
 * them, because it is several lines and a block keeps one height.
 */
export function TrafficWorkloads({ span }: { span: TrafficSpan }) {
  const [open, setOpen] = useState<string>()
  const [opened, setOpened] = useState<string>()
  const { can } = useAuth()
  const admin = can("system.admin")
  const now = useNow(60_000)
  const range = coveringWindow(span, Math.floor(now / 1000))
  const programs = usePoll<ProcessTraffic>(
    (signal) => get("/network/traffic/processes", undefined, signal),
    3000,
    [],
    { enabled: admin },
  )
  const containers = usePoll<ContainerTraffic>(
    (signal) => get("/network/traffic/containers", { window: range }, signal),
    30_000,
    [range],
  )
  const peers = programs.data?.programs.find((p) => p.name === open)

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <div data-slot="workloads" className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2">
        <ProgramReading
          data={programs.data}
          admin={admin}
          error={programs.error}
          refresh={programs.refresh}
          open={open}
          onOpen={setOpen}
        />
        <ContainerReading
          data={containers.data}
          error={containers.error}
          refresh={containers.refresh}
          words={`in ${WINDOW_WORDS[range]}${isRange(span) ? " (covers the range)" : ""}`}
          onOpen={setOpened}
        />
      </div>
      <NetworkReadWarning
        error={programs.error && programs.data ? programs.error : undefined}
        refresh={programs.refresh}
        lastSuccess={programs.lastSuccess}
        reading="program traffic"
      />
      <NetworkReadWarning
        error={containers.error && containers.data ? containers.error : undefined}
        refresh={containers.refresh}
        lastSuccess={containers.lastSuccess}
        reading="container traffic"
      />

      {peers && (
        <Panel plain aria-label={`Who ${peers.name} talks to`}>
          <PanelHeader
            title={
              <span>
                <span className="font-mono">{peers.name}</span> talks to
              </span>
            }
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(peers.connections, "connection")} ·{" "}
                {plural(peers.pids.length, "process", "processes")}
                {peers.medianRttMs !== undefined && ` · median RTT ${millis(peers.medianRttMs)}`}
                {(peers.segmentsOut ?? 0) > 0 &&
                  ` · resent ${peers.retransmitted ?? 0} of ${peers.segmentsOut} segments`}
                {(peers.udpConnected ?? 0) + (peers.udpUnconnected ?? 0) > 0 &&
                  ` · ${plural((peers.udpConnected ?? 0) + (peers.udpUnconnected ?? 0), "UDP socket")}`}
              </span>
            }
          />
          <PanelBody flush>
            <RowList>
              {peers.peers.map((peer) => (
                <Row
                  key={`${peer.protocol ?? "tcp"}:${peer.address}:${peer.port}`}
                  title={
                    <span className="font-mono">
                      {peer.address}
                      <span className="text-muted-foreground">:{peer.port}</span>
                    </span>
                  }
                  subtitle={`${(peer.protocol ?? "tcp").toUpperCase()} · ${plural(peer.connections, "connection")}`}
                  trailing={
                    peer.bytesKnown === false ? (
                      <span className="text-hint text-muted-foreground">no byte counters</span>
                    ) : (
                      <span className="numeric flex gap-3 font-mono text-hint">
                        <span style={{ color: RX }}>↓ {bytes(peer.rxBytes)}</span>
                        <span style={{ color: TX }}>↑ {bytes(peer.txBytes)}</span>
                      </span>
                    )
                  }
                />
              ))}
            </RowList>
            {(peers.udpUnconnected ?? 0) > 0 && (
              <p className="px-4 py-3 text-hint text-muted-foreground">
                {plural(peers.udpUnconnected ?? 0, "unconnected UDP socket")} — listeners and
                servers such as HTTP/3 — whose peers the socket table cannot name.
              </p>
            )}
          </PanelBody>
        </Panel>
      )}
      {opened && (
        <ContainerTrafficSheet name={opened} window={range} onClose={() => setOpened(undefined)} />
      )}
    </div>
  )
}

/** What one program read could not see, and where what came before it is kept. */
function readLimits(data: ProcessTraffic) {
  const parts: string[] = []
  if (data.missedOpens !== undefined && data.missedOpens > 0) {
    parts.push(
      `${plural(data.missedOpens, "TCP connection")} opened and closed between two reads; their bytes are in no rate`,
    )
  } else if (data.missedOpens === undefined && !data.warming) {
    parts.push("how many short connections fell between reads is unknown for this read")
  }
  if ((data.closed ?? 0) > 0)
    parts.push(`${plural(data.closed ?? 0, "socket")} closed since the last read`)
  if (data.udpError) parts.push(data.udpError)
  return [
    data.truncated && "The host has more sockets than are read each time.",
    parts.length > 0 && `${parts.join("; ")}.`,
    `${data.note}.`,
    data.history?.recording
      ? `Earlier traffic is in the socket history, recording${data.history.kernelObserver ? " with the kernel observer" : ""}.`
      : "Nothing before this page opened is kept; socket history records it once turned on.",
  ]
    .filter(Boolean)
    .join(" ")
}

/** A first read that failed, in the one line a block has for it. */
function ReadFailed({ error, refresh }: { error: Error; refresh: () => void }) {
  return (
    <span role="alert">
      {error.message}{" "}
      <Button size="xs" variant="outline" onClick={refresh}>
        Try again
      </Button>
    </span>
  )
}

function ProgramReading({
  data,
  admin,
  error,
  refresh,
  open,
  onOpen,
}: {
  data?: ProcessTraffic
  admin: boolean
  error?: Error
  refresh: () => void
  open?: string
  onOpen: (name: string | undefined) => void
}) {
  const ranked = [...(data?.programs ?? [])]
    .filter((p) => p.rxRate + p.txRate > 0)
    .sort((a, b) => b.rxRate + b.txRate - (a.rxRate + a.txRate))
  const named = ranked.slice(0, SHOWN)
  const others = ranked.length - named.length
  const limits = data && readLimits(data)
  return (
    <BarReading
      title="Programs"
      label="Traffic by program"
      list="Programs moving the most"
      hue={HUE.net}
      trail
      rest={sum(ranked.slice(SHOWN), (p) => p.rxRate + p.txRate)}
      restLabel={plural(others, "other program")}
      format={rate}
      figure={
        data &&
        !data.warming && (
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              <LiveBytes value={sum(data.programs, (p) => p.rxRate + p.txRate)} suffix="/s" />
            </span>
            {error ? "at the last read" : "right now"}
          </span>
        )
      }
      message={
        !admin ? (
          "Which programs connect is an administrator’s to see."
        ) : error && !data ? (
          <ReadFailed error={error} refresh={refresh} />
        ) : !data || data.warming ? (
          <TextShimmer>Measuring the first interval…</TextShimmer>
        ) : named.length === 0 ? (
          "No program is moving anything."
        ) : undefined
      }
      note={
        <>
          <p
            aria-label="What the program read could not see"
            title={limits}
            className="min-w-0 truncate"
          >
            {limits}
          </p>
          <Link href="/network/flows" className="shrink-0 underline underline-offset-4">
            Socket history
          </Link>
        </>
      }
      parts={named.map((p) => {
        const product = processProduct(p.name)
        const pressed = open === p.name
        return {
          key: p.name,
          value: p.rxRate + p.txRate,
          mark: product ? (
            <ProductGlyph id={product} className="size-3.5" />
          ) : (
            <Cpu aria-hidden className="size-3.5 text-muted-foreground" />
          ),
          name: p.name,
          detail: plural(p.connections, "connection"),
          figure: <LiveBytes value={p.rxRate + p.txRate} suffix="/s" />,
          label: `${p.name} ${rate(p.rxRate + p.txRate)}`,
          press: {
            label: `Who ${p.name} talks to`,
            pressed,
            opens: true,
            onPress: () => onOpen(pressed ? undefined : p.name),
          },
        }
      })}
    />
  )
}

function ContainerReading({
  data,
  error,
  refresh,
  words,
  onOpen,
}: {
  data?: ContainerTraffic
  error?: Error
  refresh: () => void
  /** The window the total covers, as the head says it. */
  words: string
  onOpen: (name: string) => void
}) {
  const all = data?.containers ?? []
  const ranked = [...all].sort((a, b) => b.rxBytes + b.txBytes - (a.rxBytes + a.txBytes))
  const moved = ranked.filter((c) => c.rxBytes + c.txBytes > 0)
  const named = moved.slice(0, SHOWN)
  const others = moved.length - named.length
  // Any number of containers fits a menu; a strip of marks holds a handful.
  const listing = all.length > named.length && (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="xs" variant="outline">
          Show all {all.length}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-72 min-w-56 overflow-y-auto">
        {ranked.map((c) => (
          <DropdownMenuItem
            key={c.name}
            aria-label={`Open ${c.name}'s traffic`}
            onSelect={() => onOpen(c.name)}
          >
            <ProductGlyph id={imageProduct(c.name)} className="size-3.5" />
            <span className="min-w-0 truncate">{c.name}</span>
            <span className="numeric ml-auto pl-3 text-hint text-muted-foreground">
              {bytes(c.rxBytes + c.txBytes)}
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
  return (
    <BarReading
      title="Containers"
      label="Traffic by container"
      list="Containers moving the most"
      hue={HUE.net}
      rest={sum(moved.slice(SHOWN), (c) => c.rxBytes + c.txBytes)}
      restLabel={plural(others, "other container")}
      format={(v) => bytes(v)}
      figure={
        data?.recording && (
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              {bytes(sum(data.containers, (c) => c.rxBytes + c.txBytes))}
            </span>
            {words}
          </span>
        )
      }
      message={
        error && !data ? (
          <ReadFailed error={error} refresh={refresh} />
        ) : !data ? (
          <TextShimmer>Reading the recorded traffic…</TextShimmer>
        ) : !data.recording ? (
          "Nothing is recorded: the metrics retention is zero (JD_METRICS_RETENTION), so no container’s traffic is kept."
        ) : named.length === 0 ? (
          <>No container moved anything. {listing}</>
        ) : undefined
      }
      note={listing}
      parts={named.map((c) => ({
        key: c.name,
        value: c.rxBytes + c.txBytes,
        mark: <ProductGlyph id={imageProduct(c.name)} className="size-3.5" />,
        name: c.name,
        detail: `↓ ${rate(c.rxRate)} ↑ ${rate(c.txRate)}`,
        figure: bytes(c.rxBytes + c.txBytes),
        label: `${c.name} ${bytes(c.rxBytes + c.txBytes)}`,
        press: { label: `Open ${c.name}'s traffic`, opens: true, onPress: () => onOpen(c.name) },
      }))}
    />
  )
}
