"use client"

import { useEffect, useState } from "react"
import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  dnsAttemptKey,
  dnsChangeOwnerProblem,
  dnsEngineName,
  dnsRetainedReview,
  readDNSAttempts,
  readDNSChange,
  readDNSConnection,
  readDNSCurrentChange,
  readDNSList,
  readDNSProvision,
  type DNSConnection,
  type DNSServiceChange,
  type DNSServiceProvision,
} from "@/lib/network-dns-services"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
import { Globe, RefreshClockwise } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useHubSession } from "@/components/network/hub-session"
import { engineHref } from "./engine-views"
import { GhostRows } from "./ghost"
import { DNSServiceConnectionForm } from "./service-connection"
import type { DNSConnectionSeed } from "./service-form"
import { DNSServiceProvisionForm } from "./service-provision"
import { DNSReviewStatus, DNSServiceReview } from "./service-review"

/** The product an engine is drawn as; Technitium has no mark in the tree and takes the globe. */
export const ENGINE_PRODUCT: Record<DNSConnection["engine"], string | undefined> = {
  adguard: "adguard",
  pihole: "pihole",
  technitium: undefined,
}

type Screen =
  | { kind: "connect"; seed?: DNSConnectionSeed }
  | { kind: "provision" }
  | { kind: "setup"; id: string; initial?: DNSServiceProvision }

/** A retained review to read: a native change of a connection, or an owned setup. */
export type RetainedScreen =
  | { kind: "change"; id: string; initial?: DNSServiceChange }
  | { kind: "setup"; id: string; initial?: DNSServiceProvision }

/**
 * A request from elsewhere on the page to open one of the manager's sheets:
 * the detected-server handoff asks for a new connection's form seeded for a
 * server; the page's menu asks for a blank connection form or an owned
 * setup's review. The nonce makes pressing the same button twice open it
 * twice. A connection that exists is not a sheet: it has a page
 * (`engine-page.tsx`), and whoever asks for it is sent there.
 */
export type DNSServiceAsk =
  | { kind: "connect"; seed?: DNSConnectionSeed }
  | { kind: "connection"; id: string; name: string }
  | { kind: "provision" }
export type DNSServiceRequest = { nonce: number } & Exclude<DNSServiceAsk, { kind: "connection" }>

export function DNSServiceManager({ request }: { request?: DNSServiceRequest }) {
  const { can, status } = useAuth()
  if (!can("system.admin") || !status?.user) return null
  return <DNSServiceAdmin key={status.user.id} account={status.user.id} request={request} />
}

