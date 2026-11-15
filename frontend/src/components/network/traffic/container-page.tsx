"use client"

import Link from "next/link"
import { useMemo } from "react"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { ApiError, get } from "@/lib/api"
import { bytes, plural, rate, relativeTime } from "@/lib/format"
import { flowBytes, flowOwnerName, type FlowReport } from "@/lib/network-flows"
import { foldHistoricalFlows, hourBounds, type ContainerTrafficDetail } from "@/lib/network-traffic"
import type { ContainerTraffic } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { ArrowUpRight, Box } from "@/components/icons"
import { Segments } from "@/components/deploy/settings/segments"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { RX, TX } from "@/components/network/rate-pair"
import { containerHref, containerWindow, trafficHref } from "./address"
import {
  BackLink,
  DetailRail,
  DetailShell,
  RailGhosts,
  RailRow,
  StepArrows,
  steps,
  type Step,
} from "./detail-shell"
import { FactGroups, FactRowHeld } from "./facts"
import { WINDOW_WORDS, type RecordedWindow } from "./span"

const SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`
const WINDOW_OPTIONS = (["1h", "6h", "24h", "7d"] as const).map((value) => ({
  value,
  label: value,
}))
const CHART_HEIGHT = 220

function decoded(segment: string | undefined) {
  if (!segment) return null
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/**
 * One container's traffic, as a place of its own: its chart over a window at
 * a finer grain than the band's span, what Docker says it is (image, compose
 * service, networks), and who it talked to. The peers are the opt-in socket
 * history's, attributed to this container's exact id; with that history off
 * the page says so rather than drawing an empty list as "nobody".
 *
 * It was a sheet over the Traffic page, which is more than a glance for a
 * column that wide: a chart and a list of up to twenty peers. The containers
 * it was picked from are the rail from `xl`, ranked as the band ranks them,
 * and the window is in the address, so the page it opens on is the window the
 * Traffic page was set to.
 */
export function ContainerTrafficPage() {
  const params = useParams<{ name: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decoded(params.name)
  const range = containerWindow(search)
  const [query, setQuery] = useSessionState("network.traffic.containers.query", "")
  const list = usePoll<ContainerTraffic>(
    (signal) => get("/network/traffic/containers", { window: range }, signal),
    30_000,
    [range],
  )
  const ranked = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return [...(list.data?.containers ?? [])]
      .filter((c) => !needle || c.name.toLowerCase().includes(needle))
      .sort((a, b) => b.rxBytes + b.txBytes - (a.rxBytes + a.txBytes))
  }, [list.data, query])
  const open = (next: string) => router.push(containerHref(next, range))
  const step = steps(
    ranked.map((c) => c.name),
    name,
    open,
  )
  const detail = usePoll<ContainerTrafficDetail>(
    (signal) =>
      get(
        `/network/traffic/containers/${encodeURIComponent(name ?? "")}`,
        { window: range },
        signal,
      ),
    30_000,
    [name, range],
    { enabled: name !== null },
  )
  const setWindow = (next: RecordedWindow) => {
    if (name === null) return
    window.history.replaceState(null, "", containerHref(name, next))
  }

  return (
    <DetailShell
      name="Container traffic"
      what="container"
      stateKey="network.traffic.container"
      refresh={() => {
        list.refresh()
        detail.refresh()
      }}
      previous={step.previous}
      next={step.next}
      rail={
        <DetailRail
          label="Containers"
          slot="container-rail"
          current={name}
          query={query}
          onQuery={setQuery}
          placeholder="Container name"
          caption={
            list.data
              ? `${plural(ranked.length, "container")} · most moved in ${WINDOW_WORDS[range]}`
              : list.error
                ? "The containers could not be read"
                : "Reading the containers…"
          }
          empty={
            list.data && ranked.length === 0
              ? query.trim()
                ? "No container matches."
                : "No container moved anything."
              : undefined
          }
        >
          {!list.data && !list.error && <RailGhosts />}
          {ranked.map((c) => (
            <RailRow
              key={c.name}
              id={c.name}
              name={c.name}
              label={`${c.name}, ${bytes(c.rxBytes + c.txBytes)}`}
              current={c.name === name}
              onOpen={() => open(c.name)}
              leading={
                <ProductLogo
                  id={imageProduct(c.name)}
                  size="sm"
                  fallback={Box}
                  className="size-7 rounded-md [&_img]:size-4"
                />
              }
              detail={`↓ ${rate(c.rxRate)} ↑ ${rate(c.txRate)}`}
              trailing={bytes(c.rxBytes + c.txBytes)}
            />
          ))}
        </DetailRail>
      }
      header={
        <ContainerHeader
          name={name}
          range={range}
          detail={detail.data}
          missing={detail.error instanceof ApiError && detail.error.status === 404}
          previous={step.previous}
          next={step.next}
        />
      }
    >
      {name === null ? (
        <EmptyState
          icon={Box}
          title="That is not a container's name"
          action={
            <Link href={trafficHref()} className="text-body font-medium text-brand hover:underline">
              Back to Traffic
            </Link>
          }
        />
      ) : (
        <ContainerBody key={name} name={name} range={range} onWindow={setWindow} detail={detail} />
      )}
    </DetailShell>
  )
}

function ContainerHeader({
  name,
  range,
  detail: d,
  missing,
  previous,
  next,
}: {
  name: string | null
  range: RecordedWindow
  detail: ContainerTrafficDetail | undefined
  missing: boolean
  previous: Step
  next: Step
}) {
  const service = d
    ? d.project
      ? `${d.project}/${d.service ?? "?"}`
      : d.dockerError
        ? "service unknown"
        : "not from compose"
    : undefined
  return (
    <>
      <PageContext
        eyebrow={
          <BackLink href={trafficHref({ span: range === "1h" ? "live" : range })}>Traffic</BackLink>
        }
        title={name ? `${name} traffic` : "Container traffic"}
        actions={<StepArrows what="container" previous={previous} next={next} />}
      />
      {name !== null && (
        <HostIdentity
          label={`About ${name}`}
          mark={imageProduct(d?.image ?? name)}
          fallback={Box}
          title={<span className="truncate font-mono">{name}</span>}
          state={
            d ? (
              d.id ? (
                <Status state={d.state ?? "unknown"} />
              ) : (
                <Status tone="unknown" label={d.dockerError ? "Docker unread" : "not in Docker"} />
              )
            ) : missing ? (
              <Status tone="unknown" label="nothing recorded" />
            ) : (
              <Status tone="unknown" label="reading" />
            )
          }
          facts={
            d ? (
              <FactGroups
                groups={[
                  [
                    <HostFact key="image">
                      <span className="font-mono">{d.image ?? "image unknown"}</span>
                    </HostFact>,
                    <HostFact key="service">
                      <span className={d.project ? "font-mono" : undefined}>{service}</span>
                    </HostFact>,
                  ],
                  [
                    d.networks.length > 0 && (
                      <HostFact key="networks">
                        on <span className="font-mono">{d.networks.join(", ")}</span>
                      </HostFact>
                    ),
                    <span key="seen">
                      last sample {relativeTime(new Date(d.lastSeen * 1000).toISOString())}
                    </span>,
                  ],
                ]}
              />
            ) : missing ? (
              <span>Nothing recorded in {WINDOW_WORDS[range]}</span>
            ) : (
              <>
                <TextShimmer>Reading the recorded traffic…</TextShimmer>
                <FactRowHeld />
              </>
            )
          }
          command={
            d?.id ? (
              <Button asChild size="sm" variant="outline">
                <Link href={`/docker/containers/${encodeURIComponent(d.id)}`}>
                  Container
                  <ArrowUpRight />
                </Link>
              </Button>
            ) : undefined
          }
        />
      )}
    </>
  )
}

function ContainerBody({
  name,
  range,
  onWindow,
  detail,
}: {
  name: string
  range: RecordedWindow
  onWindow: (next: RecordedWindow) => void
  detail: ReturnType<typeof usePoll<ContainerTrafficDetail>>
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const d = detail.data
  const flows = usePoll<FlowReport>(
    (signal) => {
      // The hours are taken when the poll runs, so a page left open follows
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
  const picker = (
    <Segments
      label="How far back the chart reaches"
      value={range}
      options={WINDOW_OPTIONS}
      onChange={onWindow}
    />
  )
  const missing = detail.error instanceof ApiError && detail.error.status === 404

  if (!d && detail.error) {
    return missing ? (
      <EmptyState
        icon={Box}
        title={`Nothing is recorded for ${name} in ${WINDOW_WORDS[range]}`}
        description="A container that moved nothing, or stopped before the window began, has no samples in it. A longer window may."
        action={picker}
      />
    ) : (
      <ErrorState error={detail.error} onRetry={detail.refresh} retryable />
    )
  }

  return (
    <>
      <NetworkReadWarning
        error={d ? detail.error : undefined}
        refresh={detail.refresh}
        lastSuccess={detail.lastSuccess}
        reading="container traffic"
      />
      {d?.dockerError && <Notice title="Docker could not be read">{d.dockerError}</Notice>}
      {/* The plot and what it stands on (title line, legend, the line under
          it): one height before and after the samples land. */}
      <div
        data-slot="container-chart"
        className="flex min-h-[407px] min-w-0 flex-col sm:min-h-[391px]"
      >
        <ChartPanel
          plain
          title="Traffic"
          actions={picker}
          rows={rows}
          series={SERIES}
          format={formatRate}
          axisFormat={axisRate}
          showPeaks={false}
          height={CHART_HEIGHT}
          note={d ? "Nothing recorded for this container in this window." : "Reading the samples…"}
          footer={
            // Two lines on a phone and one from `sm`, in every state, so
            // the first answer fills the line instead of adding it.
            <p
              aria-label={`What ${name} moved`}
              className="numeric flex flex-col gap-x-2 text-hint text-muted-foreground sm:flex-row"
            >
              {d ? (
                <>
                  <span>
                    <span className="text-foreground">
                      ↓ {bytes(d.rxBytes)} ↑ {bytes(d.txBytes)}
                    </span>{" "}
                    in {WINDOW_WORDS[range]}
                  </span>{" "}
                  <span className="max-sm:hidden">·</span>{" "}
                  <span>
                    now ↓ {rate(d.rxRate)} ↑ {rate(d.txRate)}
                  </span>
                </>
              ) : (
                <>
                  <span>&nbsp;</span>
                  <span className="sm:hidden">&nbsp;</span>
                </>
              )}
            </p>
          }
        />
      </div>
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
          ) : !d ? (
            <LoadingRows rows={3} />
          ) : !d.id ? (
            <EmptyNote>
              Docker no longer lists this container, so its socket history cannot be matched to it.
            </EmptyNote>
          ) : flows.error && !flows.data ? (
            <ErrorState error={flows.error} onRetry={flows.refresh} retryable />
          ) : !flows.data ? (
            <LoadingRows rows={3} />
          ) : !flows.data.settings.enabled && flows.data.rows.length === 0 ? (
            <EmptyState
              className="mt-2"
              title="Socket history is off"
              description="Nothing attributes peers to this container until it records."
              action={
                <Button asChild size="sm">
                  <Link href="/network/flows">Turn on socket history</Link>
                </Button>
              }
            />
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
          {admin && flows.data && (
            <p className="pt-3 text-hint text-muted-foreground">
              Peers are the socket history&rsquo;s rows for this container&rsquo;s exact id: TCP
              bytes are native counter deltas, UDP has none, and sockets that opened and closed
              between two samples are not in it.
              {peers.length > 20 && ` The first 20 of ${peers.length} are shown.`}
              {flows.data.truncated && " More rows than shown were retained."}
            </p>
          )}
        </PanelBody>
      </Panel>
    </>
  )
}
