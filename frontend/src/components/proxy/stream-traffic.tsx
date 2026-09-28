"use client"

import { useMemo, useState } from "react"
import { Connection, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, plural } from "@/lib/format"
import type {
  StreamEntry,
  StreamResult,
  StreamSessions,
  StreamTraffic,
  StreamWindow,
} from "@/lib/types"
import {
  STREAM_STATUSES,
  accessOf,
  ruleableClient,
  saveOutcome,
  sessionLength,
  streamBody,
  streamSpecOf,
  withClientRule,
} from "@/lib/streams"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { BarList, type BarListItem } from "@/components/bar-list"
import { useConfirm } from "@/components/confirm-dialog"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * What a stream carried, and who is on it now.
 *
 * A raw forward has no request line, no path and no user agent — only who
 * connected, what nginx did with them, how long they stayed and how much
 * crossed. That is what the stream's own log holds, one line per finished
 * session, and it is enough to answer the questions an open port raises:
 * is anyone using this, who is being turned away, and which address is
 * hammering it. The busiest addresses carry a Deny (or an Allow, for one
 * the list turns away) that saves the stream with that rule first.
 */

/** Module constant: ChartPanel is memoised on its props. */
const SESSION_SERIES: Series[] = [
  { key: "sessions", label: "Sessions", color: "var(--chart-1)", kind: "area" },
  { key: "denied", label: "Denied", color: "var(--warning)", kind: "line" },
  { key: "failed", label: "Not forwarded", color: "var(--destructive)", kind: "line" },
]

const WINDOWS: { value: StreamWindow; label: string }[] = [
  { value: "1h", label: "Hour" },
  { value: "24h", label: "Day" },
  { value: "7d", label: "Week" },
]

type View = "traffic" | "clients" | "now"

const count = (value: number) => Math.round(value).toLocaleString()

