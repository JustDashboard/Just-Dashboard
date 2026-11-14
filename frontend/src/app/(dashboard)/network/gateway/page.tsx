"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { useCallback, useEffect, useState } from "react"
import { Plus, RefreshClockwise, Router, Wrench } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, calendarDate, plural, relativeTime } from "@/lib/format"
import type {
  CounterTotal,
  GatewayForward,
  GatewayNAT,
  GatewayView,
  NetworkLink,
} from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { firewallProduct } from "@/components/network/firewall-picture"
import { gatewayState } from "@/components/network/gateway/evidence"
import { ForwardList, ForwardSheet } from "@/components/network/gateway/forwards"
import { ProxyHandoffs } from "@/components/network/gateway/handoffs"
import { NATList, NATSheet } from "@/components/network/gateway/nat"
import {
  GatewayNotices,
  PolicyEvidence,
  useAdmissionRepair,
} from "@/components/network/gateway/notices"
import { GatewayPicture, GatewayPictureLoading } from "@/components/network/gateway/picture"
import { compact } from "@/components/network/gateway/reading"
import { useGrowth } from "@/components/network/gateway/use-growth"

type Sheet = { kind: "forward"; forward?: GatewayForward } | { kind: "nat"; entry?: GatewayNAT }

/** What each list held at the last visit, so the grey page is the page's own shape. */
type Shape = { forwards: number; nat: number }
const FIRST_VISIT: Shape = { forwards: 2, nat: 1 }

/**
 * What this server passes on, and to where.
 *
 * The page opens on the gateway as a project's page opens on the project: the
 * firewall it writes into as its mark, whether its entries are in force
 * beside the name, and as facts which families the kernel forwards, how many
 * forwards and NAT entries are in force, and whether the firewall lets a
 * translated connection through, as a dot and a word with the evidence for
 * it behind a press. Forward a port is its command; sharing a network, a
 * repair of the dashboard's own admission rules and the refresh are behind
 * the menu.
 *
 * Then the picture of it: every forward as a public port arriving on the
 * left, this server in the middle and the target on the right drawn as the
 * product that usually answers on that port, then each NAT entry as a private
 * network on its way out through a device. A wire carries while its entry's
 * packet counter is growing (the gateway is read every five seconds, and that
 * is the only honest "alive" a firewall counter can give) and is dashed where
 * the entry is switched off.
 *
 * Then the forwards and the NAT entries as lit cards, each with its state as
 * a word in a column of its own and opening the editor it is changed in; what
 * each kind has carried is its section's own total, at the head's far end. A
 * firewall that will not let the gateway write (firewalld, or another table
 * that drops forwarded traffic) is said above the picture with the rule to
 * add, and the commands are off. The reverse proxy, TCP and UDP streams and
 * certificates are not here: they are lit cards into Proxy & TLS, where their
 * engine lives.
 *
 * It opened on a block of sentences about policy, and under the picture stood
 * four tiles and a paragraph about counters. The sentences are behind the
 * admission word and each row's state word; the tiles' figures are the
 * identity's facts and the sections' totals; and the paragraph is the ⓘ
 * beside those totals.
 *
 * The page has its shape before the first read lands: the line, the picture
 * grey, and each section's head over as many grey rows as it held last time.
 */
