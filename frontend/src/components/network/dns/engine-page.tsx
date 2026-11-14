"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, del, errorMessage, get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  dnsChangeName,
  dnsEngineName,
  readDNSChange,
  readDNSConnection,
  readDNSList,
  readDNSView,
  type DNSConnection,
  type DNSServiceChange,
  type DNSServiceView,
} from "@/lib/network-dns-services"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Globe,
  LockClosed,
  Pencil,
  RefreshClockwise,
  Trash,
} from "@/components/icons"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useHubSession } from "@/components/network/hub-session"
import { IdentityFacts } from "@/components/network/identity-facts"
import { EngineRail } from "./engine-rail"
import {
  ENGINE_TABS,
  ENGINE_TAB_NAME,
  engineHref,
  engineIdOf,
  engineSibling,
  engineTabOf,
  type EngineTab,
} from "./engine-views"
import { DNSServiceConnectionForm } from "./service-connection"
import { DNSServiceDHCP } from "./service-dhcp"
import { DNSServiceFilters } from "./service-filters"
import { DNSEngineReadings, DNSServiceChangeForm } from "./service-inventory"
import { DNSReviewStatus } from "./service-review"
import { ENGINE_PRODUCT, RetainedReview, useDNSAttempts } from "./services"
import { dnsHref } from "./views"

const SERVICES = dnsHref("services")

const BACK = (
  <Link
    href={SERVICES}
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> DNS
  </Link>
)

type Step = { disabled: boolean; run: () => void }

/**
 * One connected DNS engine, as a place of its own.
 *
 * AdGuard Home, Pi-hole or Technitium, read through its own API. Its
 * inventory was a sheet over the DNS page: twelve sections, the filter and
 * DHCP readings, a form of ten kinds of change and the retained reviews, down
 * one drawer, with no address to send anybody. It is a page on the shape a
 * tunnel and a process have:
 *
 * - the line it opens on — the engine as the product it is, whether its last
 *   reading is whole, its address, what the connection may do, its version
 *   and when it was read — with Review a change as the one command and
 *   updating the connection or disconnecting it in the `⋯`;
 * - its views as the underlined strip, in the address (`?tab=`): **Overview**
 *   (who owns it, where it listens, what it forwards to), **Clients**,
 *   **Records**, **Queries**, **Filters**, **DHCP** and **Changes**, where a
 *   change is drafted and every retained review is listed. A review is part
 *   of that view (`?change=<id>`), so it has an address too;
 * - from `xl` the other engines beside it; Alt+↑ and Alt+↓ walk them and the
 *   view on screen goes with you. On a phone it is one pane that scrolls as a
 *   whole.
 *
 * The connection's form stays a sheet: it is a small form. Every read here
 * is a private inventory, so the page is an administrator's.
 */
export function DNSEnginePage() {
  const params = useParams<{ id: string }>()
  const { can, status } = useAuth()
  const id = engineIdOf(params.id)
  if (!can("system.admin") || !status?.user)
    return (
      <Page>
        <PageContext eyebrow={BACK} title="DNS engine" />
        <EmptyState
          icon={LockClosed}
          title="A DNS engine's inventory needs the admin capability"
          description="Its queries, clients and records are private to administrators."
        />
      </Page>
    )
  return <Engine key={`${status.user.id}:${id}`} account={status.user.id} id={id} />
}

const VIEW = "min-h-0 flex-1 pt-6 focus-ring xl:overflow-y-auto data-[state=inactive]:hidden"

