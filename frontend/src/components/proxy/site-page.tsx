"use client"

import { useCallback, useEffect, useEffectEvent, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { CloudUpload, Globe, Logs, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import type { DeploymentRequests, SiteSpec, UpstreamReport, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { DetailPage, useDetailTab, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { ViewTabList } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { EMPTY_REQUEST_QUERY, type RequestQuery } from "@/components/deploy/requests-workspace"
import { siteProduct } from "@/components/proxy/marks"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import { ServingStatus, SiteFeatures, SiteNotes, SiteTLS } from "@/components/proxy/site-marks"
import {
  CHIP_LABEL,
  matchesChip,
  siteCert,
  sortSites,
  type SiteChip,
  type SiteSort,
} from "@/components/proxy/site-filters"
import { siteLogPlan } from "@/components/proxy/site-log-plan"
import { SiteLogs, siteLogViews, type SiteLogView } from "@/components/proxy/site-logs"
import { SiteRouteMap } from "@/components/proxy/site-route-map"
import { siteVerdict, type SiteVerdict } from "@/components/proxy/site-overview"
import {
  activeOwner,
  isLeftover,
  matchesSearch,
  siteKind,
  siteOrigin,
  siteTitle,
  type SiteOrigin,
} from "@/components/proxy/site-details"
import { useSiteChanges } from "@/components/proxy/site-changes"
import { sharedNames } from "@/components/proxy/site-order"
import { SiteRenameDialog } from "@/components/proxy/site-rename"
import { TextLink } from "@/components/inline-link"
import { SiteSettings, type LeaveGuard } from "@/components/proxy/site-form"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { SiteFileVerbs } from "@/components/proxy/vhosts-panel"
import { SiteBalancing } from "@/components/proxy/site-balancing"
import { SiteControls } from "@/components/proxy/site-controls-panel"
import { SiteRail } from "@/components/proxy/site-rail"
import {
  SITE_TABS,
  SITE_TAB_LABEL,
  settingsView,
  siteHref,
  siteSettingsHref,
  siteTab,
  type SiteTab,
} from "@/components/proxy/site-tabs"
import { poolsOfSite } from "@/components/proxy/upstream-pools"
import { decodeSegment } from "@/lib/route-params"
import { cn } from "@/lib/utils"

/** The site's file read back: the form's fields, and where it logs. */
type SiteRead = { spec: SiteSpec; managed: boolean; warnings: string[] }

/**
 * The site's verbs its page draws. The way here is the page itself. The ones
 * that change what the host serves — the switch, a rename, a delete, a
 * leftover route's removal — are here as they are in the list
 * (`useSiteChanges`): they were the list's alone, and an operator who opened
 * a site to remove it found nothing to remove it with.
 */
const PAGE_VERBS = [
  "deployment",
  "edit",
  "raw",
  "open",
  "scan",
  "access-log",
  "errors",
  "enable",
  "disable",
  "rename",
  "download",
  "delete",
  "retire",
]

const BACK = { href: "/proxy/sites", label: "Sites" }

/**
 * The chips the rail can answer from the listing and the certificates. Not
 * live and Upstream down need what nginx has loaded and the health check,
 * which the Sites page reads and a site's page does not: under one of those
 * the rail lists every site rather than an empty column.
 */
const railChip = (chip: SiteChip): SiteChip =>
  chip === "notlive" || chip === "down" ? "all" : chip

/**
 * One site, as a place of its own.
 *
 * It opens on the identity line the section's other destinations open on:
 * the engine's mark, the site's name and whether it is serving, what it is,
 * and at the far end whether it is well, the one command that edits it and
 * the rest behind `⋯`. Under it its views, as the view strip with the
 * view in the address (`?tab=`):
 *
 *   **Overview** — how a request reaches it, drawn as the visitors, the
 *   names, the engine and what answers behind it (`site-route-map.tsx`);
 *   what its link or its owner makes of it; how nginx spreads its requests
 *   and what limits it sets.
 *   **Logs** — its requests and errors in the pane a deployment's are read
 *   in, where it is rather than on the host Logs page.
 *   **Settings** — its whole configuration, as a deployment's settings are
 *   read (`SiteSettings` in `site-form.tsx`). Until 0.8.7 this was a sheet
 *   4,280 lines long opened over the page.
 *
 * The Sites list stays beside it from `xl` (`SiteRail`), with the list's own
 * search, chip and order, so the next site is one press away; Alt+↑ and
 * Alt+↓ walk it. On a phone the page is the one pane, and scrolls as a whole.
 *
 * Which logs those are is the site's own business (`site-log-plan.ts`): an
 * nginx site's two files, wherever its directives put them; a route on the
 * shared Caddy ingress, whose requests the ingress records for it.
 */
export function SitePage() {
  const params = useParams<{ name: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeSegment(params.name)
  const { can } = useAuth()
  const admin = can("system.admin")
  const view = settingsView(search.get("view"))

  // The section's own read of the sites, which the Sites page answers from
  // too: the page asking again on a timer of its own was a second read of
  // the same list every thirty seconds.
  const vhosts = useProxyRead("vhosts")
  const certs = useProxyRead("certs")
  const ports = useProxyRead("ports")
  const hosts = useMemo(() => vhosts.data ?? [], [vhosts.data])
  const vhost = hosts.find((v) => v.name === name)
  // The form is an administrator's, for a file it saves back where it read
  // it. Until the listing says which this is, the view asked for is kept.
  const editable = admin && Boolean(vhost?.formEditable)
  // The view is kept in the address and remembered for the visit, as a port's
  // and a certificate's are, so the next site opened from the list opens on
  // it. Settings is never what a site opens on unasked: a form is gone to on
  // purpose, not walked through or remembered into.
  const [chosen, choose] = useDetailTab(SITE_TABS, { remember: "proxy.site.tab" })
  const asked: SiteTab =
    chosen === "settings" && search.get("tab") !== "settings" ? "overview" : chosen
  const tab: SiteTab = asked === "settings" && vhosts.data && !editable ? "overview" : asked

  // Settings ask before unsaved changes are left: the form holds the
  // question, and every way off it that is not a link comes through here.
  const guardRef = useRef<LeaveGuard | null>(null)
  const guarded = useCallback((go: () => void) => {
    if (guardRef.current) guardRef.current(go)
    else go()
  }, [])
  const setTab = useCallback(
    (next: string) => guarded(() => choose(siteTab(next))),
    [choose, guarded],
  )

  const [chip, setChip] = useSessionState<SiteChip>("proxy.sites.chip.075", "all")
  const [query, setQuery] = useSessionState("proxy.sites.query", "")
  const [sort] = useSessionState<SiteSort>("proxy.sites.sort", "urgency")
  const shownChip = railChip(chip)
  const railed = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const readings = { notLive: () => false, certs: certs.data }
    return sortSites(
      hosts.filter((v) => matchesChip(v, shownChip, readings) && matchesSearch(v, needle)),
      // The hour's traffic is the Sites page's reading; without it the order is its usual one.
      sort === "traffic" ? "urgency" : sort,
    )
  }, [hosts, shownChip, query, sort, certs.data])
  const at = railed.findIndex((v) => v.name === name)
  const step = (direction: number) => {
    const next = at >= 0 ? railed[at + direction] : railed[direction > 0 ? 0 : railed.length - 1]
    // The next site opens on the view on screen, except Settings: a form is
    // gone to on purpose, not walked through.
    if (next) {
      guarded(() => router.push(siteHref(next.name, tab === "settings" ? "overview" : tab)))
    }
  }
  const steps: { previous: Step; next: Step } = {
    previous: { disabled: at <= 0, run: () => step(-1) },
    next: { disabled: railed.length === 0 || at >= railed.length - 1, run: () => step(1) },
  }
  const narrowed = shownChip !== "all" || query.trim() !== ""
  const caption = vhosts.data
    ? `${narrowed ? `${railed.length} of ${hosts.length}` : `${hosts.length} ${hosts.length === 1 ? "site" : "sites"}`}${shownChip !== "all" ? ` · ${CHIP_LABEL[shownChip]}` : sort === "urgency" ? " · worst first" : ""}`
    : "Reading the sites…"

  return (
    <SiteMain
      key={name}
      name={name}
      admin={admin}
      vhost={vhost}
      editable={editable}
      tab={tab}
      setTab={setTab}
      view={view}
      guardRef={guardRef}
      steps={steps}
      rail={
        <SiteRail
          sites={railed}
          current={name}
          tab={tab}
          loading={vhosts.loading}
          error={vhosts.data ? undefined : vhosts.error}
          onRetry={vhosts.refresh}
          listeners={ports.error ? undefined : ports.data}
          query={query}
          onQuery={setQuery}
          filtered={shownChip !== "all"}
          onClearFilter={() => setChip("all")}
          caption={caption}
        />
      }
    />
  )
}

function SiteMain({
  name,
  admin,
  vhost,
  editable,
  tab,
  setTab,
  view,
  guardRef,
  steps,
  rail,
}: {
  name: string
  admin: boolean
  vhost: VHost | undefined
  editable: boolean
  tab: SiteTab
  setTab: (tab: string) => void
  view: ReturnType<typeof settingsView>
  guardRef: React.RefObject<LeaveGuard | null>
  steps: { previous: Step; next: Step }
  rail: React.ReactNode
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { status, loading, refresh: refreshStatus } = useProxy()
  const [raw, setRaw] = useState(false)
  const [asked, setAsked] = useState<SiteLogView>()
  const [query, setQuery] = useSessionState<RequestQuery>(
    `proxy.site.${name}.requests`,
    EMPTY_REQUEST_QUERY,
  )
  // A deployment writes its route again on every deploy, so its Settings
  // open on that rather than on the form; "Edit anyway" is kept for the tab.
  // The Sites list asks the same question before it sends a reader here, and
  // says so in the address (`anyway=1`), which is taken off once it is read.
  const [overridden, setOverridden] = useSessionState(`proxy.site.${name}.override`, false)
  const search = useSearchParams()
  const anyway = search.get("anyway") === "1"
  const adopt = useEffectEvent(() => {
    setOverridden(true)
    router.replace(siteSettingsHref(name, view), { scroll: false })
  })
  useEffect(() => {
    if (anyway) adopt()
  }, [anyway])

  const vhosts = useProxyRead("vhosts")
  const certs = useProxyRead("certs")
  const ports = useProxyRead("ports")
  const nginx = vhost?.kind === "nginx"
  const read = usePoll(
    (signal) => get<SiteRead>(`/proxy/sites/${encodeURIComponent(name)}`, undefined, signal),
    30_000,
    [name],
    { enabled: nginx },
  )
  // How nginx spreads the site's requests, and what it logged meeting each
  // server: read with every route's, which the server checks at most every
  // fifteen seconds whoever asks.
  const upstreams = usePoll(
    (signal) => get<UpstreamReport>("/proxy/upstreams", undefined, signal),
    30_000,
    [],
    { enabled: nginx },
  )
  const plan = vhost && siteLogPlan(vhost, read.data?.spec, status?.ingressContainer, ports.data)
  // The site's last hour, for the verdict and the route's first node: the
  // record the server holds in memory, which the Requests view reads too. A
  // limit of one caps the rows, not the hour's figures.
  const hour = usePoll<DeploymentRequests>(
    (signal) =>
      get<DeploymentRequests>(
        `/proxy/sites/${encodeURIComponent(name)}/requests`,
        { since: new Date(Date.now() - 3_600_000).toISOString(), limit: 1 },
        signal,
      ),
    30_000,
    [name],
    { enabled: Boolean(plan?.requests) },
  )
  const refresh = () => {
    vhosts.refresh()
    read.refresh()
    upstreams.refresh()
    hour.refresh()
  }

  // The change in flight, as the word the state says while it is.
  const [busy, setBusy] = useState<string>()
  const [renaming, setRenaming] = useState<VHost | null>(null)
  const hosts = useMemo(() => vhosts.data ?? [], [vhosts.data])
  const changes = useSiteChanges({
    confirm,
    hosts,
    begin: (_name, verb) => setBusy(verb),
    settle: () => {
      setBusy(undefined)
      refresh()
    },
    // The page is the site's own, so once it is gone the list is where to be.
    onDeleted: () => router.push("/proxy/sites"),
    // Replaced: the old name's address is a page of nothing now.
    onRenamed: (_vhost, res) => router.replace(siteHref(res.name, tab)),
  })
  const verbs = useSiteVerbs({
    vhost: vhost ?? PLACEHOLDER,
    admin,
    busy,
    // Another entry has this name: a verb that acts by name could act on it.
    ambiguous: sharedNames(hosts).has(name),
    onEdit: () => setTab("settings"),
    onRaw: () => setRaw(true),
    onServed: () => {},
    onDuplicate: () => {},
    onToggle: changes.toggle,
    onDelete: changes.remove,
    onUnlink: () => {},
    onOverride: () => {},
    onRename: setRenaming,
    onRetire: changes.retire,
  }).filter((verb) => PAGE_VERBS.includes(verb.key))
  // What the page calls the site: its domain once the listing has it.
  const title = vhost ? siteTitle(vhost) : name

  // The frame every detail page has (D9): the way back with the arrows, the
  // rail from `xl`, and the identity line held still over the views.
  const frame = (identity: React.ReactNode, body: React.ReactNode) => (
    <DetailPage
      name="Site"
      what="site"
      stateKey="proxy.site.page"
      refresh={vhosts.refresh}
      steps={steps}
      rail={rail}
      back={BACK}
      title={title}
      identity={identity}
    >
      {body}
    </DetailPage>
  )

  // A route on the Docker Caddy ingress is read through the ingress the
  // proxy's status names, so its page waits for that as an nginx site's
  // waits for its file: drawn before, it said the ingress was not running.
  const routed = vhost?.kind === "caddy" && !vhost.path && !vhost.name.startsWith("docker-caddy:")
  if (!vhosts.data || (nginx && !read.data && !read.error) || (routed && !status && loading)) {
    if (vhosts.error && !vhosts.data)
      return frame(undefined, <ErrorState error={vhosts.error} onRetry={refresh} />)
    // The page's own silhouette, so nothing moves when the site lands: the
    // line keeps a place for its state and its verbs, as the loaded line
    // has them, so it is as tall on a phone as the line that replaces it.
    return frame(
      <IdentityHeader
        loading
        className="border-b-0 pb-0"
        mark={vhost ? siteProduct(vhost) : undefined}
        fallback={Globe}
        title={<TextShimmer>{`Reading ${title}…`}</TextShimmer>}
        state
        command
        menu={false}
      />,
      <LoadingRows />,
    )
  }
  if (!vhost || !plan) {
    return frame(
      undefined,
      <EmptyState
        icon={Globe}
        title={`No site called ${name}`}
        description="It may have been deleted or renamed. The Sites list has every site this host serves."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href="/proxy/sites">All sites</Link>
          </Button>
        }
      />,
    )
  }

  const spec = read.data?.spec
  const engine = nginx ? "nginx" : "Caddy"
  const pools = nginx && upstreams.data ? poolsOfSite(upstreams.data, vhost.path) : []
  const cert = siteCert(vhost, certs.data)
  const summary = hour.data?.status === "available" ? hour.data.summary : undefined
  const verdict = siteVerdict({ vhost, summary, pools, cert })
  const owner = activeOwner(vhost)
  const origin = siteOrigin(vhost)

  const logViews = siteLogViews(plan)
  const logView = asked && logViews.includes(asked) ? asked : logViews[0]
  const failedOnly =
    tab === "logs" &&
    logView === "requests" &&
    query.classes.length === 1 &&
    query.classes[0] === "5xx"
  // The verdict's figure is a question about the requests it counts, as the
  // tile it replaced was: pressed, Logs opens narrowed to them; pressed
  // again, it lets go.
  const narrowToFailed = () => {
    setQuery({ ...query, classes: failedOnly ? [] : ["5xx"] })
    setAsked("requests")
    if (tab !== "logs") setTab("logs")
  }

  // The one command at the line's end: the deployment that writes the route,
  // the form, or the file, whichever this site has first; the rest in `⋯`.
  // On Settings the form is what the reader is looking at: Edit would lead
  // where they are, so it is not offered there, as the command or in the
  // menu, and the file stays in the menu rather than taking Edit's place.
  const onSettings = tab === "settings"
  const offered = onSettings ? verbs.filter((verb) => verb.key !== "edit") : verbs
  const primary =
    offered.find((verb) => verb.key === "deployment") ??
    (onSettings
      ? undefined
      : (offered.find((verb) => verb.key === "edit") ?? offered.find((verb) => verb.key === "raw")))
  const rest = offered.filter((verb) => verb !== primary)
  const tabs = SITE_TABS.filter((key) => key !== "settings" || editable)
  // The identity line's verbs by the one rule (`IdentityVerbs`): the command
  // first, the rest behind `⋯`. On Settings there is no command, only the menu.
  const lineVerbs = primary
    ? IdentityVerbs({ verbs: [{ ...primary, inline: true }, ...rest], name: title })
    : {
        command: undefined,
        menu: rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${title}`} />,
      }

  // Plain HTTP in front of an application is the one fact here that wants action.
  const plainWarned = !vhost.tls && vhost.enabled && vhost.upstreams.length > 0
  const tlsFact = (
    <Fact>
      {/* Held to the line's 16px: the status's glyph, set inline, grew the
          facts' row 3px past the bone it replaces. */}
      <span className="inline-flex h-4 items-center align-top">
        <SiteTLS vhost={vhost} />
      </span>
    </Fact>
  )
  // What the site is, as the facts the Sites row carries, laid out as a
  // project's header: beside its name whether it is serving and whether it
  // is well, and at the far end its command and the rest of its verbs. A
  // fact that wants action (the certificate) comes before the expected ones,
  // so a phone's one line of facts never clips it.
  const identity = (
    <IdentityHeader
      className="animate-rise border-b-0 pb-0"
      mark={siteProduct(vhost)}
      fallback={Globe}
      label={`About ${title}`}
      title={<span className="truncate">{title}</span>}
      state={
        <>
          <ServingStatus vhost={vhost} busy={busy} />
          {verdict && <Verdict verdict={verdict} pressed={failedOnly} onPress={narrowToFailed} />}
        </>
      }
      facts={
        <>
          {plainWarned && tlsFact}
          <Fact>{kindWord(vhost, spec)}</Fact>
          {/* The file, where the title is the domain: what the address and
              the host's own tools call the site. */}
          {title !== vhost.name && (
            <Fact mono title="The site's file">
              {vhost.name}
            </Fact>
          )}
          {read.data && !read.data.managed && <Fact>written by hand</Fact>}
          {!plainWarned && tlsFact}
          {vhost.modified && <Fact>edited {relativeTime(vhost.modified)}</Fact>}
        </>
      }
      command={lineVerbs.command}
      menu={lineVerbs.menu}
    />
  )

  return frame(
    identity,
    <>
      {dialog}
      {changes.outputDialog}
      <SiteRenameDialog
        vhost={renaming}
        onOpenChange={(open) => !open && setRenaming(null)}
        onRename={changes.rename}
      />
      {/* Chosen by a press, not by focus: Settings ask before they are
          left, and the focus that question hands back to the tab it came
          from would choose that tab again and ask again. */}
      <Tabs value={tab} onValueChange={setTab} activationMode="manual" className="gap-0">
        <ViewTabList
          label={`Views of ${title}`}
          value={tab}
          views={tabs.map((key) => ({ key, label: SITE_TAB_LABEL[key] }))}
          sticky
        />

        <TabsContent value="overview" className="pt-6">
          {tab === "overview" && (
            <div className="flex min-w-0 flex-col gap-8 pb-2">
              {/* A leftover's line comes before the picture: removing the
                  route is the one thing to do on this page. */}
              {origin?.kind === "leftover" && (
                <SiteOriginLine
                  origin={origin}
                  onRemove={admin ? () => changes.retire(vhost) : undefined}
                  busy={Boolean(busy)}
                />
              )}
              <SiteRouteMap
                vhost={vhost}
                spec={spec}
                engine={{ product: siteProduct(vhost), name: engine }}
                summary={summary}
                recorded={Boolean(plan.requests)}
                cert={cert}
                pools={pools}
                listeners={ports.data}
              />
              <SiteAbout vhost={vhost} origin={origin?.kind === "leftover" ? undefined : origin} />
              {nginx && upstreams.data && (
                <SiteBalancing pools={pools} evidence={upstreams.data.evidence} />
              )}
              {nginx && <SiteControls name={name} admin={admin} />}
            </div>
          )}
        </TabsContent>

        <TabsContent value="logs" className="pt-6">
          {tab === "logs" &&
            (nginx && !spec && read.error ? (
              // Where an nginx site logs is its file's to say. Unread, the
              // page does not guess: the guess was nginx's shared log, under
              // a notice that the site kept no log of its own.
              <ErrorState error={read.error} onRetry={read.refresh} />
            ) : (
              <div className="flex min-w-0 flex-col gap-4 pb-2">
                {plan.unrecorded && (
                  <Unrecorded
                    kind={plan.unrecorded}
                    href={editable ? siteSettingsHref(name, "hardening") : undefined}
                  />
                )}
                {plan.sources.length > 0 && logView ? (
                  <div data-slot="site-logs">
                    <SiteLogs
                      key={plan.sources.map((s) => s.id).join("|")}
                      name={name}
                      plan={plan}
                      engine={engine}
                      view={logView}
                      onViewChange={setAsked}
                      query={query}
                      onQueryChange={setQuery}
                    />
                  </div>
                ) : (
                  <EmptyState
                    icon={Logs}
                    title="Nothing to read for this site"
                    description={
                      status
                        ? "The Caddy ingress that serves this route is not running, so neither its requests nor its errors can be read. Its state is on the Proxy overview."
                        : "The proxy's state could not be read, so neither is which ingress serves this route, and its requests and errors are read through that ingress."
                    }
                    action={
                      !status && (
                        <Button size="sm" variant="outline" onClick={refreshStatus}>
                          Try again
                        </Button>
                      )
                    }
                  />
                )}
              </div>
            ))}
        </TabsContent>

        {editable && (
          <TabsContent value="settings" className="pt-6">
            {tab === "settings" &&
              (owner && !overridden && !anyway ? (
                <EmptyState
                  icon={CloudUpload}
                  title={`${owner.project} writes this route`}
                  description="Its next deploy replaces whatever is changed here with what the deployment's own settings say, so a lasting change belongs there."
                  action={
                    <div className="flex flex-wrap items-center justify-center gap-2">
                      <Button size="sm" asChild>
                        <Link href={`/deploy/${owner.projectId}`}>Open deployment</Link>
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => setOverridden(true)}>
                        <Warning className="size-4" />
                        Edit anyway
                      </Button>
                    </div>
                  }
                />
              ) : (
                <SiteSettings name={name} view={view} guardRef={guardRef} onSaved={refresh} />
              ))}
          </TabsContent>
        )}
      </Tabs>

      <ConfigEditor
        open={raw}
        onOpenChange={(open) => !open && setRaw(false)}
        path={vhost.path}
        kind={vhost.kind}
        title={vhost.name}
        readOnly={!admin || Boolean(owner)}
        siteDisabled={vhost.kind === "nginx" && Boolean(vhost.enabledPath) && !vhost.enabled}
        onSaved={refresh}
        actions={(busy) => (
          <SiteFileVerbs
            vhost={vhost}
            admin={admin}
            busy={busy}
            onEdit={() => {
              setRaw(false)
              setTab("settings")
            }}
            verbs={["open", "scan", "edit"]}
          />
        )}
      />
    </>,
  )
}

/**
 * What the route picture cannot say about the site: a name in sites-enabled
 * that does not serve its file, who writes it, where its file really is —
 * the lines the Sites card carried under its route — and what it does
 * besides naming and listening, one word each.
 */
function SiteAbout({ vhost, origin }: { vhost: VHost; origin?: SiteOrigin }) {
  const notes = <SiteNotes vhost={vhost} />
  const tagged = Boolean(vhost.features?.length)
  return (
    <div data-slot="site-about" className="flex min-w-0 flex-col gap-2 empty:hidden">
      {origin && <SiteOriginLine origin={origin} />}
      {notes}
      {tagged && (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <SiteFeatures vhost={vhost} />
        </div>
      )}
    </div>
  )
}

/**
 * Who writes the site and where it is removed, in one line: the deployment
 * and its Domains settings, the Caddyfile, the ingress container's own. A
 * leftover says what it is beside the button that removes it, since no
 * destructive verb takes the identity line's command.
 */
function SiteOriginLine({
  origin,
  onRemove,
  busy,
}: {
  origin: SiteOrigin
  onRemove?: () => void
  busy?: boolean
}) {
  const leftover = origin.kind === "leftover"
  return (
    <p
      data-slot="site-origin"
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1",
        leftover ? "text-body" : "text-hint text-muted-foreground",
      )}
    >
      {leftover && <Warning aria-hidden className="size-4 shrink-0 text-warning" />}
      <span className="min-w-0">{origin.text}</span>
      {origin.link && <TextLink href={origin.link.href}>{origin.link.label}</TextLink>}
      {onRemove && (
        <Button size="xs" variant="outline" disabled={busy} onClick={onRemove}>
          Remove
        </Button>
      )}
    </p>
  )
}

/**
 * A site whose requests are written nowhere of its own, said in one line
 * with the way to change it: an empty Requests view reads as a site nobody
 * visits.
 */
function Unrecorded({ kind, href }: { kind: "off" | "shared"; href?: string }) {
  return (
    <p
      data-slot="site-unrecorded"
      className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-body text-muted-foreground"
    >
      <Logs aria-hidden className="size-4 shrink-0" />
      <span className="min-w-0">
        {kind === "off"
          ? "This site keeps no access log, so there are no requests to read; its errors still are."
          : "This site has no access log of its own: its requests go to nginx's shared log, syslog or a stream."}
      </span>
      {href && (
        <Button size="xs" variant="outline" asChild>
          <Link href={href}>Turn it on</Link>
        </Button>
      )}
    </p>
  )
}

/**
 * Whether the site is well, beside whether it is serving. A verdict that
 * counts failed requests is a press away from them; the others say what is
 * wrong where nothing on the page narrows to it. It stood at the line's far
 * end, in two lines, until the command and the menu took that place.
 */
function Verdict({
  verdict,
  pressed,
  onPress,
}: {
  verdict: SiteVerdict
  pressed: boolean
  onPress: () => void
}) {
  const body = (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-x-2">
      <Status tone={verdict.tone} label={verdict.label} />
      {verdict.hint && <span className="text-hint text-muted-foreground">{verdict.hint}</span>}
    </span>
  )
  if (!verdict.narrows) return body
  return (
    <button
      type="button"
      aria-pressed={pressed}
      aria-label={pressed ? "Show every request again" : "Show the requests that failed"}
      onClick={onPress}
      className="-mx-1.5 rounded-md px-1.5 py-0.5 focus-ring transition-colors hover:bg-row-hover aria-pressed:bg-accent"
    >
      {body}
    </button>
  )
}

/**
 * Who writes the site, as its row says it, where that is a deployment, a
 * leftover or Caddy; otherwise what an nginx site does, in the form's words.
 */
function kindWord(vhost: VHost, spec: SiteSpec | undefined) {
  if (vhost.kind === "caddy" || vhost.owner || isLeftover(vhost)) return siteKind(vhost)
  switch (spec?.kind) {
    case "static":
      return "static files"
    case "redirect":
      return spec.permanent ? "permanent redirect" : "redirect"
    default:
      return "reverse proxy"
  }
}

const PLACEHOLDER: VHost = {
  name: "",
  kind: "nginx",
  path: "",
  enabled: false,
  formEditable: false,
  serverNames: [],
  listen: [],
  upstreams: [],
  tls: false,
  modified: "",
  size: 0,
}
