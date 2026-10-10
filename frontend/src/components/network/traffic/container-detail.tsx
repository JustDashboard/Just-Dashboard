"use client"

import Link from "next/link"
import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes, plural, rate, relativeTime } from "@/lib/format"
import { flowBytes, flowOwnerName, type FlowReport } from "@/lib/network-flows"
import { foldHistoricalFlows, hourBounds, type ContainerTrafficDetail } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { SidePanel } from "@/components/side-panel"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { FormFact, FormFacts } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { RX, TX } from "@/components/network/rate-pair"

const SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`

/**
 * One container's traffic over the page's window: its own chart at a finer
 * grain than the band's sparkline, what Docker says it is (image, compose
 * service, networks), and who it talked to. The peers are the opt-in socket
 * history's, attributed to this container's exact id; with that history off
 * the sheet says so rather than drawing an empty list as "nobody".
 */
export function ContainerTrafficSheet({
  name,
  window,
  onClose,
}: {
  name: string
  window: string
  onClose: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const detail = usePoll<ContainerTrafficDetail>(
    (signal) => get(`/network/traffic/containers/${encodeURIComponent(name)}`, { window }, signal),
    30_000,
    [name, window],
  )
  const d = detail.data
  const flows = usePoll<FlowReport>(
    (signal) => {
      // The hours are taken when the poll runs, so a sheet left open follows
      // the clock rather than the moment it opened.
      const now = Math.floor(Date.now() / 1000)
      const bounds = hourBounds(now - (d?.windowSeconds ?? 3600), now)
      return get(
        "/network/flows/",
        { containerId: d?.id ?? "", from: bounds.from, to: bounds.to, limit: 500 },
        signal,
      )
    },
    60_000,
    [d?.id, d?.windowSeconds],
    { enabled: admin && Boolean(d?.id) },
  )
  const rows = useMemo(
    () => (d?.series ?? []).map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx })),
    [d?.series],
  )
  const peers = useMemo(
    () => foldHistoricalFlows(flows.data?.rows ?? [], (row) => flowOwnerName(row.socket.owner)),
    [flows.data?.rows],
  )

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      title={<span className="font-mono">{name}</span>}
      description={`Traffic of ${name} over the window`}
    >
      {!d ? (
        detail.error ? (
          <ErrorState error={detail.error} onRetry={detail.refresh} />
        ) : (
          <LoadingPanel />
        )
      ) : (
        <div className="flex flex-col gap-6">
          <NetworkReadWarning
            error={detail.error}
            refresh={detail.refresh}
            lastSuccess={detail.lastSuccess}
            reading="container traffic"
          />
          <FormFacts>
            <FormFact label="Moved" mono>
              ↓ {bytes(d.rxBytes)} · ↑ {bytes(d.txBytes)}
            </FormFact>
            <FormFact label="Now" mono>
              ↓ {rate(d.rxRate)} · ↑ {rate(d.txRate)}
            </FormFact>
            <FormFact label="Service" mono>
              {d.project
                ? `${d.project}/${d.service ?? "?"}`
                : d.dockerError
                  ? "unknown"
                  : "not from compose"}
            </FormFact>
            <FormFact label="Image" mono>
              {d.image ?? "—"}
            </FormFact>
          </FormFacts>
          {d.dockerError && <Notice title="Docker could not be read">{d.dockerError}</Notice>}
          {d.networks.length > 0 && (
            <p className="text-hint text-muted-foreground">
              On {d.networks.join(", ")} · last sample{" "}
              {relativeTime(new Date(d.lastSeen * 1000).toISOString())}
            </p>
          )}
          <ChartPanel
            plain
            title={`${name} · ${window}`}
            rows={rows}
            series={SERIES}
            format={formatRate}
            axisFormat={axisRate}
            showPeaks={false}
            height={180}
            note="Nothing recorded for this container in this window."
          />
          <Panel plain aria-label={`Who ${name} talked to`}>
            <PanelHeader
              title="Who it talked to"
              actions={
                flows.data && (
                  <span className="numeric text-hint text-muted-foreground">
                    {plural(peers.length, "remote address", "remote addresses")}
                  </span>
                )
              }
            />
            <PanelBody flush>
              {!admin ? (
                <EmptyNote>
                  Which addresses a container talks to is an administrator&rsquo;s to see.
                </EmptyNote>
              ) : !d.id ? (
                <EmptyNote>
                  Docker no longer lists this container, so its socket history cannot be matched to
                  it.
                </EmptyNote>
              ) : flows.error && !flows.data ? (
                <ErrorState error={flows.error} onRetry={flows.refresh} />
              ) : !flows.data ? (
                <LoadingPanel />
              ) : !flows.data.settings.enabled && flows.data.rows.length === 0 ? (
                <EmptyNote>
                  Socket history is off, so nothing attributes peers to this container.{" "}
                  <Link href="/network/flows" className="underline underline-offset-4">
                    Turn it on in Socket history
                  </Link>
                  .
                </EmptyNote>
              ) : peers.length === 0 ? (
                <EmptyNote>No retained socket of this container in the window.</EmptyNote>
              ) : (
                <RowList>
                  {peers.slice(0, 20).map((p) => (
                    <Row
                      key={p.address}
                      title={<span className="font-mono">{p.address}</span>}
                      subtitle={`${p.protocols.join(", ")} · ports ${p.localPorts.join(", ")} · ${plural(p.sockets, "socket row")}`}
                      trailing={
                        <span className="numeric flex gap-3 font-mono text-hint">
                          <span style={{ color: RX }}>↓ {flowBytes(p.rxBytes)}</span>
                          <span style={{ color: TX }}>↑ {flowBytes(p.txBytes)}</span>
                        </span>
                      }
                    />
                  ))}
                </RowList>
              )}
            </PanelBody>
          </Panel>
          {flows.data && (
            <p className="text-hint text-muted-foreground">
              Peers are the socket history&rsquo;s rows for this container&rsquo;s exact id: TCP
              bytes are native counter deltas, UDP has none, and sockets that opened and closed
              between two samples are not in it.
              {flows.data.truncated && " More rows than shown were retained."}
            </p>
          )}
        </div>
      )}
    </SidePanel>
  )
}