export default function NetworkGatewayPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const gateway = usePoll<GatewayView>(
    (signal) => get("/network/gateway", undefined, signal),
    5_000,
  )
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  const moved = useGrowth(gateway.data)
  const [sheet, setSheet] = useState<Sheet>()
  const [shape, setShape] = useViewState<Shape>("network.gateway.shape", FIRST_VISIT)
  const openForward = useCallback(
    (forward?: GatewayForward) => setSheet({ kind: "forward", forward }),
    [],
  )
  const openNAT = useCallback((entry?: GatewayNAT) => setSheet({ kind: "nat", entry }), [])
  const admission = useAdmissionRepair(gateway.data, gateway.refresh)

  const view = gateway.data
  const forwards = view?.forwards.length
  const nat = view?.nat.length
  useEffect(() => {
    if (forwards === undefined || nat === undefined) return
    setShape((held) => (held.forwards === forwards && held.nat === nat ? held : { forwards, nat }))
  }, [forwards, nat, setShape])

  const failed = !view && Boolean(gateway.error)
  // An account that cannot make an entry is drawn no command for one; an
  // administrator on a firewall the gateway may not write keeps the commands,
  // off, under the notice that says why.
  const writable = admin && Boolean(view?.capability.writable)
  const devices = links.data ?? []
  const verbs: Verb[] = [
    ...(admin
      ? [
          {
            key: "nat",
            label: "Share a network",
            icon: Plus,
            run: () => openNAT(),
            disabled: !writable,
          },
        ]
      : []),
    ...(admission.offered
      ? [{ key: "repair", label: "Repair owned rules", icon: Wrench, run: admission.repair }]
      : []),
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: gateway.refresh },
  ]
  const state = view && gatewayState(view)
  const inForce = (enabled: boolean) => enabled && Boolean(view?.loaded)
  const made = view?.nat.filter((n) => n.owner).length ?? 0

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Gateway" />
      <HostIdentity
        label="About the gateway"
        mark={firewallProduct(view?.capability.firewall)}
        fallback={Router}
        title="Gateway"
        state={
          state ? (
            <Status tone={state.tone} label={state.label} />
          ) : failed ? (
            <Status tone="danger" label="Not read" />
          ) : (
            <span className="text-xs font-medium">
              <TextShimmer>Reading the gateway…</TextShimmer>
            </span>
          )
        }
        facts={
          view ? (
            <span className={FACTS}>
              <span data-testid="gateway-forwarding">
                <span
                  className={
                    !view.forwarding.ipv4 && view.forwards.length + view.nat.length > 0
                      ? "text-warning"
                      : undefined
                  }
                >
                  IPv4 {view.forwarding.ipv4 ? "forwarding" : "not forwarding"}
                </span>
                {" · "}
                IPv6 {view.forwarding.ipv6 ? "forwarding" : "off"}
              </span>
              <FactDot />
              <span className="numeric">
                {view.forwards.filter((f) => inForce(f.enabled)).length} of{" "}
                {plural(view.forwards.length, "forward")}
              </span>
              <FactDot />
              <span className="numeric">
                {view.nat.filter((n) => inForce(n.enabled)).length} of {view.nat.length} NAT
                {made > 0 && ` · ${made} made by the VPN page`}
              </span>
              <FactDot />
              <PolicyEvidence
                view={view}
                onRepair={admission.offered ? admission.repair : undefined}
              />
            </span>
          ) : failed ? (
            <span>The gateway could not be read.</span>
          ) : (
            <span className={FACTS}>
              <Skeleton className="my-0.5 h-3 w-80 max-w-full" />
            </span>
          )
        }
        command={
          admin && (
            <Button size="sm" onClick={() => openForward()} disabled={!writable}>
              <Plus className="size-3.5" />
              Forward a port
            </Button>
          )
        }
        menu={<VerbMenu verbs={verbs} label="Gateway actions" />}
      />
      {view && (
        <NetworkReadWarning
          error={gateway.error}
          refresh={gateway.refresh}
          lastSuccess={gateway.lastSuccess}
        />
      )}
      {view && <GatewayNotices view={view} />}

      {failed ? (
        <ErrorState error={gateway.error!} onRetry={gateway.refresh} />
      ) : (
        <>
          <Panel plain>
            <PanelHeader title="Where traffic goes" />
            <PanelBody>
              {view ? (
                <GatewayPicture
                  view={view}
                  links={devices}
                  moved={moved}
                  onForward={() => openForward()}
                  onShare={() => openNAT()}
                  onOpenForward={openForward}
                  onOpenNAT={openNAT}
                />
              ) : (
                <GatewayPictureLoading rows={Math.max(1, shape.forwards + shape.nat)} />
              )}
            </PanelBody>
          </Panel>

          <Panel plain>
            <PanelHeader title="Port forwards">
              <Carried
                count={view?.forwards.length}
                packets={view?.forwards.reduce((n, f) => n + f.packets, 0)}
                bytes={view?.forwards.reduce((n, f) => n + f.bytes, 0)}
                totals={view?.forwards.map((f) => f.total)}
                view={view}
              />
            </PanelHeader>
            <PanelBody>
              {!view ? (
                <Rows count={shape.forwards} />
              ) : view.forwards.length === 0 ? (
                <EmptyNote>No port is forwarded.</EmptyNote>
              ) : (
                <ForwardList
                  forwards={view.forwards}
                  flows={view.flows}
                  writable={writable}
                  onOpen={openForward}
                  onChanged={gateway.refresh}
                />
              )}
            </PanelBody>
          </Panel>

          <Panel plain>
            <PanelHeader
              title="NAT"
              actions={
                admin && (
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => openNAT()}
                    disabled={!writable}
                  >
                    <Plus aria-hidden />
                    Share a network
                  </Button>
                )
              }
            >
              <Carried
                count={view?.nat.length}
                packets={view?.nat.reduce((n, e) => n + e.packets + (e.inPackets ?? 0), 0)}
                bytes={view?.nat.reduce((n, e) => n + e.bytes + (e.inBytes ?? 0), 0)}
                totals={view?.nat.flatMap((e) => [e.total, e.inTotal])}
                view={view}
              />
            </PanelHeader>
            <PanelBody>
              {!view ? (
                <Rows count={shape.nat} />
              ) : view.nat.length === 0 ? (
                <EmptyNote>No network is shared out.</EmptyNote>
              ) : (
                <NATList
                  entries={view.nat}
                  flows={view.flows}
                  writable={writable}
                  onOpen={openNAT}
                  onChanged={gateway.refresh}
                />
              )}
            </PanelBody>
          </Panel>

          <Section title="Proxy & TLS">
            <ProxyHandoffs />
          </Section>
        </>
      )}

      {view && sheet?.kind === "forward" && (
        <ForwardSheet
          key={sheet.forward?.id ?? "new"}
          forward={sheet.forward}
          links={devices}
          writable={writable}
          onOpenChange={(open) => !open && setSheet(undefined)}
          onSaved={gateway.refresh}
        />
      )}
      {view && sheet?.kind === "nat" && (
        <NATSheet
          key={sheet.entry?.id ?? "new"}
          entry={sheet.entry}
          links={devices}
          writable={writable}
          onOpenChange={(open) => !open && setSheet(undefined)}
          onSaved={gateway.refresh}
        />
      )}
      {admission.dialog}
    </Page>
  )
}