export function StreamTrafficPanel({
  stream,
  open,
  onOpenChange,
  admin,
  live,
  onSaved,
}: {
  stream: StreamEntry | null
  open: boolean
  onOpenChange: (open: boolean) => void
  admin: boolean
  /** nginx reads the stream directory, so a save reloads it. */
  live: boolean
  onSaved: () => void
}) {
  const [view, setView] = useState<View>("traffic")
  const [span, setSpan] = useState<StreamWindow>("1h")
  const name = stream?.name ?? ""
  const traffic = usePoll<StreamTraffic>(
    (signal) => get(`/proxy/streams/${encodeURIComponent(name)}/traffic`, { window: span }, signal),
    60_000,
    [name, span],
    { enabled: open && name !== "" },
  )
  // Sockets change by the second, so the list is asked for often, and only
  // while it is on screen.
  const sessions = usePoll<StreamSessions>(
    (signal) => get(`/proxy/streams/${encodeURIComponent(name)}/sessions`, undefined, signal),
    10_000,
    [name],
    { enabled: open && name !== "" && view === "now" },
  )

  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      width="lg"
      title={
        <>
          <ProductLogo id={portProduct(stream?.listen ?? 0)} size="sm" fallback={Connection} />
          {name}
        </>
      }
      description={`Sessions, clients and live connections of ${name}`}
    >
      <div className="flex flex-wrap items-end justify-between gap-3 border-b border-hairline">
        <div role="tablist" aria-label="Traffic views" className="flex h-9">
          {(
            [
              ["traffic", "Traffic"],
              ["clients", "Clients"],
              ["now", "Connected now"],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={view === key}
              onClick={() => setView(key)}
              className={tabClasses(view === key, "h-9")}
            >
              {label}
            </button>
          ))}
        </div>
        {view !== "now" && (
          <ToggleGroup
            type="single"
            value={span}
            onValueChange={(v) => v && setSpan(v as StreamWindow)}
            variant="outline"
            size="sm"
            aria-label="Window"
            className="mb-1.5"
          >
            {WINDOWS.map((w) => (
              <ToggleGroupItem key={w.value} value={w.value} className="text-hint">
                {w.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        )}
      </div>

      <div className="space-y-6 pt-4">
        {view === "now" ? (
          <ConnectedNow name={name} state={sessions} />
        ) : traffic.error && !traffic.data ? (
          <ErrorState error={traffic.error} onRetry={traffic.refresh} />
        ) : !traffic.data || traffic.data.window !== span || traffic.data.name !== name ? (
          <LoadingPanel />
        ) : (
          <>
            <LogState traffic={traffic.data} />
            {view === "traffic" ? (
              <TrafficView traffic={traffic.data} />
            ) : (
              <ClientsView
                traffic={traffic.data}
                stream={stream}
                admin={admin}
                live={live}
                onSaved={() => {
                  onSaved()
                  traffic.refresh()
                }}
              />
            )}
          </>
        )}
      </div>
    </SidePanel>
  )
}

/** Why the figures below are empty, or only a floor. */
function LogState({ traffic }: { traffic: StreamTraffic }) {
  if (traffic.unreadable) {
    return (
      <Notice tone="warning" icon={Warning} title="The log could not be read">
        <p className="break-words">{traffic.unreadable}</p>
      </Notice>
    )
  }
  if (!traffic.logging) {
    return (
      <Notice title="This stream does not log its sessions">
        Turn on <span className="font-medium">Log connections</span> in its form, and nginx writes
        one line per session to <code className="font-mono">{traffic.path}</code>.
        {traffic.sessions > 0 && " What is below is from when it did."}
      </Notice>
    )
  }
  if (!traffic.complete) {
    return (
      <Notice
        tone="warning"
        icon={Warning}
        title="Only the most recent part of the window was read"
      >
        The log is larger than one reading takes, or older lines are compressed, so every figure is
        a floor
        {traffic.oldest ? `, counted from ${new Date(traffic.oldest * 1000).toLocaleString()}` : ""}
        .
      </Notice>
    )
  }
  return null
}

function TrafficView({ traffic }: { traffic: StreamTraffic }) {
  const rows: ChartRowLike[] = useMemo(
    () =>
      traffic.buckets.map((b) => ({
        ts: b.start * 1000,
        sessions: b.sessions,
        denied: b.denied,
        failed: b.failed,
      })),
    [traffic.buckets],
  )
  const statuses: BarListItem[] = useMemo(() => {
    const entries = Object.entries(traffic.statuses).sort((a, b) => b[1] - a[1])
    const top = entries[0]?.[1] ?? 1
    return entries.map(([code, n]) => ({
      key: code,
      label: code,
      hint: STREAM_STATUSES[code] ?? "",
      value: count(n),
      share: n / top,
      signal: code === "200" ? 0 : 1,
      tone: code === "403" || code === "400" ? "warning" : code === "200" ? "default" : "danger",
    }))
  }, [traffic.statuses])

  if (traffic.sessions === 0) {
    return <EmptyNote className="py-6">No session ended in this window.</EmptyNote>
  }
  return (
    <>
      <StatGrid columns={4} dense>
        <StatTile label="Sessions" value={count(traffic.sessions)} />
        <StatTile
          label="Denied"
          value={count(traffic.denied)}
          tone={traffic.denied > 0 ? "warning" : "default"}
        />
        <StatTile
          label="Not forwarded"
          value={count(traffic.failed)}
          tone={traffic.failed > 0 ? "danger" : "default"}
        />
        <StatTile
          label="Transferred"
          value={bytes(traffic.bytesIn + traffic.bytesOut)}
          hint={`${bytes(traffic.bytesIn)} in · ${bytes(traffic.bytesOut)} out`}
        />
      </StatGrid>
      <ChartPanel
        plain
        title="Sessions"
        rows={rows}
        series={SESSION_SERIES}
        height={140}
        format={count}
      />
      <Panel plain>
        <PanelHeader title="Outcome" />
        <PanelBody flush>
          <BarList items={statuses} />
        </PanelBody>
      </Panel>
      <StatGrid columns={4} dense>
        <StatTile label="Median session" value={sessionLength(traffic.duration.p50)} />
        <StatTile label="p90" value={sessionLength(traffic.duration.p90)} />
        <StatTile label="p99" value={sessionLength(traffic.duration.p99)} />
        <StatTile label="Longest" value={sessionLength(traffic.duration.max)} />
      </StatGrid>
    </>
  )
}

function ClientsView({
  traffic,
  stream,
  admin,
  live,
  onSaved,
}: {
  traffic: StreamTraffic
  stream: StreamEntry | null
  admin: boolean
  live: boolean
  onSaved: () => void
}) {
  const { confirm, dialog } = useConfirm()
  // A rule is a save of the stream's form, so only a stream the form can
  // save over takes one.
  const editable =
    admin && stream !== null && !stream.error && !stream.paused && stream.unsupported.length === 0

  const addRule = (address: string, action: "allow" | "deny") => {
    if (!stream) return
    let result: StreamResult | undefined
    confirm({
      title: `${action === "deny" ? "Deny" : "Allow"} ${address}`,
      confirmLabel: live ? `${action === "deny" ? "Deny" : "Allow"} and reload` : "Save",
      description: (
        <p>
          {action === "deny"
            ? `${address} is turned away from port ${stream.listen}, ahead of every other rule. Connections it has open now stay until they end.`
            : `${address} may connect to port ${stream.listen}, ahead of every other rule — including a deny that covers it.`}{" "}
          The rule goes first in the stream&rsquo;s access list, where the form can move or remove
          it.
        </p>
      ),
      action: async () => {
        const access = withClientRule(accessOf(stream), action, address)
        result = await post<StreamResult>("/proxy/streams/", {
          spec: streamBody(streamSpecOf(stream), access),
          previous: stream.name,
          reload: live,
        })
        onSaved()
      },
      onDone: () => {
        if (!result) return
        const outcome = saveOutcome(result, live)
        notify[outcome.tone](outcome.title, { description: outcome.description })
      },
    })
  }

  const top = traffic.clients[0]?.sessions ?? 1
  const items: BarListItem[] = traffic.clients.map((c) => ({
    key: c.address,
    label: c.address,
    value: count(c.sessions),
    share: c.sessions / top,
    signal: (c.denied + c.failed) / c.sessions,
    tone: c.denied > 0 ? "warning" : c.failed > 0 ? "danger" : "default",
    hint: [
      bytes(c.bytes),
      c.denied > 0 && `${count(c.denied)} denied`,
      c.failed > 0 && `${count(c.failed)} not forwarded`,
    ]
      .filter(Boolean)
      .join(" · "),
    title: `${plural(c.sessions, "session")}, last ${new Date(c.last * 1000).toLocaleString()}`,
    trailing:
      editable && ruleableClient(c.address) ? (
        <Button
          size="sm"
          variant="ghost"
          onClick={() => addRule(c.address, c.denied === c.sessions ? "allow" : "deny")}
        >
          {c.denied === c.sessions ? "Allow" : "Deny"}
        </Button>
      ) : undefined,
  }))

  return (
    <Panel plain>
      <PanelHeader title="Busiest clients" />
      <PanelBody flush>
        <BarList items={items} emptyLabel="No client connected in this window." />
      </PanelBody>
      {dialog}
    </Panel>
  )
}

function ConnectedNow({ name, state }: { name: string; state: PollState<StreamSessions> }) {
  const { error, refresh } = state
  // A reading of the stream this panel showed before is not this one's.
  const data = state.data?.name === name ? state.data : undefined
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return <LoadingPanel />
  if (data.paused) {
    return <EmptyNote className="py-6">Paused: nginx is not reading this stream.</EmptyNote>
  }
  if (data.udpOnly) {
    return (
      <EmptyNote className="py-6">
        nginx answers every UDP client from its one listening socket, so there is no connection per
        client to list. The Traffic view counts UDP sessions as they end.
      </EmptyNote>
    )
  }
  return (
    <Panel plain>
      <PanelHeader
        title={
          data.total === 0
            ? "No TCP connection open"
            : `${plural(data.total, "TCP connection")} open`
        }
      />
      <PanelBody flush>
        {data.sessions.length === 0 ? (
          <EmptyNote className="py-6">
            Nobody is connected to port {data.listen} right now.
          </EmptyNote>
        ) : (
          <RowList>
            {data.sessions.map((s) => (
              <Row
                key={`${s.client}-${s.local}`}
                title={s.client}
                subtitle={`to ${s.local}`}
                mono
              />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}
