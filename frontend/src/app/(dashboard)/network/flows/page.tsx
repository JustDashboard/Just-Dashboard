"use client"

import { useEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, post, put, del, errorMessage } from "@/lib/api"
import {
  flowBytes,
  flowCounter,
  flowDateRange,
  flowOwnerName,
  flowPolicyProblem,
  flowReading,
  flowTotals,
  flowIsKernelRow,
  flowKernelTotals,
  yesterdayUTC,
  type FlowReport,
  type FlowSettings,
  type FlowRow,
} from "@/lib/network-flows"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { ErrorState, LoadingPanel, Notice, EmptyNote } from "@/components/state"
import { Field, FieldRow, Disclosure } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { KernelObserverPanel, ObserverQualityReading } from "@/components/network/kernel-observer"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table"

type Filters = { day: string; address: string; containerId: string }
/** Reading register: measured socket history, its exact owner evidence and its gaps. */
export default function NetworkFlowsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = can("destructive")
  const [draft, setDraft] = useState<Filters>({ day: yesterdayUTC(), address: "", containerId: "" })
  const [filters, setFilters] = useState(draft)
  const range = flowDateRange(filters.day)
  const query = { ...range, address: filters.address, containerId: filters.containerId }
  const read = usePoll<FlowReport>(
    (signal) => get("/network/flows/", query, signal),
    30_000,
    [admin, filters.day, filters.address, filters.containerId],
    { enabled: admin && Boolean(range) },
  )
  const [action, setAction] = useState<"recording" | "export" | "observer" | null>(null)
  const busy = action !== null
  const [actionError, setActionError] = useState("")
  const { confirm, dialog } = useConfirm()
  const data = read.data
  const disabled = busy || Boolean(read.error) || !data
  const observerRetained = Boolean(
    data?.settings.kernelObserverEnabled || data?.kernelObserver.attachmentsRetained,
  )
  const totals = flowTotals(data?.rows ?? [])
  const observed = flowKernelTotals(data?.rows ?? [])
  const reading = flowReading(data?.status ?? "unavailable")
  const mutationState = useRef({ data, disabled, destructive })
  useEffect(() => {
    mutationState.current = { data, disabled, destructive }
  }, [data, disabled, destructive])
  const reviewObserver = (enabled: boolean) => {
    if (!data || disabled || !destructive) return
    confirm({
      title: enabled ? "Activate kernel packet observer" : "Stop kernel packet observer",
      description: enabled
        ? `Observe TCP and UDP transport payload metadata at the declared cgroup hooks. Peer and owner evidence is retained for up to ${data.settings.retentionDays} days with bounded event and storage budgets. Missing events, bytes and owners remain unknown. Restarting the dashboard will leave the observer off.`
        : "Drain retained observations and detach this session's owned programs. Failed cleanup retains the actual attachment state for inspection and retry. The final shutdown tail can remain unknown.",
      confirmLabel: enabled ? "Activate observer" : "Stop observer",
      action: async () => {
        const latest = mutationState.current
        if (!latest.data || latest.disabled || !latest.destructive)
          throw new Error("Inspect a fresh recorder state before changing the observer.")
        if (enabled && !latest.data.settings.enabled)
          throw new Error("Start history recording before activating the observer.")
        setAction("observer")
        setActionError("")
        try {
          await post("/network/flows/observer", { enabled })
          return "reported"
        } catch (err) {
          setActionError(`${errorMessage(err)} Inspect observer state before trying again.`)
          throw err
        } finally {
          read.refresh()
          setAction(null)
        }
      },
    })
  }
  const changeRecording = async () => {
    if (!data || disabled || (data.settings.enabled && observerRetained)) return
    setAction("recording")
    setActionError("")
    try {
      await post<FlowSettings>("/network/flows/recording", { enabled: !data.settings.enabled })
      read.refresh()
    } catch (err) {
      setActionError(`${errorMessage(err)} Inspect recorder state before trying again.`)
      read.refresh()
    } finally {
      setAction(null)
    }
  }
  const exportRows = async () => {
    setAction("export")
    setActionError("")
    try {
      const artifact = await get<FlowReport>("/network/flows/export", query)
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(artifact)], { type: "application/json" }),
      )
      const link = document.createElement("a")
      link.href = url
      link.download = `socket-observations-${filters.day}.json`
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setAction(null)
    }
  }
  return (
    <Page className="animate-rise">
      <PageContext title="Socket history" />
      {!admin ? (
        <Notice title="Socket history needs the admin capability">
          Peer and descriptor-owner history can expose private workload connections.
        </Notice>
      ) : (
        <>
          <Panel plain>
            <PanelHeader
              title="Native socket recorder"
              actions={
                <div className="flex flex-wrap items-center gap-3">
                  <Status
                    tone={read.error ? "warning" : reading.tone}
                    label={read.error ? "Last known recorder state" : reading.label}
                  />
                  <Button size="sm" variant="outline" onClick={read.refresh}>
                    Inspect again
                  </Button>
                  {data && (
                    <Button
                      size="sm"
                      variant="outline"
                      pending={action === "recording"}
                      disabled={disabled || (data.settings.enabled && observerRetained)}
                      onClick={changeRecording}
                    >
                      {data.settings.enabled ? "Stop recording" : "Start recording"}
                    </Button>
                  )}
                </div>
              }
            />
            {data && (
              <PanelBody>
                <div className="space-y-2 text-body text-muted-foreground">
                  <p>
                    Read <Time value={data.checkedAt} /> · Recording setting{" "}
                    {data.settings.enabled ? "on" : "off"} · Sample interval{" "}
                    {data.settings.intervalSeconds}s · Retention {data.settings.retentionDays} days
                  </p>
                  {data.settings.enabled && observerRetained && (
                    <p>Stop the kernel observer before stopping ordinary history recording.</p>
                  )}
                  <p>
                    {data.recordingSince ? (
                      <>
                        Recording enabled since <Time value={data.recordingSince} />.
                      </>
                    ) : (
                      "Recording has not been enabled. Historical totals are unavailable."
                    )}{" "}
                    Collector started <Time value={data.collectorStartedAt} />; every restart begins
                    a fresh byte baseline.
                  </p>
                  <p>
                    Retained socket-hour rows: {data.retainedRows} · Payload{" "}
                    {flowBytes(BigInt(data.retainedBytes))} · Rows pruned: {data.prunedRows}
                    {data.retainedFrom && (
                      <>
                        {" "}
                        · Earliest retained row <Time value={data.retainedFrom} />
                      </>
                    )}
                  </p>
                </div>
              </PanelBody>
            )}
          </Panel>
          <NetworkReadWarning
            error={read.error}
            refresh={read.refresh}
            lastSuccess={read.lastSuccess}
            reading="socket history"
          />
          {actionError && (
            <Notice tone="warning" title="Recorder action needs a fresh reading">
              <p>{actionError}</p>
            </Notice>
          )}
          <Panel plain>
            <PanelHeader title="History filters" />
            <PanelBody>
              <FieldRow columns={3}>
                <Field label="Day (UTC)" htmlFor="flow-day">
                  <Input
                    id="flow-day"
                    type="date"
                    value={draft.day}
                    onChange={(e) => setDraft({ ...draft, day: e.target.value })}
                  />
                </Field>
                <Field
                  label="Observed peer IP"
                  htmlFor="flow-peer"
                  hint="Literal IPv4 or IPv6; blank shows all peers"
                >
                  <Input
                    id="flow-peer"
                    value={draft.address}
                    onChange={(e) => setDraft({ ...draft, address: e.target.value })}
                  />
                </Field>
                <Field
                  label="Container identity"
                  htmlFor="flow-container"
                  hint="Full Docker ID; blank includes unknown owners"
                >
                  <Input
                    id="flow-container"
                    value={draft.containerId}
                    onChange={(e) => setDraft({ ...draft, containerId: e.target.value })}
                  />
                </Field>
              </FieldRow>
              <div className="mt-4 flex flex-wrap gap-3">
                <Button
                  variant="outline"
                  disabled={!flowDateRange(draft.day) || busy}
                  onClick={() =>
                    setFilters({
                      ...draft,
                      address: draft.address.trim(),
                      containerId: draft.containerId.trim(),
                    })
                  }
                >
                  Read period
                </Button>
                <Button
                  variant="outline"
                  disabled={disabled}
                  pending={action === "export"}
                  onClick={exportRows}
                >
                  Export bounded JSON
                </Button>
              </div>
            </PanelBody>
          </Panel>
          {!data ? (
            read.error ? (
              <ErrorState error={read.error} onRetry={read.refresh} />
            ) : (
              <LoadingPanel plain />
            )
          ) : (
            <>
              <Notice title="Separate measurement sources; incomplete flow coverage">
                <p>
                  Native snapshots leave UDP bytes and dropped-event counts unknown, and miss
                  connections that open and close between reads. Kernel packet observations retain
                  separate subtotals and coverage gaps. The two channels are never added together.
                  Empty results do not prove no contact or zero bandwidth.
                </p>
                {data.error && <p className="mt-2">{data.error}</p>}
              </Notice>
              <StatGrid columns={4}>
                <StatTile
                  label="Displayed TCP sent"
                  value={flowBytes(totals.tx.value)}
                  hint={`${totals.tx.known} rows with measured sent deltas`}
                />
                <StatTile
                  label="Displayed TCP received"
                  value={flowBytes(totals.rx.value)}
                  hint={`${totals.rx.known} rows with measured received deltas`}
                />
                <StatTile
                  label="Displayed retransmits"
                  value={totals.retrans.value?.toString() ?? "Unknown"}
                  hint="Sampled sender counter deltas"
                />
                <StatTile
                  label="Recorded sample hours"
                  value={data.coverageHours.length}
                  hint={`${filters.day} · UTC`}
                />
              </StatGrid>
              <KernelObserverPanel
                observer={data.kernelObserver}
                settings={data.settings}
                disabled={disabled}
                pending={action === "observer"}
                stale={Boolean(read.error)}
                canControl={destructive}
                onReview={reviewObserver}
              />
              {observed.rows > 0 && (
                <Panel plain>
                  <PanelHeader title="Displayed kernel packet observations" />
                  <PanelBody>
                    <StatGrid columns={3}>
                      <StatTile
                        label="Observed sent subtotal"
                        value={flowBytes(observed.tx.value)}
                        hint={`${observed.tx.known} rows with known payload bytes`}
                      />
                      <StatTile
                        label="Observed received subtotal"
                        value={flowBytes(observed.rx.value)}
                        hint={`${observed.rx.known} rows with known payload bytes`}
                      />
                      <StatTile
                        label="Observed packet subtotal"
                        value={observed.packets.value?.toString() ?? "Unknown"}
                        hint={`${observed.packets.known} counted rows · ${observed.packets.unknown} unknown`}
                      />
                    </StatGrid>
                    <p className="mt-3 text-body text-muted-foreground">
                      Known sent byte gaps {observed.txGaps.value?.toString() ?? "Unknown"} · Known
                      received byte gaps {observed.rxGaps.value?.toString() ?? "Unknown"} · Rows
                      without gap counts: sent {observed.txGaps.unknown}, received{" "}
                      {observed.rxGaps.unknown} · Rows without known sent bytes{" "}
                      {observed.tx.unknown} · Without known received bytes {observed.rx.unknown}.
                      Subtotals include only retained events with known transport payload lengths;
                      they are not complete host bandwidth or billing.
                    </p>
                  </PanelBody>
                </Panel>
              )}
              {data.truncated && (
                <Notice tone="warning" title="Displayed rows are capped">
                  The period has more matching rows. These figures cover only the rows below; narrow
                  the peer or container filter. Exports have separate row and byte caps.
                </Notice>
              )}
              <SocketRows rows={data.rows} />
              <Panel plain>
                <PanelHeader title="Recorded coverage for the selected period" />
                <PanelBody>
                  {data.coverageHours.length ? (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>UTC hour</TableHead>
                          <TableHead>Samples</TableHead>
                          <TableHead>Failed source reads</TableHead>
                          <TableHead>Capped source reads</TableHead>
                          <TableHead>Omitted sources</TableHead>
                          <TableHead>Skipped intervals</TableHead>
                          <TableHead>Kernel observer quality</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {data.coverageHours.map((hour) => (
                          <TableRow key={hour.hour}>
                            <TableCell>
                              <Time value={hour.hour} />
                            </TableCell>
                            <TableCell>{hour.samples}</TableCell>
                            <TableCell>{hour.failedSources}</TableCell>
                            <TableCell>{hour.truncatedSources}</TableCell>
                            <TableCell>{hour.omittedSources}</TableCell>
                            <TableCell>{hour.discardedIntervals}</TableCell>
                            <TableCell>
                              <Disclosure quiet summary="Retained observer gaps">
                                <ObserverQualityReading quality={hour.observerQuality} />
                              </Disclosure>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  ) : (
                    <EmptyNote>
                      No retained samples for this period. Recorder downtime and missing hours
                      remain unknown.
                    </EmptyNote>
                  )}
                </PanelBody>
              </Panel>
              <Panel plain>
                <PanelHeader title="Latest native sample" />
                <PanelBody>
                  {data.lastCycle ? (
                    <div className="space-y-3 text-body">
                      <p>
                        <Time value={data.lastCycle.at} /> →{" "}
                        <Time value={data.lastCycle.finishedAt} /> · Capture{" "}
                        {data.lastCycle.elapsedMillis}ms · Docker sources{" "}
                        {data.lastCycle.dockerStatus} · Omitted {data.lastCycle.omittedSources}
                      </p>
                      <p className="break-words text-muted-foreground">
                        {data.lastCycle.toolVersion ?? "ss version unknown"} · Kernel{" "}
                        {data.lastCycle.kernelRelease ?? "unknown"} · Version read{" "}
                        <Time value={data.lastCycle.toolVersionCheckedAt} />
                      </p>
                      {data.lastCycle.dockerError && <p>{data.lastCycle.dockerError}</p>}
                      {data.lastCycle.toolVersionError && <p>{data.lastCycle.toolVersionError}</p>}
                      {data.lastCycle.sources.map((source) => (
                        <div key={source.id} className="space-y-1 border-t border-hairline pt-3">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-medium">{source.name}</span>
                            <Status
                              tone={source.status === "observed" ? "running" : "unknown"}
                              label={source.status.replaceAll("_", " ")}
                            />
                          </div>
                          <p className="break-all text-muted-foreground">
                            Source {source.id} · Namespace {source.namespace ?? "unknown"} ·
                            Observed <Time value={source.observedAt} />
                          </p>
                          <p>
                            {source.tcp} TCP · {source.udp} UDP · Native TCP sent counters{" "}
                            {source.tcpTxCounters} · Received counters {source.tcpRxCounters} ·
                            Unknown owners {source.unverifiedOwners} · Missing identities{" "}
                            {source.identityUnavailable}
                            {source.truncated ? " · Socket or output cap reached" : ""}
                          </p>
                          {source.error && <p>{source.error}</p>}
                        </div>
                      ))}
                    </div>
                  ) : (
                    <EmptyNote>No native sample has been retained.</EmptyNote>
                  )}
                </PanelBody>
              </Panel>
              <Panel plain>
                <PanelHeader title="Measurement boundaries" />
                <PanelBody>
                  <div className="space-y-3 text-body">
                    <Disclosure quiet summary="Native source and counter coverage">
                      <ul className="list-disc space-y-2 pl-5 text-muted-foreground">
                        {data.coverage.map((line) => (
                          <li key={line}>{line}</li>
                        ))}
                      </ul>
                    </Disclosure>
                    {destructive && (
                      <PolicyEditor
                        key={`${data.settings.intervalSeconds}:${data.settings.retentionDays}`}
                        settings={data.settings}
                        disabled={disabled}
                        onChanged={read.refresh}
                      />
                    )}
                    {destructive && (
                      <Button
                        variant="outline"
                        disabled={disabled}
                        onClick={() =>
                          confirm({
                            title: "Erase socket history",
                            description:
                              "Stop any attached kernel observer, then delete all retained socket observations and coverage hours. Ordinary history keeps its current on/off setting and starts a fresh byte baseline.",
                            confirmLabel: "Erase history",
                            action: async () => {
                              await del("/network/flows/history")
                            },
                            onDone: read.refresh,
                          })
                        }
                      >
                        Erase retained history
                      </Button>
                    )}
                  </div>
                </PanelBody>
              </Panel>
            </>
          )}
        </>
      )}
      {dialog}
    </Page>
  )
}
function Time({ value }: { value: string }) {
  return (
    <time dateTime={value}>{new Date(value).toLocaleString("en-GB", { timeZone: "UTC" })} UTC</time>
  )
}
function SocketRows({ rows }: { rows: FlowRow[] }) {
  // This table owns its horizontal scrolling; the frame is its reading region.
  return (
    <Panel plain>
      <PanelHeader title="Observed socket-hour rows" />
      <PanelBody>
        {rows.length ? (
          <Table className="min-w-[960px]" containerClassName="max-h-[600px]">
            <TableHeader>
              <TableRow>
                <TableHead>Owner / protocol</TableHead>
                <TableHead>Local → observed peer</TableHead>
                <TableHead>Measurement source / bytes</TableHead>
                <TableHead>Observed UTC window</TableHead>
                <TableHead>Identity / coverage</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={`${row.id}:${row.hour}`}>
                  <TableCell>
                    <div className="space-y-1">
                      <p className="font-medium">{flowOwnerName(row.socket.owner)}</p>
                      <p>
                        {row.socket.protocol.toUpperCase()} · {row.socket.state}
                      </p>
                      <p className="max-w-56 break-all text-muted-foreground">
                        {row.socket.owner.containerId ??
                          row.socket.owner.reason ??
                          row.socket.owner.status}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell>
                    <p className="font-mono">
                      {row.socket.localEndpoint ||
                        `${row.socket.localAddress}:${row.socket.localPort}`}
                    </p>
                    <p className="font-mono">
                      → {row.socket.remoteAddress ? row.socket.remoteEndpoint : "Peer unknown"}
                    </p>
                  </TableCell>
                  <TableCell>
                    {flowIsKernelRow(row) ? (
                      <>
                        <p className="font-medium">Kernel transport payload</p>
                        <p>Sent subtotal {flowBytes(row.observedTxBytes)}</p>
                        <p>Received subtotal {flowBytes(row.observedRxBytes)}</p>
                        <p>
                          Observed SYN {flowCounter(row.observedSyn)?.toString() ?? "Unknown"} · FIN{" "}
                          {flowCounter(row.observedFin)?.toString() ?? "Unknown"} · RST{" "}
                          {flowCounter(row.observedRst)?.toString() ?? "Unknown"}
                        </p>
                        <p className="text-muted-foreground">
                          Observed flags; lifecycle completeness unknown
                        </p>
                      </>
                    ) : row.evidence ? (
                      <p>Unrecognized evidence source; byte interpretation unknown</p>
                    ) : (
                      <>
                        <p className="font-medium">Native TCP counter deltas</p>
                        <p>
                          Sent{" "}
                          {row.socket.protocol === "tcp"
                            ? flowBytes(row.txBytes)
                            : "Unavailable for UDP"}
                        </p>
                        <p>
                          Received{" "}
                          {row.socket.protocol === "tcp"
                            ? flowBytes(row.rxBytes)
                            : "Unavailable for UDP"}
                        </p>
                        <p>
                          Retransmits {flowCounter(row.retransmissions)?.toString() ?? "Unknown"}
                        </p>
                        <p>
                          Outstanding-loss gauge max{" "}
                          {flowCounter(row.lostGaugeMax)?.toString() ?? "Unknown"}
                        </p>
                      </>
                    )}
                  </TableCell>
                  <TableCell>
                    <p>
                      <Time value={row.firstSeen} />
                    </p>
                    <p>
                      <Time value={row.lastSeen} />
                    </p>
                    <p className="text-muted-foreground">
                      {row.timestampUncertain
                        ? "UTC timestamp uncertain; inspect observer clock evidence"
                        : flowIsKernelRow(row)
                          ? "Retained packet window; full connection span unknown"
                          : "Observed span; no close event"}
                    </p>
                  </TableCell>
                  <TableCell>
                    {flowIsKernelRow(row) ? (
                      <>
                        <p>
                          {flowCounter(row.observedPackets)?.toString() ?? "Unknown"} observed
                          packets
                        </p>
                        <p>
                          Sent known lengths{" "}
                          {flowCounter(row.observedTxKnownPackets)?.toString() ?? "Unknown"}/
                          {flowCounter(row.observedTxPackets)?.toString() ?? "Unknown"} · Byte gaps{" "}
                          {flowCounter(row.observedTxByteGaps)?.toString() ?? "Unknown"}
                        </p>
                        <p>
                          Received known lengths{" "}
                          {flowCounter(row.observedRxKnownPackets)?.toString() ?? "Unknown"}/
                          {flowCounter(row.observedRxPackets)?.toString() ?? "Unknown"} · Byte gaps{" "}
                          {flowCounter(row.observedRxByteGaps)?.toString() ?? "Unknown"}
                        </p>
                        <p>Cgroup {row.socketCgroup ?? "unknown"}</p>
                      </>
                    ) : (
                      <p>
                        {row.samples} samples · {row.measuredIntervals} measured ·{" "}
                        {row.skippedIntervals} skipped
                      </p>
                    )}
                    <p>
                      Cookie {row.socket.cookie ?? "unknown"} · Inode{" "}
                      {row.socket.inode ?? "unknown"}
                    </p>
                    <p>Namespace {row.namespace}</p>
                    {!row.evidence && (
                      <p>
                        Sent intervals {row.txIntervals} · Received {row.rxIntervals} · Retransmits{" "}
                        {row.retransIntervals}
                      </p>
                    )}
                    <p className="max-w-48 break-all text-muted-foreground">
                      Via {row.sourceName} · Boot {row.bootId}
                    </p>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <EmptyNote>
            No retained socket rows match this period and filter. No contact or bandwidth conclusion
            can be drawn.
          </EmptyNote>
        )}
      </PanelBody>
    </Panel>
  )
}
function PolicyEditor({
  settings,
  disabled,
  onChanged,
}: {
  settings: FlowSettings
  disabled: boolean
  onChanged: () => void
}) {
  const [interval, setInterval] = useState(String(settings.intervalSeconds))
  const [days, setDays] = useState(String(settings.retentionDays))
  const problem = flowPolicyProblem(interval, days)
  const changed =
    Number(interval) !== settings.intervalSeconds || Number(days) !== settings.retentionDays
  const { confirm, dialog } = useConfirm()
  return (
    <Disclosure quiet summary="Sample interval and retention">
      <div className="space-y-3">
        <FieldRow columns={2}>
          <Field label="Sample interval (seconds)" htmlFor="flow-interval">
            <Input
              id="flow-interval"
              inputMode="numeric"
              value={interval}
              onChange={(e) => setInterval(e.target.value)}
            />
          </Field>
          <Field label="Retention (days)" htmlFor="flow-days">
            <Input
              id="flow-days"
              inputMode="numeric"
              value={days}
              onChange={(e) => setDays(e.target.value)}
            />
          </Field>
        </FieldRow>
        {problem && (
          <p role="alert" className="text-body text-destructive">
            {problem}
          </p>
        )}
        <Button
          variant="outline"
          disabled={disabled || !changed || Boolean(problem)}
          onClick={() =>
            confirm({
              title: "Change socket recording policy",
              description: `Sample every ${interval} seconds and retain up to ${days} days. Expired and over-cap observations are erased immediately. Changing the interval resets the byte baseline; shorter intervals still miss short-lived sockets.`,
              confirmLabel: "Save policy",
              action: async () => {
                await put("/network/flows/policy", {
                  intervalSeconds: Number(interval),
                  retentionDays: Number(days),
                })
              },
              onDone: onChanged,
            })
          }
        >
          Review recording policy
        </Button>
      </div>
      {dialog}
    </Disclosure>
  )
}
