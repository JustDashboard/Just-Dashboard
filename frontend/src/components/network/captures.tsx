"use client"

import Link from "next/link"
import { useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Detail, DetailList, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, downloadUrl, get, post } from "@/lib/api"
import {
  captureFinished,
  captureReading,
  type CaptureDraft,
  type CaptureList,
  type CaptureRun,
} from "@/lib/network-captures"
import { CaptureCreate } from "./capture-create"
import { NetworkReadWarning } from "./read-warning"

/** Reading register: retained observations and private artifacts, with explicit capture setup. */
export function Captures() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  const seed = captureSeed(params)
  // A hand-off from the quick snapshot opens setup with its settings; nothing
  // is captured until the operator starts it.
  const [open, setOpen] = useState(() => Boolean(seed))
  const poll = usePoll(
    (signal) => get<CaptureList>("/network/captures/", undefined, signal),
    2500,
    [admin],
    { enabled: admin },
  )
  const selected = params.get("capture") ?? poll.data?.runs[0]?.id
  const select = (id: string) =>
    router.replace(id ? `/network/captures?capture=${encodeURIComponent(id)}` : "/network/captures")
  return (
    <>
      <PageContext eyebrow="Network" title="Packet captures" />
      {!admin ? (
        <Notice title="Packet captures require the admin capability">
          Original packet bytes and private filter tuples are available only to server
          administrators.
        </Notice>
      ) : (
        <>
          {poll.data ? (
            <>
              <NetworkReadWarning
                error={poll.error}
                refresh={poll.refresh}
                lastSuccess={poll.lastSuccess}
                reading="packet captures"
              />
              <StatGrid columns={4}>
                <StatTile
                  label="Capturing"
                  value={poll.data.runs.filter((run) => !captureFinished(run)).length}
                  hint={`up to ${poll.data.maxRunning} at once`}
                />
                <StatTile
                  label="Retained"
                  value={poll.data.runs.length}
                  hint={`up to ${poll.data.maxRetained} records`}
                />
                <StatTile
                  label="Packet data"
                  value={`${Math.round(poll.data.runs.reduce((sum, run) => sum + (run.result?.bytes ?? 0), 0) / 1024)} KiB`}
                  hint="bounded original snapshots"
                />
                <StatTile
                  label="Retention"
                  value={`${poll.data.retentionHours} h`}
                  hint="expired artifacts are removed"
                />
              </StatGrid>
              <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                <Panel plain>
                  <PanelHeader
                    title="Captures"
                    actions={
                      <div className="flex flex-wrap gap-2">
                        <Button size="sm" variant="outline" onClick={poll.refresh}>
                          Refresh captures
                        </Button>
                        <Button size="sm" onClick={() => setOpen(true)}>
                          New capture
                        </Button>
                      </div>
                    }
                  />
                  <PanelBody>
                    {poll.data.runs.length ? (
                      <ChoiceList>
                        {poll.data.runs.map((run) => {
                          const reading = captureReading(run)
                          return (
                            <ChoiceRow
                              key={run.id}
                              title={run.name}
                              verb={`Open ${run.name}`}
                              href={`/network/captures?capture=${encodeURIComponent(run.id)}`}
                              description={
                                <span className="break-all">
                                  {run.request.interface} ·{" "}
                                  {run.request.family === "inet6" ? "IPv6" : "IPv4"} ·{" "}
                                  {run.request.protocol} ·{" "}
                                  {new Date(run.createdAt).toLocaleString()}
                                </span>
                              }
                              trailing={<Status {...reading} />}
                              className={run.id === selected ? "bg-accent" : undefined}
                            />
                          )
                        })}
                      </ChoiceList>
                    ) : (
                      <EmptyNote>No captures have been requested.</EmptyNote>
                    )}
                  </PanelBody>
                </Panel>
                {selected ? (
                  <CaptureInspector
                    key={selected}
                    id={selected}
                    onChanged={poll.refresh}
                    onDeleted={() => {
                      select("")
                      poll.refresh()
                    }}
                  />
                ) : (
                  <Panel plain>
                    <PanelBody>
                      <Notice title="Capture only the evidence you need">
                        Select one native interface, address family and bounded filter. Quick tools
                        still offer the short packet summary without retaining a PCAP.
                      </Notice>
                      <Button asChild variant="outline" className="mt-3">
                        <Link href="/network/tools?tool=capture">Quick packet summary</Link>
                      </Button>
                    </PanelBody>
                  </Panel>
                )}
              </div>
            </>
          ) : poll.error ? (
            <ErrorState error={poll.error} onRetry={poll.refresh} />
          ) : (
            <LoadingPanel plain />
          )}
          <CaptureCreate
            seed={seed}
            open={open}
            onOpenChange={setOpen}
            onCreated={(id) => {
              select(id)
              poll.refresh()
            }}
          />
        </>
      )}
    </>
  )
}

