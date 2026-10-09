"use client"

import { useMemo, useState } from "react"
import { get, put, del } from "@/lib/api"
import { bytes, plural, rate, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGEndpointEvidence, WGEvent, WGHistory, WGInterface, WGPeer } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Detail, DetailList } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Field } from "@/components/form"
import { Meter, utilisationTone } from "@/components/meter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useConfirm } from "@/components/confirm-dialog"
import { RX, TX } from "@/components/network/rate-pair"
import { NetworkReadWarning } from "@/components/network/read-warning"
import {
  HISTORY_WINDOWS,
  type HistoryWindow,
  VERDICT_LABEL,
  eventLabel,
  outcomeTone,
  parseBudget,
  quotaPercent,
  verdictTone,
} from "./record-logic"

const TREND_SERIES = [
  { key: "rx", label: "From it", color: RX, kind: "area" as const },
  { key: "tx", label: "To it", color: TX, kind: "area" as const },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`
const iso = (unix: number) => new Date(unix * 1000).toISOString()

/**
 * What needs a look on a tunnel, read with it: an always-on peer whose
 * handshake went stale, a transport routed into a tunnel, a budget passed.
 * Each says what it is; none of them acts.
 */
export function TunnelAlerts({ tunnel }: { tunnel: WGInterface }) {
  const alerts = tunnel.alerts ?? []
  if (alerts.length === 0) return null
  return (
    <Notice
      tone="warning"
      title={`${plural(alerts.length, "thing")} on ${tunnel.name} ${alerts.length === 1 ? "needs" : "need"} a look`}
      className="mb-5"
    >
      <ul className="space-y-1" aria-label={`${tunnel.name} alerts`}>
        {alerts.map((a) => (
          <li key={`${a.kind}-${a.peer ?? ""}`}>
            {a.message}
            {a.since ? (
              <span className="text-muted-foreground"> · since {relativeTime(iso(a.since))}</span>
            ) : null}
          </li>
        ))}
      </ul>
    </Notice>
  )
}

function WindowToggle({
  value,
  onChange,
}: {
  value: HistoryWindow
  onChange: (next: HistoryWindow) => void
}) {
  return (
    <ToggleGroup
      type="single"
      value={value}
      onValueChange={(next) => next && onChange(next as HistoryWindow)}
      variant="outline"
      size="sm"
      aria-label="How far back the trend reaches"
    >
      {HISTORY_WINDOWS.map((w) => (
        <ToggleGroupItem key={w} value={w} className="px-2.5 text-hint">
          {w}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/** One record: a trend, read from the server's samples. */
function useHistory(tunnel: string, peer: string | undefined, span: HistoryWindow) {
  return usePoll<WGHistory>(
    (signal) =>
      get(
        `/network/vpn/wireguard/${encodeURIComponent(tunnel)}/history`,
        { window: span, ...(peer ? { peer } : {}) },
        signal,
      ),
    60_000,
    [tunnel, peer, span],
  )
}

function Trend({ history, title }: { history: WGHistory | undefined; title: string }) {
  const rows = useMemo(
    () => (history?.points ?? []).map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx })),
    [history],
  )
  if (history && history.recording === false) {
    return (
      <Notice title="Nothing is recorded">
        The metrics retention is zero (JD_METRICS_RETENTION), so no WireGuard traffic history is
        kept. Lifecycle events are recorded either way.
      </Notice>
    )
  }
  return (
    <ChartPanel
      plain
      title={title}
      rows={rows}
      series={TREND_SERIES}
      format={formatRate}
      axisFormat={axisRate}
      showPeaks={false}
      height={160}
      note="Nothing recorded for this window yet; the record reads every five minutes."
    />
  )
}

/** A tunnel's or a peer's lifecycle, newest first, each entry with its outcome. */
export function EventList({ events, label }: { events: WGEvent[]; label: string }) {
  if (events.length === 0)
    return <p className="text-hint text-muted-foreground">Nothing recorded yet.</p>
  return (
    <RowList aria-label={label}>
      {events.map((e) => (
        <Row
          key={e.id}
          title={
            <>
              {eventLabel(e.kind)}
              {e.peerName ? <span className="text-muted-foreground"> · {e.peerName}</span> : null}
            </>
          }
          subtitle={[e.detail, e.actor && `by ${e.actor}`].filter(Boolean).join(" · ")}
          trailing={
            <>
              <Status tone={outcomeTone(e.outcome)} label={e.outcome} />
              <time dateTime={iso(e.at)} className="text-hint text-muted-foreground">
                {relativeTime(iso(e.at))}
              </time>
            </>
          }
        />
      ))}
    </RowList>
  )
}

/**
 * A peer's record in its sheet: traffic over time, every address it was seen
 * dialling from, its usage budget and what was done to it.
 */
export function PeerRecord({
  tunnel,
  peer,
  onChanged,
}: {
  tunnel: WGInterface
  peer: WGPeer
  onChanged: () => void
}) {
  const [span, setSpan] = useState<HistoryWindow>("24h")
  const history = useHistory(tunnel.name, peer.publicKey, span)
  const endpoints = history.data?.endpoints ?? []
  const events = history.data?.events ?? []
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex items-center justify-between gap-3">
          <p className="text-title font-medium">Traffic</p>
          <WindowToggle value={span} onChange={setSpan} />
        </div>
        {history.error && !history.data ? (
          <ErrorState error={history.error} onRetry={history.refresh} />
        ) : !history.data ? (
          <LoadingRows rows={2} />
        ) : (
          <>
            <NetworkReadWarning
              error={history.error}
              refresh={history.refresh}
              lastSuccess={history.lastSuccess}
              reading="WireGuard record"
            />
            <Trend history={history.data} title={`${peer.name || peer.address} · ${span}`} />
          </>
        )}
      </div>
      {peer.transport && (
        <DetailList>
          <Detail label="Transport">
            <span className="flex flex-wrap items-center gap-2">
              <Status
                tone={peer.transport.state === "native" ? "running" : "warning"}
                label={
                  peer.transport.state === "native"
                    ? `through ${peer.transport.device}`
                    : peer.transport.state
                }
              />
              <span className="text-hint text-muted-foreground">
                checked {relativeTime(iso(peer.transport.checkedAt))}
              </span>
            </span>
            {peer.transport.reason && (
              <p className="text-hint text-muted-foreground">{peer.transport.reason}</p>
            )}
          </Detail>
        </DetailList>
      )}
      <div className="flex min-w-0 flex-col gap-2">
        <p className="text-title font-medium">Seen from</p>
        {endpoints.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            No endpoint recorded yet: one appears once the peer dials in.
          </p>
        ) : (
          <RowList aria-label={`Where ${peer.name || peer.address} was seen from`}>
            {endpoints.map((e) => (
              <Row
                key={e.endpoint}
                title={<span className="font-mono">{e.endpoint}</span>}
                subtitle={`first ${relativeTime(iso(e.firstSeen))} · ${plural(e.observations, "reading")}`}
                trailing={
                  <time dateTime={iso(e.lastSeen)} className="text-hint text-muted-foreground">
                    last {relativeTime(iso(e.lastSeen))}
                  </time>
                }
              />
            ))}
          </RowList>
        )}
      </div>
      {peer.id > 0 && tunnel.managed && (
        <PeerBudget tunnel={tunnel} peer={peer} onChanged={onChanged} />
      )}
      <div className="flex min-w-0 flex-col gap-2">
        <p className="text-title font-medium">History</p>
        <EventList events={events} label={`${peer.name || peer.address} history`} />
      </div>
    </div>
  )
}

/**
 * A usage budget: a threshold the record measures the peer's traffic
 * against, in UTC calendar periods. It alerts; it never disconnects, because
 * a background cut could take the operator's own way in with it.
 */
function PeerBudget({
  tunnel,
  peer,
  onChanged,
}: {
  tunnel: WGInterface
  peer: WGPeer
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [period, setPeriod] = useState<"day" | "week" | "month">(peer.quota?.period ?? "month")
  const [limit, setLimit] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const base = `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers/${peer.id}/quota`
  const parsed = parseBudget(limit)
  const problem = limit.trim() && !parsed ? "Use a figure in MB, GB or TB, at least 1 MB." : ""
  const save = async () => {
    if (!parsed) return
    setBusy(true)
    setError(undefined)
    try {
      await put(base, { period, limitBytes: parsed })
      notify.success(`${peer.name}'s budget is ${bytes(parsed)} a ${period}`)
      setLimit("")
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const clear = () =>
    confirm({
      title: `Clear ${peer.name}'s budget`,
      confirmLabel: "Clear",
      description: <p>Its recorded usage stays; only the threshold and its alert go.</p>,
      action: async () => {
        await del(base)
        notify.success("Budget cleared")
        onChanged()
      },
    })
  const q = peer.quota
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="text-title font-medium">Budget</p>
      {q && (
        <div className="space-y-1.5" aria-label={`${peer.name} budget`}>
          <div className="flex items-baseline justify-between gap-3 text-body">
            <span className="numeric">
              {q.state === "unmeasured"
                ? "Not measured"
                : `${bytes(q.usedBytes)} of ${bytes(q.limitBytes)}`}
            </span>
            <span className="text-hint text-muted-foreground">
              this {q.period}
              {q.state === "exceeded" ? " · passed" : q.state === "warning" ? " · over 80%" : ""}
            </span>
          </div>
          {q.state !== "unmeasured" && (
            <Meter
              value={quotaPercent(q)}
              tone={q.state === "exceeded" ? "danger" : utilisationTone(quotaPercent(q))}
              mark={80}
              label={`${peer.name} budget used`}
            />
          )}
          <p className="text-hint text-muted-foreground">
            An alert, not a limit: the peer stays connected past it.
          </p>
          <Button size="xs" variant="outline" onClick={clear}>
            Clear the budget
          </Button>
        </div>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Per">
          <ToggleGroup
            type="single"
            value={period}
            onValueChange={(next) => next && setPeriod(next as typeof period)}
            variant="outline"
            size="sm"
            aria-label="Budget period"
          >
            {(["day", "week", "month"] as const).map((p) => (
              <ToggleGroupItem key={p} value={p} className="px-2.5 text-hint">
                {p}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
        <Field label={q ? "New budget" : "Budget"} htmlFor={`budget-${peer.id}`} error={problem}>
          <Input
            id={`budget-${peer.id}`}
            value={limit}
            placeholder="50 GB"
            onChange={(event) => setLimit(event.target.value)}
            aria-invalid={Boolean(problem)}
            className="w-32 font-mono"
          />
        </Field>
        <Button size="sm" onClick={() => void save()} disabled={!parsed || busy} pending={busy}>
          Set budget
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
      {dialog}
    </div>
  )
}

/**
 * A tunnel's record in a sheet: what this host can say about the endpoint its
 * clients dial, the tunnel's traffic over time, and its lifecycle — every
 * creation, change, failure and recovery, kept after the tunnel is removed.
 */
export function TunnelRecord({
  tunnel,
  open,
  onOpenChange,
}: {
  tunnel: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [span, setSpan] = useState<HistoryWindow>("24h")
  const history = useHistory(tunnel, undefined, span)
  const [evidence, setEvidence] = useState<WGEndpointEvidence>()
  const [checking, setChecking] = useState(false)
  const check = async () => {
    setChecking(true)
    try {
      setEvidence(
        await get<WGEndpointEvidence>(
          `/network/vpn/wireguard/${encodeURIComponent(tunnel)}/endpoint`,
        ),
      )
    } catch (err) {
      notify.error("The endpoint could not be checked", err)
    } finally {
      setChecking(false)
    }
  }
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title={`${tunnel} history`}
      description={`The endpoint, traffic and lifecycle of ${tunnel}`}
      initialFocus="body"
    >
      <div className="flex min-w-0 flex-col gap-6">
        <div className="flex min-w-0 flex-col gap-2">
          <div className="flex items-center justify-between gap-3">
            <p className="text-title font-medium">Endpoint</p>
            <Button size="xs" variant="outline" onClick={() => void check()} pending={checking}>
              Check endpoint
            </Button>
          </div>
          {evidence ? (
            <EndpointEvidence evidence={evidence} />
          ) : (
            <p className="text-hint text-muted-foreground">
              Resolves the address clients are given and compares it with what this host holds.
              Nothing is sent from outside.
            </p>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          <div className="flex items-center justify-between gap-3">
            <p className="text-title font-medium">Traffic</p>
            <WindowToggle value={span} onChange={setSpan} />
          </div>
          {history.error && !history.data ? (
            <ErrorState error={history.error} onRetry={history.refresh} />
          ) : !history.data ? (
            <LoadingRows rows={2} />
          ) : (
            <>
              <NetworkReadWarning
                error={history.error}
                refresh={history.refresh}
                lastSuccess={history.lastSuccess}
                reading="WireGuard record"
              />
              <Trend history={history.data} title={`${tunnel} · every peer · ${span}`} />
            </>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-title font-medium">Lifecycle</p>
          <EventList events={history.data?.events ?? []} label={`${tunnel} lifecycle`} />
        </div>
      </div>
    </SidePanel>
  )
}

function EndpointEvidence({ evidence }: { evidence: WGEndpointEvidence }) {
  return (
    <div className="space-y-2" aria-label="Endpoint evidence">
      <Status tone={verdictTone(evidence.verdict)} label={VERDICT_LABEL[evidence.verdict]} />
      <p className="text-hint text-muted-foreground">{evidence.explanation}</p>
      <DetailList>
        <Detail label="Endpoint">
          <span className="font-mono">{evidence.endpoint}</span>
        </Detail>
        {evidence.addresses.map((a) => (
          <Detail key={a.address} label={evidence.kind === "hostname" ? "Resolves to" : "Address"}>
            <span className="font-mono">{a.address}</span>
            <span className="text-muted-foreground">
              {" · "}
              {a.onHost ? `held on ${a.device}` : "not held here"}
              {a.public ? " · public" : " · private"}
            </span>
          </Detail>
        ))}
        <Detail label="UDP port">
          {evidence.port} ·{" "}
          {evidence.listening ? "a socket holds it here" : "nothing holds it here"}
        </Detail>
      </DetailList>
    </div>
  )
}
