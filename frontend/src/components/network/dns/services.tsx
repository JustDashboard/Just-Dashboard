"use client"

import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  dnsAttemptKey,
  dnsChangeName,
  dnsChangeOwnerProblem,
  dnsEngineName,
  dnsRetainedReview,
  readDNSAttempts,
  readDNSChange,
  readDNSConnection,
  readDNSList,
  readDNSProvision,
  readDNSView,
  type DNSConnection,
  type DNSServiceChange,
  type DNSServiceProvision,
} from "@/lib/network-dns-services"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { useConfirm } from "@/components/confirm-dialog"
import { Section } from "@/components/page"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { DNSServiceConnectionForm } from "./service-connection"
import { DNSServiceInventory } from "./service-inventory"
import { DNSServiceProvisionForm } from "./service-provision"
import { DNSReviewStatus, DNSServiceReview } from "./service-review"

type Screen =
  | { kind: "connect" }
  | { kind: "edit"; connection: DNSConnection }
  | { kind: "provision" }
  | { kind: "connection"; id: string; name: string }
  | { kind: "change"; id: string; initial?: DNSServiceChange }
  | { kind: "setup"; id: string; initial?: DNSServiceProvision }

export function DNSServiceManager() {
  const { can, status } = useAuth()
  if (!can("system.admin") || !status?.user) return null
  return <DNSServiceAdmin key={status.user.id} account={status.user.id} />
}