function CaptureInspector({
  id,
  onChanged,
  onDeleted,
}: {
  id: string
  onChanged: () => void
  onDeleted: () => void
}) {
  const poll = usePoll(
    (signal) => get<CaptureRun>(`/network/captures/${encodeURIComponent(id)}`, undefined, signal),
    1500,
    [id],
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const { confirm, dialog } = useConfirm()
  if (!poll.data)
    return poll.error ? (
      <ErrorState error={poll.error} onRetry={poll.refresh} />
    ) : (
      <LoadingPanel plain />
    )
  const run = poll.data
  const finished = captureFinished(run)
  const result = run.result
  const reading = captureReading(run)
  const cancel = async () => {
    if (busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post(`/network/captures/${encodeURIComponent(id)}/cancel`, {})
      poll.refresh()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Panel plain aria-label="Selected packet capture">
      <PanelHeader title={run.name} actions={<Status {...reading} />} />
      <PanelBody className="space-y-4">
        <NetworkReadWarning
          error={poll.error}
          refresh={poll.refresh}
          lastSuccess={poll.lastSuccess}
          reading="capture evidence"
        />
        <div className="flex flex-wrap gap-2">
          {!finished && (
            <Button
              variant="outline"
              disabled={busy || run.status === "cancelling"}
              pending={busy}
              onClick={() => void cancel()}
            >
              {run.status === "cancelling" ? "Stopping…" : "Stop capture"}
            </Button>
          )}
          {finished && result?.artifactAvailable && (
            <Button asChild variant="outline">
              <a href={downloadUrl(`/network/captures/${id}/pcap`)}>Download original PCAP</a>
            </Button>
          )}
          <Button asChild variant="outline">
            <a href={downloadUrl(`/network/captures/${id}/support`)}>Export redacted support</a>
          </Button>
          {finished && (
            <Button
              variant="ghost"
              onClick={() =>
                confirm({
                  title: "Delete packet capture",
                  description: `Delete the retained packet bytes and metadata for ${run.name}.`,
                  confirmLabel: "Delete capture",
                  action: async () => {
                    await del(`/network/captures/${encodeURIComponent(id)}`)
                  },
                  onDone: onDeleted,
                })
              }
            >
              Delete
            </Button>
          )}
        </div>
        {error && <ErrorState error={error} />}
        {run.error && (
          <Notice tone="warning" title="Capture needs attention">
            {run.error}
          </Notice>
        )}
        {run.status === "interrupted" && (
          <Notice tone="warning" title="Final cleanup is unverified">
            This capture was not restarted. A predecessor process is bounded by its independent host
            timeout; this record does not prove when that process stopped.
          </Notice>
        )}
        {run.status === "cancelling" && (
          <Notice tone="warning" title="Waiting for native cleanup">
            The record becomes cancelled after its runner returns from process-group cleanup.
          </Notice>
        )}
        {result && (
          <StatGrid columns={3}>
            <StatTile
              label="Observed packets"
              value={result.packets}
              hint={finished ? "complete retained records" : "provisional until capture ends"}
            />
            <StatTile
              label="PCAP bytes"
              value={result.bytes}
              hint={`${run.request.maxBytes} byte limit`}
            />
            <StatTile
              label="Kernel drops"
              value={result.kernelDropped ?? "Unknown"}
              tone={
                result.kernelDropped === undefined || result.kernelDropped > 0
                  ? "warning"
                  : "default"
              }
              hint="only when the native tool reports it"
            />
          </StatGrid>
        )}
        <DetailList>
          <Detail label="Interface">{run.request.interface}</Detail>
          <Detail label="Family">{run.request.family === "inet6" ? "IPv6" : "IPv4"}</Detail>
          <Detail label="Protocol">{run.request.protocol}</Detail>
          <Detail label="Source filter">{run.request.source || "Any in this family"}</Detail>
          <Detail label="Destination filter">
            {run.request.destination || "Any in this family"}
          </Detail>
          <Detail label="Port filter">{run.request.port || "Any"}</Detail>
          <Detail label="Limits">
            {run.request.packets} packets · {run.request.seconds} s · {run.request.maxBytes} bytes
          </Detail>
          <Detail label="Snapshot">First {run.request.snapshotLength} bytes of each packet</Detail>
          <Detail label="Requested">
            {new Date(run.createdAt).toLocaleString()} · {run.createdBy}
          </Detail>
          <Detail label="Started">
            {run.startedAt ? new Date(run.startedAt).toLocaleString() : "Not started"}
          </Detail>
          <Detail label="Ended">
            {run.endedAt ? new Date(run.endedAt).toLocaleString() : "Not ended"}
          </Detail>
          {result && (
            <>
              <Detail label="Last progress">
                {result.checkedAt ? new Date(result.checkedAt).toLocaleString() : "Not recorded"}
              </Detail>
              <Detail label="Stop reason">{result.stopReason.replaceAll("_", " ")}</Detail>
              <Detail label="Interface readback">
                {!finished
                  ? "Awaiting final readback"
                  : result.identityVerified
                    ? "Native index and attributes matched before and after"
                    : "Unknown or changed"}
              </Detail>
              {finished && (
                <Detail label="Native process cleanup">
                  Exit {result.cleanup.exitCode} · TERM{" "}
                  {result.cleanup.termSent ? "sent" : "not sent"} · KILL{" "}
                  {result.cleanup.killSent ? "sent" : "not sent"}
                </Detail>
              )}
              <Detail label="Partial trailing packet">
                {result.partialPacket ? "Omitted from the artifact" : "None recorded"}
              </Detail>
              {result.sha256 && (
                <Detail label="Artifact SHA-256">
                  <span className="font-mono break-all">{result.sha256}</span>
                </Detail>
              )}
            </>
          )}
        </DetailList>
        {run.request.incidentRunId && (
          <IncidentReference id={run.request.incidentRunId} capture={run} />
        )}
        <details className="text-body">
          <summary className="cursor-pointer focus-ring">Scope and limitations</summary>
          <ul className="mt-3 list-disc space-y-2 pl-5 text-muted-foreground">
            {run.limitations.map((limit) => (
              <li key={limit}>{limit}</li>
            ))}
          </ul>
        </details>
        {result?.nativeSummary && (
          <details className="text-body">
            <summary className="cursor-pointer focus-ring">Native capture summary</summary>
            <Well className="mt-3 max-h-64 break-all whitespace-pre-wrap">
              {result.nativeSummary}
            </Well>
          </details>
        )}
        {dialog}
      </PanelBody>
    </Panel>
  )
}

function IncidentReference({ id, capture }: { id: string; capture: CaptureRun }) {
  const poll = usePoll(
    (signal) =>
      get<{ id: string; name: string; startedAt?: string; endedAt?: string }>(
        `/network/diagnostics/${encodeURIComponent(id)}`,
        undefined,
        signal,
      ),
    30000,
    [id],
  )
  return (
    <Panel plain>
      <PanelHeader
        title="Related diagnostic"
        actions={
          <Button asChild size="sm" variant="outline">
            <Link href={`/network/runs?run=${encodeURIComponent(id)}`}>Open saved run</Link>
          </Button>
        }
      />
      <PanelBody>
        {poll.data ? (
          <>
            <NetworkReadWarning
              error={poll.error}
              refresh={poll.refresh}
              lastSuccess={poll.lastSuccess}
              reading="related diagnostic"
            />
            <DetailList>
              <Detail label="Name">{poll.data.name}</Detail>
              <Detail label="Diagnostic started">
                {poll.data.startedAt
                  ? new Date(poll.data.startedAt).toLocaleString()
                  : "Not recorded"}
              </Detail>
              <Detail label="Capture started">
                {capture.startedAt ? new Date(capture.startedAt).toLocaleString() : "Not recorded"}
              </Detail>
            </DetailList>
            <p className="mt-3 text-hint text-muted-foreground">
              The explicit reference and timestamps connect the evidence. They do not establish the
              same flow or the cause of an incident.
            </p>
          </>
        ) : poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} />
        ) : (
          <LoadingPanel plain />
        )}
      </PanelBody>
    </Panel>
  )
}

const SNAPSHOT_PROTOCOLS = ["all", "tcp", "udp", "icmp", "icmp6"] as const

/** The interface and protocol a quick packet snapshot links here with. */
function captureSeed(params: {
  get(key: string): string | null
}): Partial<CaptureDraft> | undefined {
  const iface = params.get("interface")?.trim()
  if (!iface || !/^[A-Za-z0-9._@:-]{1,15}$/.test(iface)) return undefined
  const asked = params.get("protocol") ?? "all"
  const protocol = (SNAPSHOT_PROTOCOLS as readonly string[]).includes(asked)
    ? (asked as CaptureDraft["protocol"])
    : "all"
  return {
    interface: iface,
    protocol,
    family: protocol === "icmp6" ? "inet6" : "inet",
    name: `Snapshot follow-up · ${iface}`,
  }
}