function Engine({ account, id }: { account: number; id: string }) {
  const search = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const destructive = can("destructive")
  // Changing a connection stores a credential, which the server refuses a
  // session opened through Just Hub: such a session is not offered it.
  const hub = useHubSession()
  const change = search.get("change") || undefined
  const tab = engineTabOf(search.get("tab"), change)

  // A view that holds a draft or a reading that was asked for stays in the
  // document once it has been opened: a filter draft, the DHCP table and a
  // change being written outlive a look at the queries.
  const [opened, setOpened] = useState<EngineTab[]>([tab])
  if (!opened.includes(tab)) setOpened([...opened, tab])

  const engines = usePoll(
    async (signal) =>
      readDNSList(await get(DNS_SERVICE_BASE, undefined, signal), readDNSConnection, 32),
    30_000,
    [account],
  )
  const view = usePoll(
    async (signal) =>
      readDNSView(
        await get(`${DNS_SERVICE_BASE}/${encodeURIComponent(id)}`, undefined, signal),
        id,
      ),
    15_000,
    [id],
  )
  const history = usePoll(
    async (signal) => {
      const rows = readDNSList(
        await get(`${DNS_SERVICE_BASE}/${encodeURIComponent(id)}/changes`, undefined, signal),
        readDNSChange,
      )
      if (rows.some((row) => row.connectionId !== id))
        throw new Error("Retained DNS history belongs to another connection.")
      return rows
    },
    15_000,
    [id],
  )
  const attempts = useDNSAttempts(account)
  const { confirm, dialog } = useConfirm()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState(false)
  // The review the server returned when a change was staged, shown before
  // the retained copy has been read back.
  const [staged, setStaged] = useState<DNSServiceChange>()

  const connection = view.data?.connection
  const fresh = Boolean(view.data && !view.error)
  const refreshView = view.refresh
  const refreshHistory = history.refresh
  const refreshEngines = engines.refresh
  const refresh = useCallback(() => {
    refreshView()
    refreshHistory()
  }, [refreshView, refreshHistory])

  // The address says which view and which review; replaced rather than
  // pushed, because neither is a place Back should step through.
  const show = useCallback((next: EngineTab, review?: string) => {
    const url = new URL(window.location.href)
    if (next === "overview") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    if (review) url.searchParams.set("change", review)
    else url.searchParams.delete("change")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])
  const review = useCallback(
    (next: DNSServiceChange) => {
      setStaged(next)
      show("changes", next.id)
    },
    [show],
  )
  // A review opens at its head, wherever the form that staged it had scrolled to.
  const reviewed = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (change) reviewed.current?.scrollIntoView({ block: "start" })
  }, [change])

  const list = engines.data ?? []
  const go = (target?: DNSConnection) => target && router.push(engineHref(target.id, tab))
  const at = list.findIndex((engine) => engine.id === id)
  const previous: Step = {
    disabled: list.length === 0 || at === 0,
    run: () => go(engineSibling(list, id, -1)),
  }
  const next: Step = {
    disabled: list.length === 0 || at === list.length - 1,
    run: () => go(engineSibling(list, id, 1)),
  }

  const disconnect = () => {
    if (!fresh || !connection || connection.ownership !== "connected" || !destructive || busy)
      return
    confirm({
      title: `Disconnect ${connection.name}`,
      description:
        "Remove this dashboard connection and its sealed credential. The native engine and its policy keep running.",
      confirmLabel: "Disconnect engine",
      action: async (): Promise<"reported"> => {
        setBusy(true)
        setError(undefined)
        try {
          // The dialog may have stood open while the connection changed hands:
          // it is read again, and only the one that was confirmed is removed.
          const now = readDNSView(
            await get(`${DNS_SERVICE_BASE}/${encodeURIComponent(id)}`),
            id,
          ).connection
          if (now.generation !== connection.generation || now.ownership !== "connected") {
            setError("The connection changed. Refresh its current ownership before disconnecting.")
            refresh()
            return "reported"
          }
          await del(`${DNS_SERVICE_BASE}/${encodeURIComponent(id)}`)
          router.push(SERVICES)
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

  const verbs: Verb[] = connection
    ? [
        ...(hub
          ? []
          : [
              {
                key: "update",
                label: "Update connection",
                icon: Pencil,
                disabled: !fresh || busy,
                run: () => setEditing(true),
              },
            ]),
        ...(connection.ownership === "connected" && destructive
          ? [
              {
                key: "disconnect",
                label: "Disconnect engine",
                icon: Trash,
                danger: true,
                disabled: !fresh || busy,
                run: disconnect,
              },
            ]
          : []),
      ]
    : []

  const steps = (
    <span className="flex items-center">
      <IconAction label="Refresh native reading" onClick={refresh}>
        <RefreshClockwise />
      </IconAction>
      <IconAction label="Previous engine" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next engine" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )
  const gone = !view.data && view.error instanceof ApiError && view.error.status === 404
  const changes = history.data ?? []

  return (
    <Workspace
      name="DNS engine"
      stateKey="network.dns.engine"
      refresh={refresh}
      commands={[
        {
          id: "previous",
          label: "Previous engine",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next engine",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the engine's line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div data-slot="dns-engine" className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <EngineRail
            className="hidden w-64 shrink-0 xl:flex"
            engines={list}
            current={id}
            tab={tab}
            loading={!engines.data && !engines.error}
            failed={!engines.data && Boolean(engines.error)}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
            <div className="flex min-w-0 shrink-0 flex-col gap-5">
              <PageContext
                eyebrow={BACK}
                title={connection?.name ?? "DNS engine"}
                actions={steps}
              />
              {!gone && (
                <Identity
                  view={view.data}
                  failed={Boolean(!view.data && view.error)}
                  command={
                    // On Changes the form is the page: the command that leads to it steps aside.
                    connection?.management &&
                    tab !== "changes" && (
                      <Button size="sm" onClick={() => show("changes")}>
                        Review a change
                      </Button>
                    )
                  }
                  menu={verbs.length > 0 && <VerbMenu verbs={verbs} label="Engine actions" />}
                />
              )}
              {error && (
                <div role="alert">
                  <Notice tone="warning" title="Connection needs review">
                    {error}
                  </Notice>
                </div>
              )}
              {view.data && (!fresh || view.data.state !== "available" || view.data.error) && (
                <div role="status">
                  <Notice
                    tone="warning"
                    title={!fresh ? "Last native reading retained" : "Native service needs review"}
                  >
                    {view.data.error && <p>{view.data.error}</p>}
                    <p>
                      {!fresh
                        ? "The refresh failed. The prior inventory remains visible; native changes are blocked."
                        : `Native state: ${view.data.state}. A returned response does not establish verified native success.`}
                    </p>
                  </Notice>
                </div>
              )}
            </div>
            {gone ? (
              <EmptyState
                icon={Globe}
                title="No DNS engine is connected under that address"
                description="It may have been disconnected."
                action={
                  <Button size="sm" variant="outline" asChild>
                    <Link href={SERVICES}>DNS services</Link>
                  </Button>
                }
              />
            ) : !view.data ? (
              view.error ? (
                // A first read that failed is safe to ask again.
                <ErrorState error={view.error} onRetry={view.refresh} retryable />
              ) : (
                // The strip's place and the first view's, so the read lands under the line.
                <div className="flex min-w-0 flex-col">
                  <div data-slot="engine-strip" className="h-10 border-b border-hairline" />
                  <div className="pt-6">
                    <LoadingRows rows={4} />
                  </div>
                </div>
              )
            ) : (
              <Views
                view={view.data}
                stale={!fresh}
                tab={tab}
                opened={opened}
                show={show}
                counts={{
                  queries: view.data.snapshot?.queries.length,
                  changes: history.data?.length,
                }}
              >
                {{
                  changes: (
                    <>
                      {/* Kept in the document under a review, so a draft is there to come back to. */}
                      <div hidden={Boolean(change)} className="flex min-w-0 flex-col gap-8">
                        <DNSServiceChangeForm view={view.data} stale={!fresh} onReview={review} />
                        <Panel plain aria-label="Retained native changes">
                          <PanelHeader
                            title="Retained changes"
                            actions={
                              history.data && (
                                <span className="numeric text-hint text-muted-foreground">
                                  {changes.length}
                                </span>
                              )
                            }
                          />
                          <PanelBody>
                            {history.error && (
                              <ErrorState error={history.error} onRetry={history.refresh} />
                            )}
                            {!history.data ? (
                              !history.error && <LoadingRows rows={2} />
                            ) : changes.length === 0 ? (
                              <EmptyNote>
                                No native change has been reviewed for this connection.
                              </EmptyNote>
                            ) : (
                              <ChoiceList>
                                {changes.map((one) => (
                                  <ChoiceRow
                                    key={one.id}
                                    title={dnsChangeName(one.request)}
                                    verb={`Read DNS change ${one.id}`}
                                    description={new Date(one.createdAt).toLocaleString()}
                                    trailing={<DNSReviewStatus state={one.state} />}
                                    onSelect={() => review(one)}
                                  />
                                ))}
                              </ChoiceList>
                            )}
                          </PanelBody>
                        </Panel>
                      </div>
                      {change && (
                        <div ref={reviewed} className="flex min-w-0 scroll-mt-6 flex-col gap-5">
                          <button
                            type="button"
                            onClick={() => show("changes")}
                            className="inline-flex items-center gap-1 self-start rounded-sm text-hint text-muted-foreground focus-ring hover:text-foreground hover:underline"
                          >
                            <ArrowLeft className="size-3" /> Changes
                          </button>
                          <RetainedReview
                            key={`change:${change}`}
                            screen={{
                              kind: "change",
                              id: change,
                              initial: staged?.id === change ? staged : undefined,
                            }}
                            attempts={attempts}
                            onListsRefresh={refresh}
                          />
                        </div>
                      )}
                    </>
                  ),
                }}
              </Views>
            )}
          </div>
        </div>
      </Page>
      <SidePanel
        open={editing && Boolean(connection)}
        onOpenChange={(open) => !open && setEditing(false)}
        title={connection ? `Update ${connection.name}` : "Update connection"}
        description="The connection's address, trust and credential."
        width="xl"
        initialFocus="body"
      >
        {editing && connection && (
          <DNSServiceConnectionForm
            key={connection.id}
            connection={connection}
            onCancel={() => setEditing(false)}
            onConnected={() => {
              setEditing(false)
              refresh()
              refreshEngines()
            }}
          />
        )}
      </SidePanel>
      {dialog}
    </Workspace>
  )
}

/**
 * The strip and the views under it. The readings are drawn only while they
 * are on screen; the three views that hold a draft or a reading that was
 * asked for stay in the document once opened, hidden by hand: Radix shows a
 * force-mounted panel whichever tab is chosen.
 */
function Views({
  view,
  stale,
  tab,
  opened,
  show,
  counts,
  children,
}: {
  view: DNSServiceView
  stale: boolean
  tab: EngineTab
  opened: EngineTab[]
  show: (next: EngineTab) => void
  counts: Partial<Record<EngineTab, number>>
  children: { changes: React.ReactNode }
}) {
  const connection = view.connection
  return (
    <Tabs
      value={tab}
      onValueChange={(next) => show(next as EngineTab)}
      className="min-h-0 flex-1 gap-0"
    >
      <TabsPrimitive.List
        aria-label={`Views of ${connection.name}`}
        data-slot="engine-strip"
        className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
      >
        {ENGINE_TABS.map((key) => (
          <TabsPrimitive.Trigger key={key} value={key} className={tabClasses(tab === key, "h-10")}>
            {ENGINE_TAB_NAME[key]}
            {(counts[key] ?? 0) > 0 && <ChipCount>{counts[key]}</ChipCount>}
          </TabsPrimitive.Trigger>
        ))}
      </TabsPrimitive.List>
      {(["overview", "clients", "records", "queries"] as const).map((key) => (
        <TabsPrimitive.Content key={key} value={key} className={VIEW}>
          {tab === key && <DNSEngineReadings view={view} tab={key} />}
        </TabsPrimitive.Content>
      ))}
      <TabsPrimitive.Content value="filters" forceMount hidden={tab !== "filters"} className={VIEW}>
        {opened.includes("filters") && (
          <DNSServiceFilters
            key={JSON.stringify(connection)}
            connection={connection}
            ownerStale={stale}
          />
        )}
      </TabsPrimitive.Content>
      <TabsPrimitive.Content value="dhcp" forceMount hidden={tab !== "dhcp"} className={VIEW}>
        {opened.includes("dhcp") && (
          <DNSServiceDHCP
            key={`dhcp:${JSON.stringify(connection)}`}
            connection={connection}
            ownerStale={stale}
          />
        )}
      </TabsPrimitive.Content>
      <TabsPrimitive.Content value="changes" forceMount hidden={tab !== "changes"} className={VIEW}>
        {opened.includes("changes") && children.changes}
      </TabsPrimitive.Content>
    </Tabs>
  )
}

/**
 * The line the page opens on, laid out as a project's header. It is drawn
 * before the first read with its place for the command held, so the read
 * fills the line rather than moving what is under it.
 */
function Identity({
  view,
  failed,
  command,
  menu,
}: {
  view?: DNSServiceView
  failed: boolean
  command: React.ReactNode
  menu: React.ReactNode
}) {
  if (!view)
    return (
      <HostIdentity
        fallback={Globe}
        slot="dns-engine-header"
        className="border-b-0 pb-0"
        label="About the engine"
        title={failed ? "DNS engine" : <TextShimmer>Reading the engine…</TextShimmer>}
        state={
          failed ? (
            <Status tone="danger" label="Not read" />
          ) : (
            <Status tone="unknown" label="Reading" />
          )
        }
        facts={
          <IdentityFacts loading={!failed}>
            <span>the engine could not be read</span>
          </IdentityFacts>
        }
      />
    )
  const { connection, snapshot } = view
  const whole = view.state === "available" && !view.error
  return (
    <HostIdentity
      mark={ENGINE_PRODUCT[connection.engine]}
      fallback={Globe}
      slot="dns-engine-header"
      className="animate-rise border-b-0 pb-0"
      label={`About ${connection.name}`}
      title={<span className="truncate">{connection.name}</span>}
      state={
        // A flex box, so the word's line is the height it has while it waits.
        <span
          className="flex"
          title={snapshot ? `read ${new Date(snapshot.observedAt).toLocaleString()}` : undefined}
        >
          <Status
            tone={whole ? "running" : "warning"}
            label={whole ? "Connected" : view.state.replaceAll("_", " ")}
          />
        </span>
      }
      // Three short facts, so they stay one line beside the rail at 1280; on a
      // phone they wrap to two, and two lines are held from the first frame.
      facts={
        <IdentityFacts>
          <HostFact>
            {dnsEngineName(connection.engine)}
            {snapshot?.version ? ` ${snapshot.version}` : ""}
          </HostFact>
          <FactDot />
          <HostFact>
            <span className="block truncate font-mono">{connection.endpoint}</span>
          </HostFact>
          <FactDot />
          <HostFact>{connection.management ? "reviewed changes" : "read-only"}</HostFact>
        </IdentityFacts>
      }
      command={command}
      menu={menu}
    />
  )
}
