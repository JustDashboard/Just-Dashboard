"use client"

import Link from "next/link"
import { useMemo } from "react"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { ApiError, get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { flowOwnerName, type FlowReport } from "@/lib/network/flows"
import { foldHistoricalFlows, hourBounds, type ContainerTrafficDetail } from "@/lib/network/traffic"
import type { ContainerTraffic } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { ArrowUpRight, Box } from "@/components/icons"
import { Explain } from "@/components/explain"
import { WindowPicker } from "@/components/window-picker"
import { ChartPanel } from "@/components/chart-panel"
import { IdentityHeader } from "@/components/identity-header"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { containerHref, containerWindow, trafficHref } from "./address"
import { DetailPage, useSiblingSteps } from "@/components/detail-page"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { FactGroups, FactRowHeld } from "./facts"
import { WINDOW_WORDS, type RecordedWindow } from "./span"
import { decodeSegment } from "@/lib/route-params"
import { axisRate, formatRate, InOut, inOutSeries } from "@/components/in-out"

const SERIES = inOutSeries()
const WINDOW_OPTIONS = (["1h", "6h", "24h", "7d"] as const).map((value) => ({
  value,
  label: value,
}))
const CHART_HEIGHT = 220

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
  const name = decodeSegment(params.name) || null
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
  const step = useSiblingSteps(
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
    <DetailPage
      name="Container traffic"
      what="container"
      stateKey="network.traffic.container"
      refresh={() => {
        list.refresh()
        detail.refresh()
      }}
      steps={step}
      rail={
        <Rail
          slot="container-rail"
          label="Containers"
          rows={ranked}
          keyOf={(c) => c.name}
          current={(c) => c.name === name}
          loading={!list.data && !list.error}
          error={!list.data ? list.error : undefined}
          onRetry={list.refresh}
          search={{ placeholder: "Container name", query, onQuery: setQuery }}
          caption={
            list.data
              ? `${plural(ranked.length, "container")} · most moved in ${WINDOW_WORDS[range]}`
              : list.error
                ? "The containers could not be read"
                : "Reading the containers…"
          }
          empty={query.trim() ? "No container matches." : "No container moved anything."}
          row={(c, current) => (
            <RailRow
              item={c.name}
              name={c.name}
              label={`${c.name}, ${bytes(c.rxBytes + c.txBytes)}`}
              current={current}
              href={containerHref(c.name, range)}
              mark={
                <ProductLogo
                  id={imageProduct(c.name)}
                  size="sm"
                  fallback={Box}
                  className="size-7 rounded-md [&_img]:size-4"
                />
              }
              end={
                <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
                  {bytes(c.rxBytes + c.txBytes)}
                </span>
              }
            >
              <RailName current={current} mono>
                {c.name}
              </RailName>
              <RailLine>
                <InOut rx={c.rxRate} tx={c.txRate} unit="rate" plain className="font-sans" />
              </RailLine>
            </RailRow>
          )}
        />
      }
      back={{ href: trafficHref({ span: range === "1h" ? "live" : range }), label: "Traffic" }}
      title={name ? `${name} traffic` : "Container traffic"}
      identity={
        <ContainerHeader
          name={name}
          range={range}
          detail={detail.data}
          missing={detail.error instanceof ApiError && detail.error.status === 404}
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
    </DetailPage>
  )
}

function ContainerHeader({
  name,
  range,
  detail: d,
  missing,
}: {
  name: string | null
  range: RecordedWindow
  detail: ContainerTrafficDetail | undefined
  missing: boolean
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
      {name !== null && (
        <IdentityHeader
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
                    <span key="image">
                      <span className="font-mono">{d.image ?? "image unknown"}</span>
                    </span>,
                    <span key="service">
                      <span className={d.project ? "font-mono" : undefined}>{service}</span>
                    </span>,
                  ],
                  [
                    d.networks.length > 0 && (
                      <span key="networks">
                        on <span className="font-mono">{d.networks.join(", ")}</span>
                      </span>
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
    <WindowPicker
      label="How far back the chart reaches"
      windows={WINDOW_OPTIONS}
      value={range}
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
                    <InOut
                      rx={d.rxBytes}
                      tx={d.txBytes}
                      unit="bytes"
                      plain
                      className="text-foreground"
                    />{" "}
                    in {WINDOW_WORDS[range]}
                  </span>{" "}
                  <span className="max-sm:hidden">·</span>{" "}
                  <span>
                    now <InOut rx={d.rxRate} tx={d.txRate} unit="rate" plain />
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
          title={
            <span className="inline-flex items-center gap-1.5">
              Who it talked to
              <Explain label="Where these peers come from">
                The socket history&rsquo;s rows for this container&rsquo;s exact id. TCP bytes are
                native counter deltas, UDP has none, and a socket that opened and closed between two
                samples is not in it.
              </Explain>
            </span>
          }
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
                    // UDP has no byte counters: said as a word, not as a coloured figure.
                    p.rxBytes === null && p.txBytes === null ? (
                      <span className="text-hint text-muted-foreground">not counted</span>
                    ) : (
                      // In the chart's units, so the page says its bytes one way.
                      <InOut rx={p.rxBytes} tx={p.txBytes} unit="bytes" />
                    )
                  }
                />
              ))}
            </RowList>
          )}
          {admin && flows.data && (peers.length > 20 || flows.data.truncated) && (
            <p className="pt-3 text-hint text-muted-foreground">
              {peers.length > 20 && `The first 20 of ${peers.length}.`}
              {flows.data.truncated && " More rows were retained than are shown."}
            </p>
          )}
        </PanelBody>
      </Panel>
    </>
  )
}
