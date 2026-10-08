"use client"

import { useState } from "react"
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
import { Sparkline } from "@/components/metrics/sparkline"
import { ErrorState, Notice } from "@/components/state"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { RX, TX } from "@/components/network/rate-pair"
import { BandRow, ShareBar, shade } from "@/components/network/traffic/traffic-band"
import type { TrafficWindow } from "@/components/network/traffic/interface-charts"

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
 * are set to. Each is the Processes page's workload bar (`procs/workloads.tsx`)
 * with traffic for its measurement, so a reader who has met one has met both.
 *
 * Programs are read from `ss` on the host every three seconds and are the
 * administrators' to see — they name who connects. The first read has nothing
 * to difference against, so it says it is measuring rather than drawing zeros.
 * Pressing a program opens the peers it talks to under the bars. Containers
 * are the recorder's samples over the window, with the live window mapped to
 * the last hour because a container has no two-second ring of its own, and
 * each carries its shape over that window.
 */
export function TrafficWorkloads({ span }: { span: TrafficWindow }) {
  const [open, setOpen] = useState<string>()
  const { can } = useAuth()
  const admin = can("system.admin")
  const range = span === "live" ? "1h" : span
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
        <Panel plain aria-label="Traffic by program">
          <PanelHeader
            title="Programs"
            actions={
              programs.data &&
              !programs.data.warming && (
                <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                  <span className="font-medium text-foreground">
                    <LiveBytes
                      value={sum(programs.data.programs, (p) => p.rxRate + p.txRate)}
                      suffix="/s"
                    />
                  </span>
                  {programs.error ? "at the last read" : "right now"}
                </span>
              )
            }
          />
          <PanelBody className="space-y-3 pt-4">
            <NetworkReadWarning
              error={programs.error && programs.data ? programs.error : undefined}
              refresh={programs.refresh}
              lastSuccess={programs.lastSuccess}
              reading="program traffic"
            />
            <ProgramBand
              data={programs.data}
              admin={admin}
              error={programs.error}
              refresh={programs.refresh}
              open={open}
              onOpen={setOpen}
            />
          </PanelBody>
        </Panel>

        <Panel plain aria-label="Traffic by container">
          <PanelHeader
            title="Containers"
            actions={
              containers.data?.recording && (
                <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                  <span className="font-medium text-foreground">
                    {bytes(sum(containers.data.containers, (c) => c.rxBytes + c.txBytes))}
                  </span>
                  in {WINDOW_WORDS[range]}
                </span>
              )
            }
          />
          <PanelBody className="space-y-3 pt-4">
            <NetworkReadWarning
              error={containers.error && containers.data ? containers.error : undefined}
              refresh={containers.refresh}
              lastSuccess={containers.lastSuccess}
              reading="container traffic"
            />
            <ContainerBand
              data={containers.data}
              error={containers.error}
              refresh={containers.refresh}
            />
          </PanelBody>
        </Panel>
      </div>

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
              </span>
            }
          />
          <PanelBody flush>
            <RowList>
              {peers.peers.map((peer) => (
                <Row
                  key={`${peer.address}:${peer.port}`}
                  title={
                    <span className="font-mono">
                      {peer.address}
                      <span className="text-muted-foreground">:{peer.port}</span>
                    </span>
                  }
                  subtitle={plural(peer.connections, "connection")}
                  trailing={
                    <span className="numeric flex gap-3 font-mono text-hint">
                      <span style={{ color: RX }}>↓ {bytes(peer.rxBytes)}</span>
                      <span style={{ color: TX }}>↑ {bytes(peer.txBytes)}</span>
                    </span>
                  }
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

function ProgramBand({
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
  if (!admin) {
    return (
      <p className="py-2 text-body text-muted-foreground">
        Which programs connect is an administrator&rsquo;s to see.
      </p>
    )
  }
  if (error && !data) {
    return <ErrorState error={error} onRetry={refresh} />
  }
  if (!data || data.warming) {
    return (
      <p className="py-2 text-body">
        <TextShimmer>Measuring the first interval…</TextShimmer>
      </p>
    )
  }
  const ranked = [...data.programs]
    .filter((p) => p.rxRate + p.txRate > 0)
    .sort((a, b) => b.rxRate + b.txRate - (a.rxRate + a.txRate))
  const named = ranked.slice(0, SHOWN)
  const rest = sum(ranked.slice(SHOWN), (p) => p.rxRate + p.txRate)
  if (named.length === 0) {
    return <p className="py-2 text-body text-muted-foreground">No program is moving anything.</p>
  }
  return (
    <>
      <ShareBar
        label="Traffic by program"
        rest={rest}
        format={rate}
        parts={named.map((p, rank) => ({
          key: p.name,
          value: p.rxRate + p.txRate,
          color: shade(HUE.net, rank),
          label: `${p.name} ${rate(p.rxRate + p.txRate)}`,
        }))}
      />
      <ul className="-mx-2">
        {named.map((p, rank) => {
          const product = processProduct(p.name)
          return (
            <BandRow
              key={p.name}
              color={shade(HUE.net, rank)}
              mark={
                product ? (
                  <ProductGlyph id={product} className="size-3.5" />
                ) : (
                  <Cpu aria-hidden className="size-3.5 text-muted-foreground" />
                )
              }
              name={p.name}
              detail={plural(p.connections, "connection")}
              figure={<LiveBytes value={p.rxRate + p.txRate} suffix="/s" />}
              pressed={open === p.name}
              onPress={() => onOpen(open === p.name ? undefined : p.name)}
              label={`Who ${p.name} talks to`}
            />
          )
        })}
      </ul>
      <p className="text-hint text-muted-foreground">
        {rest > 0 &&
          `${rate(rest)} more across ${plural(ranked.length - SHOWN, "other program")}. `}
        {data.truncated && "The host has more sockets than are read each time. "}
        {data.note}
      </p>
    </>
  )
}

function ContainerBand({
  data,
  error,
  refresh,
}: {
  data?: ContainerTraffic
  error?: Error
  refresh: () => void
}) {
  if (error && !data) {
    return <ErrorState error={error} onRetry={refresh} />
  }
  if (!data) {
    return (
      <p className="py-2 text-body">
        <TextShimmer>Reading the recorded traffic…</TextShimmer>
      </p>
    )
  }
  if (!data.recording) {
    return (
      <Notice title="Nothing is recorded">
        The metrics retention is zero (JD_METRICS_RETENTION), so no container&rsquo;s traffic is
        kept.
      </Notice>
    )
  }
  const ranked = [...data.containers]
    .filter((c) => c.rxBytes + c.txBytes > 0)
    .sort((a, b) => b.rxBytes + b.txBytes - (a.rxBytes + a.txBytes))
  const named = ranked.slice(0, SHOWN)
  const rest = sum(ranked.slice(SHOWN), (c) => c.rxBytes + c.txBytes)
  if (named.length === 0) {
    return <p className="py-2 text-body text-muted-foreground">No container moved anything.</p>
  }
  return (
    <>
      <ShareBar
        label="Traffic by container"
        rest={rest}
        format={(v) => bytes(v)}
        parts={named.map((c, rank) => ({
          key: c.name,
          value: c.rxBytes + c.txBytes,
          color: shade(HUE.net, rank),
          label: `${c.name} ${bytes(c.rxBytes + c.txBytes)}`,
        }))}
      />
      <ul className="-mx-2">
        {named.map((c, rank) => (
          <BandRow
            key={c.name}
            color={shade(HUE.net, rank)}
            mark={<ProductGlyph id={imageProduct(c.name)} className="size-3.5" />}
            name={c.name}
            detail={`↓ ${rate(c.rxRate)} ↑ ${rate(c.txRate)}`}
            middle={
              <Sparkline
                values={c.series.map((p) => p.rx + p.tx)}
                color={shade(HUE.net, rank)}
                width={72}
                height={20}
                className="hidden h-5 w-[72px] shrink-0 sm:block"
                label={`${c.name}'s traffic over the window`}
              />
            }
            figure={bytes(c.rxBytes + c.txBytes)}
          />
        ))}
      </ul>
      {rest > 0 && (
        <p className="text-hint text-muted-foreground">
          {bytes(rest)} more across {plural(ranked.length - SHOWN, "other container")}.
        </p>
      )}
    </>
  )
}
