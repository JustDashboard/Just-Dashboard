"use client"

import { useState } from "react"
import { DesktopDevice, Location } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGEndpointEvidence, WGHistory, WGInterface, WGPeer } from "@/lib/types"
import type { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RankList } from "@/components/rank-list"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { EndpointEvidence, EventList, Trend, WindowToggle } from "./record"
import type { HistoryWindow } from "./record-logic"

type History = ReturnType<typeof usePoll<WGHistory>>

/**
 * What this host can say about the address a tunnel's clients dial: resolved
 * and compared with the addresses the host holds, when asked. It is a check
 * the reader runs, so the section holds the endpoint and one verb until then.
 */
export function TunnelEndpoint({ tunnel }: { tunnel: WGInterface }) {
  const [evidence, setEvidence] = useState<WGEndpointEvidence>()
  const [checking, setChecking] = useState(false)
  const check = async () => {
    setChecking(true)
    try {
      setEvidence(
        await get<WGEndpointEvidence>(
          `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/endpoint`,
        ),
      )
    } catch (err) {
      notify.error("The endpoint could not be checked", err)
    } finally {
      setChecking(false)
    }
  }
  return (
    <Panel plain>
      <PanelHeader
        title="Endpoint"
        actions={
          <>
            <InfoTip label="What the endpoint check does">
              Resolves the address clients are given and compares it with what this host holds.
              Nothing is sent from outside.
            </InfoTip>
            <Button size="xs" variant="outline" onClick={() => void check()} pending={checking}>
              Check endpoint
            </Button>
          </>
        }
      />
      <PanelBody>
        {evidence ? (
          <EndpointEvidence evidence={evidence} />
        ) : (
          <p className="text-body">
            <span className="font-mono">{tunnel.endpoint || "No endpoint"}</span>
            <span className="text-muted-foreground"> · not checked yet</span>
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * A tunnel's traffic over the window chosen, and for a tunnel that is up, what
 * each peer has moved since it came up, most first.
 */
export function TunnelTraffic({
  name,
  tunnel,
  history,
  span,
  onSpan,
  onPeer,
}: {
  name: string
  /** Undefined for a tunnel that was removed: its record is all there is. */
  tunnel?: WGInterface
  history: History
  span: HistoryWindow
  onSpan: (next: HistoryWindow) => void
  onPeer?: (peer: WGPeer) => void
}) {
  const peers = [...(tunnel?.peers ?? [])].sort(
    (a, b) => b.rxBytes + b.txBytes - (a.rxBytes + a.txBytes),
  )
  const total = peers.reduce((sum, p) => sum + p.rxBytes + p.txBytes, 0)
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <Panel plain>
        <PanelHeader title="Traffic" actions={<WindowToggle value={span} onChange={onSpan} />} />
        <PanelBody>
          {history.error && !history.data ? (
            <ErrorState error={history.error} onRetry={history.refresh} />
          ) : !history.data ? (
            <LoadingRows rows={4} />
          ) : (
            <>
              <NetworkReadWarning
                error={history.error}
                refresh={history.refresh}
                lastSuccess={history.lastSuccess}
                reading="WireGuard record"
              />
              <Trend history={history.data} title={`${name} · every peer · ${span}`} />
            </>
          )}
        </PanelBody>
      </Panel>
      {tunnel && peers.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Moved since it came up"
            actions={
              <span className="numeric text-hint text-muted-foreground">{bytes(total)}</span>
            }
          />
          <PanelBody flush>
            <RankList
              items={peers.map((peer) => ({
                key: peer.publicKey,
                label: peer.name || peer.address,
                mono: !peer.name,
                mark:
                  peer.kind === "site" ? (
                    <Location aria-hidden className="size-3.5 text-muted-foreground" />
                  ) : (
                    <DesktopDevice aria-hidden className="size-3.5 text-muted-foreground" />
                  ),
                hint: (
                  <span className="font-mono">
                    <span className="text-[var(--chart-5)]">↓ {bytes(peer.rxBytes)}</span>{" "}
                    <span className="text-[var(--chart-2)]">↑ {bytes(peer.txBytes)}</span>
                  </span>
                ),
                value: bytes(peer.rxBytes + peer.txBytes),
                share: total > 0 ? (peer.rxBytes + peer.txBytes) / total : 0,
                onClick: onPeer ? () => onPeer(peer) : undefined,
                title: onPeer ? `Open ${peer.name || peer.address}` : undefined,
              }))}
            />
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

/**
 * Everything that was done to a tunnel, newest first, each with its outcome:
 * created, changed, failed, recovered. Kept after the tunnel is removed.
 */
export function TunnelHistory({ name, history }: { name: string; history: History }) {
  if (history.error && !history.data)
    return <ErrorState error={history.error} onRetry={history.refresh} />
  if (!history.data) return <LoadingRows rows={6} />
  return (
    <>
      <NetworkReadWarning
        error={history.error}
        refresh={history.refresh}
        lastSuccess={history.lastSuccess}
        reading="WireGuard record"
      />
      <EventList events={history.data.events} label={`${name} lifecycle`} />
    </>
  )
}