/**
 * The line of facts as one block. On a phone it wraps, so it holds two lines
 * of room there from the start and the picture under it does not move when
 * the read lands.
 */
const FACTS = "flex min-w-0 flex-wrap content-start items-center gap-x-3 gap-y-1 max-sm:min-h-9"

/**
 * A section's own total, at the far end of its title line: how many entries,
 * what they have carried since the table was loaded and, where the server
 * keeps it, in all across reloads. What the counters are and what they miss
 * is the ⓘ after it. On a phone it is the line under the title, at one
 * height whatever it says, so the rows under it start in one place.
 */
function Carried({
  count,
  packets,
  bytes: carried,
  totals,
  view,
}: {
  count?: number
  packets?: number
  bytes?: number
  totals?: (CounterTotal | undefined)[]
  view?: GatewayView
}) {
  const kept = (totals ?? []).filter((t) => t !== undefined)
  const all = kept.reduce((n, t) => n + t.packets, 0)
  const since = kept
    .map((t) => t.since)
    .filter((t): t is string => Boolean(t))
    .sort()[0]
  return (
    <p className="numeric ml-auto flex min-w-0 items-center gap-1.5 text-hint leading-4 text-muted-foreground max-sm:order-last max-sm:min-h-4 max-sm:basis-full">
      {count === undefined ? (
        <Skeleton className="h-3 w-40" />
      ) : count === 0 ? (
        <span>0</span>
      ) : (
        <>
          <span className="truncate">
            {count} · {compact(packets ?? 0)} packets · {bytes(carried ?? 0)} since load
            {since && ` · ${compact(all)} since ${calendarDate(since)}`}
          </span>
          {view?.counters && view.loaded && (
            <InfoTip label="About these counters">
              Live counters belong to table generation {view.counters.generation}
              {view.counters.observedAt
                ? `, last recorded ${relativeTime(view.counters.observedAt)}`
                : ", not yet recorded"}
              . Totals carry across reloads. {view.counters.gap}
            </InfoTip>
          )}
        </>
      )}
    </p>
  )
}

/** A section's rows while the first read is out: as many as it held last time, at their height. */
function Rows({ count }: { count: number }) {
  if (count === 0) return <Skeleton aria-hidden className="h-9 w-full rounded-lg" />
  return (
    <ul aria-hidden className="space-y-2">
      {Array.from({ length: count }, (_, i) => (
        <li key={i}>
          <Skeleton className="h-14 w-full rounded-xl" />
        </li>
      ))}
    </ul>
  )
}