function DNSServiceAdmin({ account }: { account: number }) {
  const [screen, setScreen] = useState<Screen>()
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
  const onReview = (initial: DNSServiceChange) =>
    setScreen({ kind: "change", id: initial.id, initial })
  const title =
    screen?.kind === "connect"
      ? "Connect native DNS engine"
      : screen?.kind === "edit"
        ? `Update ${screen.connection.name}`
        : screen?.kind === "provision"
          ? "Review owned DNS setup"
          : screen?.kind === "connection"
            ? screen.name
            : screen?.kind === "change"
              ? "Retained native DNS change"
              : "Retained owned DNS setup"

  return (
    <>
      <Section
        title={
          connections.data
            ? `Native DNS engines · ${connections.data.length}`
            : "Native DNS engines"
        }
        actions={
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={refresh}>
              Refresh
            </Button>
            <Button variant="outline" onClick={() => setScreen({ kind: "provision" })}>
              Review owned setup
            </Button>
            <Button onClick={() => setScreen({ kind: "connect" })}>Connect engine</Button>
          </div>
        }
      >
        {connections.error && (
          <ErrorState error={connections.error} onRetry={connections.refresh} />
        )}
        {!connections.data ? (
          !connections.error && <LoadingPanel plain rows={2} />
        ) : connections.data.length === 0 ? (
          <EmptyNote>No native DNS engine is connected.</EmptyNote>
        ) : (
          <ChoiceList>
            {connections.data.map((connection) => (
              <ChoiceRow
                key={connection.id}
                title={connection.name}
                verb={`Inspect ${connection.name}`}
                description={`${dnsEngineName(connection.engine)} · ${connection.endpoint}`}
                trailing={
                  <Status
                    tone={connection.management ? "notice" : "stopped"}
                    label={connection.management ? "Reviewed changes enabled" : "Read-only"}
                  />
                }
                onSelect={() =>
                  setScreen({ kind: "connection", id: connection.id, name: connection.name })
                }
              />
            ))}
          </ChoiceList>
        )}
      </Section>
      <Section
        title={
          provisions.data ? `Owned DNS setups · ${provisions.data.length}` : "Owned DNS setups"
        }
      >
        {provisions.error && <ErrorState error={provisions.error} onRetry={provisions.refresh} />}
        {!provisions.data ? (
          !provisions.error && <LoadingPanel plain rows={1} />
        ) : provisions.data.length === 0 ? (
          <EmptyNote>No owned DNS setup has been reviewed.</EmptyNote>
        ) : (
          <ChoiceList>
            {provisions.data.map((setup) => (
              <ChoiceRow
                key={setup.id}
                title={setup.request.name}
                verb={`Review owned DNS setup ${setup.request.name}`}
                description={`${dnsEngineName(setup.request.engine)} · 127.0.0.1:${setup.request.dnsPort}`}
                trailing={<DNSReviewStatus state={setup.state} />}
                onSelect={() => setScreen({ kind: "setup", id: setup.id })}
              />
            ))}
          </ChoiceList>
        )}
      </Section>
      <SidePanel
        open={Boolean(screen)}
        onOpenChange={(open) => !open && close()}
        title={title}
        description="Private native DNS inventory and retained reviews."
        width="xl"
        initialFocus="body"
      >
        {screen?.kind === "connect" || screen?.kind === "edit" ? (
          <DNSServiceConnectionForm
            key={screen.kind === "edit" ? screen.connection.id : "new"}
            connection={screen.kind === "edit" ? screen.connection : undefined}
            onCancel={close}
            onConnected={(view) => {
              refresh()
              setScreen({ kind: "connection", id: view.connection.id, name: view.connection.name })
            }}
          />
        ) : screen?.kind === "provision" ? (
          <DNSServiceProvisionForm
            onReviewed={(initial) => {
              provisions.refresh()
              setScreen({ kind: "setup", id: initial.id, initial })
            }}
          />
        ) : screen?.kind === "connection" ? (
          <ConnectionDetail
            key={screen.id}
            id={screen.id}
            onReview={onReview}
            onEdit={(connection) => setScreen({ kind: "edit", connection })}
            onDisconnected={() => {
              refresh()
              close()
            }}
          />
        ) : screen?.kind === "change" || screen?.kind === "setup" ? (
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

function ConnectionDetail({
  id,
  onReview,
  onEdit,
  onDisconnected,
}: {
  id: string
  onReview: (value: DNSServiceChange) => void
  onEdit: (value: DNSConnection) => void
  onDisconnected: () => void
}) {
  const view = usePoll(
    async (signal) => readDNSView(await get(`${DNS_SERVICE_BASE}/${id}`, undefined, signal), id),
    15000,
    [id],
  )
  const history = usePoll(
    async (signal) => {
      const rows = readDNSList(
        await get(`${DNS_SERVICE_BASE}/${id}/changes`, undefined, signal),
        readDNSChange,
      )
      if (rows.some((row) => row.connectionId !== id))
        throw new Error("Retained DNS history belongs to another connection.")
      return rows
    },
    15000,
    [id],
  )
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const fresh = Boolean(view.data && !view.error && can("system.admin"))
  const live = useRef({ fresh, connection: view.data?.connection, destructive: can("destructive") })
  useLayoutEffect(() => {
    live.current = { fresh, connection: view.data?.connection, destructive: can("destructive") }
  })
  const refresh = () => {
    view.refresh()
    history.refresh()
  }
  const disconnect = () => {
    const connection = view.data?.connection
    if (
      !fresh ||
      !connection ||
      connection.ownership !== "connected" ||
      !can("destructive") ||
      busy
    )
      return
    confirm({
      title: `Disconnect ${connection.name}`,
      description:
        "Remove this dashboard connection and its sealed credential. The native engine and its policy keep running.",
      confirmLabel: "Disconnect engine",
      action: async (): Promise<"reported"> => {
        const current = live.current
        if (
          !current.fresh ||
          !current.destructive ||
          current.connection?.id !== connection.id ||
          current.connection.generation !== connection.generation ||
          current.connection.ownership !== "connected"
        ) {
          setError("The connection changed. Refresh its current ownership before disconnecting.")
          return "reported"
        }
        setBusy(true)
        setError(undefined)
        try {
          await del(`${DNS_SERVICE_BASE}/${id}`)
          onDisconnected()
        } catch (err) {
          setError(errorMessage(err))
          refresh()
        } finally {
          setBusy(false)
        }
        return "reported"
      },
    })
  }
  return (
    <div className="space-y-6">
      {view.error && <ErrorState error={view.error} onRetry={view.refresh} />}
      {error && (
        <div role="alert">
          <Notice tone="warning" title="Connection needs review">
            {error}
          </Notice>
        </div>
      )}
      {!view.data ? (
        !view.error && <LoadingPanel plain />
      ) : (
        <>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={refresh}>
              Read native engine
            </Button>
            <Button
              variant="outline"
              disabled={!fresh || busy}
              onClick={() => onEdit(view.data!.connection)}
            >
              Update connection
            </Button>
            {view.data.connection.ownership === "connected" && (
              <Button
                variant="outline"
                disabled={!fresh || !can("destructive") || busy}
                onClick={disconnect}
                pending={busy}
              >
                Disconnect engine
              </Button>
            )}
          </div>
          <DNSServiceInventory
            view={view.data}
            stale={!fresh}
            onReview={onReview}
            onRefresh={refresh}
          />
        </>
      )}
      <Section
        title={
          history.data
            ? `Retained native changes · ${history.data.length}`
            : "Retained native changes"
        }
      >
        {history.error && <ErrorState error={history.error} onRetry={history.refresh} />}
        {!history.data ? (
          !history.error && <LoadingPanel plain rows={2} />
        ) : history.data.length === 0 ? (
          <EmptyNote>No native change has been reviewed for this connection.</EmptyNote>
        ) : (
          <ChoiceList>
            {history.data.map((change) => (
              <ChoiceRow
                key={change.id}
                title={dnsChangeName(change.request)}
                verb={`Read DNS change ${change.id}`}
                description={new Date(change.createdAt).toLocaleString()}
                trailing={<DNSReviewStatus state={change.state} />}
                onSelect={() => onReview(change)}
              />
            ))}
          </ChoiceList>
        )}
      </Section>
      {dialog}
    </div>
  )
}

type AttemptState = {
  ready: boolean
  values: ReadonlySet<string>
  error?: string
  mark: (kind: "change" | "provision", id: string) => boolean
}

function useDNSAttempts(account: number): AttemptState {
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

function RetainedReview({
  screen,
  attempts,
  onListsRefresh,
}: {
  screen: Extract<Screen, { kind: "change" | "setup" }>
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
  const owner = usePoll(
    async (signal) =>
      readDNSView(await get(`${DNS_SERVICE_BASE}/${ownerId}`, undefined, signal), ownerId),
    5000,
    [ownerId],
    { enabled: Boolean(ownerId) },
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