function DNSServiceAdmin({ account, request }: { account: number; request?: DNSServiceRequest }) {
  const router = useRouter()
  // Connecting an engine stores a credential, which the server refuses a
  // session opened through Just Hub: such a session is not offered it.
  const hub = useHubSession()
  const [screen, setScreen] = useState<Screen>()
  // Derived while rendering from the last request handled, so a handoff opens
  // its panel without an effect.
  const [handled, setHandled] = useState<number>()
  if (request && request.nonce !== handled) {
    setHandled(request.nonce)
    setScreen(
      request.kind === "connect" ? { kind: "connect", seed: request.seed } : { kind: "provision" },
    )
  }
  const connections = usePoll(
    async (signal) =>
      readDNSList(await get(DNS_SERVICE_BASE, undefined, signal), readDNSConnection, 32),
    30000,
    [account],
  )
  const provisions = usePoll(
    async (signal) =>
      readDNSList(await get(`${DNS_SERVICE_BASE}/provisions`, undefined, signal), readDNSProvision),
    30000,
    [account],
  )
  const attempts = useDNSAttempts(account)
  const refresh = () => {
    connections.refresh()
    provisions.refresh()
  }
  const close = () => setScreen(undefined)
  const title =
    screen?.kind === "connect"
      ? "Connect native DNS engine"
      : screen?.kind === "provision"
        ? "Review owned DNS setup"
        : "Retained owned DNS setup"

  const setups = provisions.data ?? []
  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Connected engines"
          actions={
            <>
              {connections.data && connections.data.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {connections.data.length}
                </span>
              )}
              <IconAction label="Refresh" onClick={refresh}>
                <RefreshClockwise />
              </IconAction>
              {/* With no engine the list's place holds the one Connect. */}
              {!hub && connections.data && connections.data.length > 0 && (
                <Button size="xs" variant="outline" onClick={() => setScreen({ kind: "connect" })}>
                  Connect engine
                </Button>
              )}
            </>
          }
        />
        <PanelBody className="flex min-w-0 flex-col gap-3">
          {connections.error && (
            <ErrorState error={connections.error} onRetry={connections.refresh} />
          )}
          {!connections.data ? (
            !connections.error && <GhostRows rows={1} />
          ) : connections.data.length === 0 ? (
            <EmptyState
              mark={<ProductLogos ids={["adguard", "pihole"]} size="md" />}
              title="No DNS engine is connected"
              description="AdGuard Home, Pi-hole or Technitium, read through its own API: queries, filters, clients, DHCP and records."
              action={
                !hub && (
                  <Button onClick={() => setScreen({ kind: "connect" })}>Connect engine</Button>
                )
              }
            />
          ) : (
            <ChoiceList>
              {connections.data.map((connection) => (
                <ChoiceRow
                  key={connection.id}
                  leading={
                    <ProductLogo
                      id={ENGINE_PRODUCT[connection.engine]}
                      size="sm"
                      fallback={Globe}
                    />
                  }
                  title={connection.name}
                  // An engine's inventory is a page of its own, not a sheet over this one.
                  href={engineHref(connection.id)}
                  verb={`Inspect ${connection.name}`}
                  description={
                    <>
                      {dnsEngineName(connection.engine)} ·{" "}
                      <span className="align-top font-mono">{connection.endpoint}</span>
                    </>
                  }
                  trailing={
                    <span className="w-44 max-sm:hidden">
                      <Status
                        tone={connection.management ? "notice" : "stopped"}
                        label={connection.management ? "Reviewed changes enabled" : "Read-only"}
                      />
                    </span>
                  }
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      {/* A setup the dashboard owns is rare: its section is drawn once there
          is one (or its read failed), and reviewing a first one is in the
          page's menu rather than a second empty list under the first. */}
      {(setups.length > 0 || provisions.error) && (
        <Panel plain>
          <PanelHeader
            title="Owned setups"
            actions={
              <>
                <span className="numeric text-hint text-muted-foreground">{setups.length}</span>
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => setScreen({ kind: "provision" })}
                >
                  Review owned setup
                </Button>
              </>
            }
          />
          <PanelBody className="flex min-w-0 flex-col gap-3">
            {provisions.error && (
              <ErrorState error={provisions.error} onRetry={provisions.refresh} />
            )}
            <ChoiceList>
              {setups.map((setup) => (
                <ChoiceRow
                  key={setup.id}
                  leading={
                    <ProductLogo
                      id={ENGINE_PRODUCT[setup.request.engine]}
                      size="sm"
                      fallback={Globe}
                    />
                  }
                  title={setup.request.name}
                  verb={`Review owned DNS setup ${setup.request.name}`}
                  description={
                    <>
                      {dnsEngineName(setup.request.engine)} ·{" "}
                      <span className="align-top font-mono">127.0.0.1:{setup.request.dnsPort}</span>
                    </>
                  }
                  trailing={
                    <span className="w-44 max-sm:hidden">
                      <DNSReviewStatus state={setup.state} />
                    </span>
                  }
                  onSelect={() => setScreen({ kind: "setup", id: setup.id })}
                />
              ))}
            </ChoiceList>
          </PanelBody>
        </Panel>
      )}
      <SidePanel
        open={Boolean(screen)}
        onOpenChange={(open) => !open && close()}
        title={title}
        description="Private native DNS inventory and retained reviews."
        width="xl"
        initialFocus="body"
      >
        {screen?.kind === "connect" ? (
          <DNSServiceConnectionForm
            key={`new:${screen.seed?.engine ?? ""}:${screen.seed?.endpoint ?? ""}`}
            seed={screen.seed}
            onCancel={close}
            onConnected={(view) => {
              refresh()
              router.push(engineHref(view.connection.id))
            }}
          />
        ) : screen?.kind === "provision" ? (
          <DNSServiceProvisionForm
            onReviewed={(initial) => {
              provisions.refresh()
              setScreen({ kind: "setup", id: initial.id, initial })
            }}
          />
        ) : screen?.kind === "setup" ? (
          <RetainedReview
            key={`${screen.kind}:${screen.id}`}
            screen={screen}
            attempts={attempts}
            onListsRefresh={refresh}
          />
        ) : null}
      </SidePanel>
    </>
  )
}

export type AttemptState = {
  ready: boolean
  values: ReadonlySet<string>
  error?: string
  mark: (kind: "change" | "provision", id: string) => boolean
}

/** The applies this account has tried in this tab: a retained review is applied once. */
export function useDNSAttempts(account: number): AttemptState {
  const key = `jd.dns-service-attempts.${account}`
  const [state, setState] = useState<{ key: string; values: Set<string>; error?: string }>()
  useEffect(() => {
    let active = true
    Promise.resolve().then(() => {
      try {
        const values = readDNSAttempts(sessionStorage.getItem(key))
        if (active) setState({ key, values })
      } catch (err) {
        if (active) setState({ key, values: new Set(), error: errorMessage(err) })
      }
    })
    return () => {
      active = false
    }
  }, [key])
  const ready = state?.key === key && !state.error
  return {
    ready,
    values: state?.key === key ? state.values : new Set(),
    error: state?.key === key ? state.error : undefined,
    mark: (kind, id) => {
      if (!ready) return false
      try {
        const values = readDNSAttempts(sessionStorage.getItem(key))
        const attempt = dnsAttemptKey(kind, id)
        if (values.has(attempt) || values.size >= 256) return false
        values.add(attempt)
        sessionStorage.setItem(key, JSON.stringify([...values]))
        setState({ key, values })
        return true
      } catch (err) {
        setState({ key, values: state?.values ?? new Set(), error: errorMessage(err) })
        return false
      }
    },
  }
}

export function RetainedReview({
  screen,
  attempts,
  onListsRefresh,
}: {
  screen: RetainedScreen
  attempts: AttemptState
  onListsRefresh: () => void
}) {
  const kind = screen.kind === "setup" ? "provision" : "change"
  const [returned, setReturned] = useState<DNSServiceChange | DNSServiceProvision>()
  const retained = usePoll(
    async (signal) => {
      const path = kind === "provision" ? `/provisions/${screen.id}` : `/changes/${screen.id}`
      const value = await get(DNS_SERVICE_BASE + path, undefined, signal)
      return kind === "provision"
        ? readDNSProvision(value, screen.id)
        : readDNSChange(value, screen.id)
    },
    5000,
    [kind, screen.id],
  )
  const review = dnsRetainedReview(returned, retained.data ?? screen.initial)
  const ownerId = review && !("resources" in review) ? review.connectionId : undefined
  // History omits snapshots; wait for the retained baseline before checking its native selection.
  const owner = usePoll(
    async (signal) => {
      if (!review || "resources" in review) throw new Error("No native change is selected.")
      return readDNSCurrentChange(
        await get(`${DNS_SERVICE_BASE}/changes/${screen.id}/current`, undefined, signal),
        review,
      )
    },
    5000,
    [ownerId, screen.id, review && !("resources" in review) ? review.generation : undefined],
    { enabled: Boolean(ownerId && review && !("resources" in review) && review.before) },
  )
  const ownerProblem =
    review && !("resources" in review) && review.state === "planned"
      ? dnsChangeOwnerProblem(review, owner.data)
      : undefined
  const stale =
    !attempts.ready || !retained.data || Boolean(retained.error || owner.error || ownerProblem)
  const refresh = () => {
    retained.refresh()
    owner.refresh()
    onListsRefresh()
  }
  return (
    <div className="space-y-6">
      {retained.error && <ErrorState error={retained.error} onRetry={retained.refresh} />}
      {owner.error && <ErrorState error={owner.error} onRetry={owner.refresh} />}
      {attempts.error && (
        <Notice tone="warning" title="Apply attempts cannot be retained">
          {attempts.error}
        </Notice>
      )}
      {ownerProblem && (
        <Notice tone="warning" title="Native baseline needs review">
          {ownerProblem}
        </Notice>
      )}
      {!review ? (
        !retained.error && <LoadingPanel plain />
      ) : (
        <DNSServiceReview
          review={review}
          connection={owner.data?.connection}
          stale={stale}
          attempted={attempts.values}
          onAttempt={attempts.mark}
          onUpdated={setReturned}
          onRefresh={refresh}
        />
      )}
    </div>
  )
}
