"use client"

import { Fragment, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { ArrowRight, CloudUpload, Download, Globe, Plus, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { bytes, plural } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { ApiError, del, get, post } from "@/lib/api"
import type {
  Certificate,
  NpmImportResult,
  ProxyPending,
  SiteBackup,
  SiteCacheUsage,
  SiteDeleteResult,
  SitePlacementResult,
  SiteRenameResult,
  SiteResult,
  SitesBulkResult,
  SiteTrafficReading,
  SitesTraffic,
  SiteUpstreamHealth,
  SiteUpstreams,
  SystemdUnit,
  VHost,
  VHostLinkResult,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMetrics } from "@/hooks/use-metrics"
import { ConfirmDialog, useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { Page, PageContext, SearchInput } from "@/components/page"
import { ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbBar, VerbMenu, type Verb } from "@/components/verbs"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { AccessListsPanel } from "@/components/proxy/access-lists-panel"
import { RouteResolver } from "@/components/proxy/route-resolver"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useNewSiteLink } from "@/components/proxy/site-link"
import { DefaultSitePanel } from "@/components/proxy/default-site"
import { SiteBand } from "@/components/proxy/site-band"
import {
  errorTone,
  siteApp,
  siteHue,
  siteTraffic,
  sitesVerdict,
  type SiteApp,
  type SitesVerdict,
} from "@/components/proxy/site-apps"
import { SiteForm } from "@/components/proxy/site-form"
import { SiteRenameDialog } from "@/components/proxy/site-rename"
import { SiteImportDialog } from "@/components/proxy/site-import"
import { SiteBackupsPanel } from "@/components/proxy/site-backups"
import {
  RouteMark,
  ServingStatus,
  SiteTLS,
  UpstreamHealth,
  siteKind,
} from "@/components/proxy/site-marks"
import { siteDestination } from "@/components/proxy/site-rail"
import { siteSettingsHref } from "@/components/proxy/site-tabs"
import { ExpiryStatus } from "@/components/proxy/expiry-status"
import {
  CHIP_LABEL,
  SORT_LABEL,
  exportBundle,
  isDown,
  isRedirect,
  isStatic,
  matchesChip,
  siteCert,
  siteUpstreams,
  sortSites,
  stepSelection,
  type SiteChip,
  type SiteReadings,
  type SiteSort,
} from "@/components/proxy/site-filters"
import { activeOwner, matchesSearch } from "@/components/proxy/site-details"
import { reloadFailure, reloadOutput } from "@/components/proxy/site-outcome"
import { sitesReadError, sitesReadFailure } from "@/components/proxy/sites-read-failure"
import {
  KEPT_LOAD,
  disabledHint,
  keptLoad,
  loadKnown,
  loadedSince,
  notLiveLabel,
  siteChanges,
} from "@/components/proxy/site-pending"
import { PendingStrip } from "@/components/proxy/pending-strip"
import {
  engineRun,
  hear,
  markUnloaded,
  reloadOutcome,
  stillUnloaded,
  unitState,
  type ReloadOutcome,
  type ReloadsHeard,
  type Unloaded,
  type UnitReading,
} from "@/components/proxy/site-serving"
import { engineUnit, useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import {
  downloadFrom,
  opensFile,
  retireRoute,
  routeRetirement,
  siteDeletable,
  sitePath,
  siteSwitchable,
  useSiteVerbs,
} from "@/components/proxy/site-verbs"
import { isDisabled, sharedNames } from "@/components/proxy/site-order"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** What a bulk change is called while it runs and once it has landed. */
const BULK = {
  enable: { word: "Enable", busy: "Enabling", done: "enabled" },
  disable: { word: "Disable", busy: "Disabling", done: "disabled" },
  delete: { word: "Delete", busy: "Deleting", done: "deleted" },
} as const

type BulkAction = keyof typeof BULK

/**
 * The chips that name something wrong carry its tone in their word, so the
 * strip says where the trouble is before a count is read. Drawn only while
 * there is one (the strip leaves out an empty chip).
 */
const CHIP_TONE: Partial<Record<SiteChip, string>> = {
  broken: "text-destructive",
  down: "text-destructive",
  notlive: "text-warning",
  expiring: "text-warning",
  plain: "text-warning",
}

/** A card's identity in the list: two entries may share a name across layouts. */
const siteKey = (v: VHost) => `${v.kind}:${v.layout ?? ""}:${v.name}`

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

/** nginx's own words about a change, kept for the operator who asks to see them. */
type NginxOutput = { title: string; output: string }

/**
 * One read of the list: the rows, and the error when the read failed; and the
 * read of what nginx has not loaded that went with it.
 */
type ListRead = { read: unknown; data: unknown; pending: unknown }

/**
 * A verb in flight on a site. Once it has answered, the card keeps the busy
 * word until the list is read again: dropping it on the answer drew the state
 * from before the change — "disabled" under a toast saying enabled — until
 * the next read arrived. `answered` is the read the page had then.
 * `unloaded` is a change a verb made that nginx did not reload; it outlives
 * the busy word, and a later verb on the site that is refused keeps it.
 */
type Busy = { verb: string; answered?: ListRead; unloaded?: Unloaded }

/**
 * What a change that landed says when nginx did not reload: `notRunning` when
 * there was no nginx to reload, which starts with the change, and
 * `notReloaded`, before nginx's reason, when the reload itself failed.
 */
type ReloadCopy = { notRunning: string; notReloaded: string }

/**
 * A file open in the config editor: the site's own, or — for a site whose
 * name in sites-enabled holds a separate copy — the copy nginx serves.
 * `override` is a deployment's route an administrator chose to edit anyway;
 * without it, such a route opens read-only.
 */
type OpenFile = { vhost: VHost; served?: boolean; override?: boolean }

/**
 * The host's sites, each one line: what it fronts over the engine serving
 * it, its name over where its requests go, then in aligned columns its last
 * hour, its ports, its TLS and certificate, and whether it is serving. The
 * rows keep the worst-first groups, and each opens the site's own page —
 * what it is, its requests and errors, and its Settings — for every site and
 * every reader. Everything else a site can do is in its menu.
 *
 * A site was a card about 190px tall until 0.8.7, with its route, its notes
 * and three buttons inside, and editing one opened a sheet over this list.
 * The route is the row's second line now, the notes and the feature words
 * are on the site's page, and editing is that page's Settings view: Edit
 * goes there, and so does every `?site=` link that used to open the sheet.
 * The sheet stays for the one thing that is not an edit, a new site.
 */
export function SitesPage({ hasNginx }: { hasNginx: boolean }) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const { status, loading: statusLoading } = useProxy()
  const [editing, setEditing] = useState<OpenFile | null>(null)
  // The new-site form's open state and its fields are kept for the tab: a
  // site is a long form, and checking a port or a certificate half-way
  // through it should not mean typing it again. Closing it is what forgets
  // it. An existing site is edited on its own page.
  const [form, setForm] = useSessionState<{
    open: boolean
    copyFrom: string | null
    session: number
  }>("proxy.sites.newform", { open: false, copyFrom: null, session: 0 })
  const [filter, setFilter] = useSessionState("proxy.sites.query", "")
  const [chip, setChip] = useSessionState<SiteChip>("proxy.sites.chip.075", "all")
  const [sortChoice, setSort] = useSessionState<SiteSort>("proxy.sites.sort", "urgency")
  // By name, as the bulk endpoint acts: a name two entries share is never
  // selectable, so a name picks out one site.
  const [selected, setSelected] = useState<string[]>([])
  // The site j and k have moved to, which Enter opens.
  const [cursor, setCursor] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)
  const [renaming, setRenaming] = useState<VHost | null>(null)
  const [importing, setImporting] = useState(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const [pending, setPending] = useState<Record<string, Busy>>({})
  const [output, setOutput] = useState<NginxOutput | null>(null)
  // In the URL so a deployment finding can link straight at the site serving
  // its hostname, and so the browser's back button restores the selection.
  const [requested, setRequested] = useQuerySelection("site")
  const { data, error, loading, refresh } = useProxyRead("vhosts")
  // Readings other parts of the proxy own. Each draws nothing where its
  // endpoint does not answer — no certificates readable, no health check or
  // traffic summary on this build — rather than an error on a page about
  // sites, and nothing drawn is never read as "fine".
  const certPoll = useProxyRead("certs")
  const portsPoll = useProxyRead("ports")
  const { host } = useMetrics()
  const upstreamPoll = usePoll(
    (signal) => get<SiteUpstreams>("/proxy/upstreams", undefined, signal),
    30_000,
    [],
    { enabled: hasNginx },
  )
  const trafficPoll = usePoll(
    (signal) => get<SitesTraffic>("/proxy/traffic", undefined, signal),
    60_000,
    [],
    { enabled: hasNginx },
  )
  // Whether the configuration history answers, which is what its verb
  // leads to; it is an administrator's.
  const historyPoll = usePoll(
    (signal) => get<unknown>("/proxy/history/files", undefined, signal),
    0,
    [admin],
    { enabled: admin && hasNginx },
  )
  const hasTraffic = Array.isArray(trafficPoll.data?.sites)
  const hasHistory = historyPoll.data !== undefined
  const sort: SiteSort = sortChoice === "traffic" && !hasTraffic ? "urgency" : sortChoice
  // What on disk nginx has not loaded. `nginx -s reload` answers before
  // nginx has loaded anything, so a read after a reload the page asked for
  // names the load it saw before, and the backend waits for a newer one.
  // `warn` is a reload a verb knows it sent: nginx still on the load from
  // before it refused what it read, which the verb's toast could not know.
  const reloadFrom = useRef<{ after: string; warn: boolean } | undefined>(undefined)
  // Deleting a site leaves its .bak, so the list is read again after every
  // verb, with the sites.
  const backupsPoll = usePoll<SiteBackup[]>(
    (signal) => get("/proxy/site-backups", undefined, signal),
    0,
    [],
    { enabled: admin && hasNginx },
  )
  const pendingPoll = usePoll(
    async (signal) => {
      const expected = reloadFrom.current
      const res = await get<ProxyPending>(
        "/proxy/pending",
        expected ? { after: expected.after } : undefined,
        signal,
      )
      if (reloadFrom.current === expected) reloadFrom.current = undefined
      if (expected?.warn && loadKnown(res) && !loadedSince(expected.after, res)) {
        notify.warning(KEPT_LOAD, { description: keptLoad(res.lastReload), duration: 12_000 })
      }
      return res
    },
    30_000,
    [],
    { enabled: hasNginx },
  )
  const expectReload = (warn: boolean) => {
    const after = pendingPoll.data?.generation
    if (after) reloadFrom.current = { after, warn }
    pendingPoll.refresh()
  }
  // Each read of the list, failed or not, is a new object here: a failed one
  // brings a new error and keeps the rows, a good one brings new rows. The
  // same goes for what is pending, which is read only where there is nginx.
  const read: unknown = error ?? data
  const pendingRead: unknown = hasNginx ? (pendingPoll.error ?? pendingPoll.data) : undefined
  const lastRead = useRef<ListRead>({ read, data, pending: pendingRead })
  useEffect(() => {
    lastRead.current = { read, data, pending: pendingRead }
  })
  // The read Try again asked for is in once `read` moves on from this one.
  const [retryFrom, setRetryFrom] = useState<unknown>(undefined)
  const retrying = retryFrom !== undefined && retryFrom === read

  // Whether the engine runs, read from systemd the way the Overview reads
  // it: an enabled site is served only while it does.
  const unitName = engineUnit(status)
  const unitPoll = usePoll(
    async (signal) => {
      const r = await get<{ unit: SystemdUnit }>(`/systemd/${unitName}`, undefined, signal)
      // Stamped here rather than during render, which has to stay pure.
      return { at: Date.now(), unit: r.unit }
    },
    30_000,
    [unitName],
    { enabled: Boolean(unitName) },
  )
  // systemd answers `not-found` for a unit it has never seen: an nginx run
  // from a container or by hand has none, and then only the verbs' reloads
  // say whether it runs.
  const unitReading: UnitReading | undefined =
    unitPoll.data?.unit.loadState === "loaded" ? unitPoll.data : undefined
  const unitNow = useRef(unitReading)
  useEffect(() => {
    unitNow.current = unitReading
  })
  const [reloads, setReloads] = useState<ReloadsHeard>({})
  const run = engineRun(unitReading, reloads.last)
  // The verbs' reloads are nginx's; a unit is Caddy's only on a host with
  // no nginx, where it serves the Caddyfile's sites and never the Docker
  // ingress's, which is a container with no unit here.
  const caddyEngine = run?.from === "unit" && unitName === "caddy.service"
  const engineName = caddyEngine ? "Caddy" : "nginx"
  const engineStopped = (v: VHost) =>
    run?.running === false &&
    (caddyEngine ? v.kind === "caddy" && Boolean(v.path) : v.kind === "nginx")

  const openNew = (copyFrom: string | null = null) => {
    setRequested(null)
    setForm((f) => ({ open: true, copyFrom, session: f.session + 1 }))
  }
  useNewSiteLink(() => openNew())
  const editSite = (name: string) => router.push(siteSettingsHref(name))

  // A ?site= link from elsewhere in the dashboard opens that site as soon as
  // its row loads. For an administrator and a file the form saves back where
  // it found it, that is the site's Settings, which the sheet this link
  // opened became. Anyone else, and any other file, gets the config viewer —
  // read-only unless they may write it — derived from the URL rather than
  // copied into state, so the back button closes it and a reload reopens it.
  // A deployment's route, which a deployment links to, opens read-only: its
  // deployment is where it is changed.
  const linked = requested
    ? (data?.find((vhost) => vhost.name === requested && vhost.formEditable) ??
      data?.find((vhost) => vhost.name === requested))
    : undefined
  const linkedForm = admin && linked?.formEditable && !activeOwner(linked) ? linked : undefined
  const rawEditing: OpenFile | null =
    editing ?? (linked && !linkedForm && opensFile(linked) ? { vhost: linked } : null)
  const rawSite = rawEditing?.vhost
  const rawServed = Boolean(rawEditing?.served)
  const rawReadOnly = !admin || Boolean(rawSite && activeOwner(rawSite) && !rawEditing?.override)
  // A link to nothing has no file to open: the overview's finding links it
  // here, and what there is to do with it is take it out.
  const linkedRemoval = admin && linked?.broken === "dangling" && !linked.path ? linked : undefined
  const linkedSettings = linkedForm?.name
  useEffect(() => {
    // Replaced, so Back from the site's Settings is not this address again.
    if (linkedSettings) router.replace(siteSettingsHref(linkedSettings))
  }, [linkedSettings, router])

  const closeForm = (open: boolean) => {
    setForm((f) => ({ ...f, open }))
    if (!open) forgetSessionState("proxy.site.form.")
  }
  const closeRaw = (open: boolean) => {
    if (open) return
    setEditing(null)
    setRequested(null)
  }

  const hosts = useMemo(() => data ?? [], [data])
  const shared = useMemo(() => sharedNames(hosts), [hosts])
  const arrived = useArrivals(hosts.map(siteKey))
  const counts = useMemo(
    () => ({
      all: hosts.length,
      tls: hosts.filter((v) => v.tls).length,
      disabled: hosts.filter(isDisabled).length,
      caddy: hosts.filter((v) => v.kind === "caddy").length,
    }),
    [hosts],
  )
  // What each site hands its requests to, read through the Ports page's list
  // of sockets: the card's logo and the Applications block are this answer.
  const listeners = portsPoll.error ? undefined : portsPoll.data
  const apps = useMemo(() => {
    const out = new Map<string, SiteApp | undefined>()
    for (const v of hosts)
      out.set(siteKey(v), siteApp(v, listeners, siteUpstreams(v, upstreamPoll.data)))
    return out
  }, [hosts, listeners, upstreamPoll.data])
  const appOf = (v: VHost) => apps.get(siteKey(v))
  // A change nginx did not load waits for a reload; with the engine
  // stopped, the rest are enabled rather than serving, and nothing is live.
  const notLive = (v: VHost) =>
    run?.running === false ? undefined : notLiveLabel(v, siteChanges(pendingPoll.data, v))
  // A card whose change nginx has not loaded is not drawn as serving it.
  // Only a card with a link in sites-enabled can carry one: a verb acts on
  // that link, and a conf.d file of the same name is not what it changed.
  const isUnloaded = (v: VHost) =>
    Boolean(v.enabledPath) &&
    stillUnloaded(pending[v.name]?.unloaded, reloads.loadedAt, unitReading)
  const readings: SiteReadings = {
    notLive: (v) => Boolean(notLive(v)),
    unloaded: isUnloaded,
    certs: certPoll.data,
    upstreams: upstreamPoll.data,
    traffic: trafficPoll.data,
  }
  const chipCounts = Object.fromEntries(
    (Object.keys(CHIP_LABEL) as SiteChip[]).map((key) => [
      key,
      hosts.filter((v) => matchesChip(v, key, readings)).length,
    ]),
  ) as Record<SiteChip, number>
  const needle = filter.trim().toLowerCase()
  const visible = sortSites(
    hosts.filter((v) => matchesChip(v, chip, readings) && matchesSearch(v, needle)),
    sort,
    trafficPoll.data,
  )
  // Selection needs a name only one entry answers to, and something to do
  // with it: a file to export, or a switch or a delete for an administrator.
  const selectable = (v: VHost) =>
    v.kind === "nginx" &&
    !shared.has(v.name) &&
    (opensFile(v) || (admin && (siteSwitchable(v) || siteDeletable(v))))
  const chosen = visible.filter((v) => selected.includes(v.name) && selectable(v))
  // A deployment's route is changed from its deployment, one at a time.
  const bulkable = (v: VHost) => admin && !activeOwner(v)
  const toEnable = chosen.filter((v) => bulkable(v) && siteSwitchable(v) && !v.enabled && !v.broken)
  const toDisable = chosen.filter((v) => bulkable(v) && siteSwitchable(v) && v.enabled)
  const toDelete = chosen.filter((v) => bulkable(v) && siteDeletable(v))
  const toExport = chosen.filter(opensFile)
  const allChosen = visible.filter(selectable)
  const toggleSelected = (v: VHost, on: boolean) =>
    setSelected((list) => (on ? [...list, v.name] : list.filter((name) => name !== v.name)))

  const setBusy = (name: string, verb: string) =>
    setPending((p) => ({ ...p, [name]: { verb, unloaded: p[name]?.unloaded } }))
  // The verb has answered: read the list and what is pending again, and
  // hold the busy word until both reads are in — after a reload, once nginx
  // has loaded something newer or plainly has not.
  const reread = (name: string, reloaded: boolean) => {
    const answered = lastRead.current
    setPending((p) => (p[name] ? { ...p, [name]: { ...p[name], answered } } : p))
    refresh()
    unitPoll.refresh()
    if (admin && hasNginx) backupsPoll.refresh()
    if (reloaded) expectReload(true)
    else pendingPoll.refresh()
  }
  /**
   * Takes in how a verb's reload went: whether nginx runs, whether it has
   * every change loaded, and — on a card the verb leaves in the list — the
   * change it did not load.
   */
  const noteReload = (
    name: string,
    res: VHostLinkResult | SiteDeleteResult,
    keepsCard: boolean,
  ): ReloadOutcome | undefined => {
    const at = Date.now()
    const outcome = reloadOutcome(res, unitNow.current)
    setReloads((r) => hear(r, at, outcome))
    const mark =
      keepsCard && (outcome === "notRunning" || outcome === "failed")
        ? markUnloaded(at, unitNow.current)
        : undefined
    setPending((p) => (p[name] ? { ...p, [name]: { ...p[name], unloaded: mark } } : p))
    return outcome
  }
  // A read that failed after the verb answered, with no good one since,
  // leaves the card with only the rows from before the change: its state
  // is not drawn from them, under a toast saying what the verb did.
  const stateOf = (name: string): { busy?: string; unread?: boolean } => {
    const busy = pending[name]
    if (!busy) return {}
    if (!busy.answered || busy.answered.read === read) return { busy: busy.verb }
    if (hasNginx && busy.answered.pending === pendingRead) return { busy: busy.verb }
    return { unread: Boolean(error) && data === busy.answered.data }
  }
  const retry = () => {
    setRetryFrom(read)
    refresh()
  }
  const failure = error ? sitesReadFailure(error) : undefined

  // The whole of what nginx printed, one press away from the toast that
  // carries its first line.
  const showOutput = (name: string, text: string | undefined) =>
    text
      ? { label: "Show nginx output", onClick: () => setOutput({ title: name, output: text }) }
      : undefined

  /**
   * Says when a change that landed did not reload, and returns whether it
   * said anything. With no nginx running there is nothing still serving the
   * configuration from before — "keeps serving" would claim a site is up
   * that is not — and nginx starts with the change. Any other failed reload
   * leaves whatever nginx had loaded, if it is running, which the page
   * cannot see from here and so does not claim.
   */
  const reportReload = (
    res: VHostLinkResult | SiteDeleteResult,
    outcome: ReloadOutcome | undefined,
    done: string,
    copy: ReloadCopy,
  ): boolean => {
    const failure = reloadFailure(res)
    if (!failure) return false
    const action = showOutput(res.name, reloadOutput(res))
    if (outcome === "notRunning") {
      notify.warning(`${done}; nginx is not running`, {
        description: copy.notRunning,
        duration: 12_000,
        action,
      })
    } else {
      notify.warning(`${done}, not reloaded`, {
        description: `${copy.notReloaded} ${failure}`,
        duration: 12_000,
        action,
      })
    }
    return true
  }
  /**
   * Says what a change to a link did. The change itself passed `nginx -t` —
   * a refused one comes back as a 422 carrying nginx's reason, and nothing
   * changed — but the reload after it can still fail, which the page has to
   * say.
   */
  const reportLink = (
    res: VHostLinkResult,
    outcome: ReloadOutcome | undefined,
    done: string,
    copy: ReloadCopy,
  ) => {
    if (!reportReload(res, outcome, done, copy)) notify.success(done)
  }
  const reportRefusal = (title: string, name: string, err: unknown) =>
    notify.error(title, err, {
      action: err instanceof ApiError ? showOutput(name, err.raw) : undefined,
    })

  const toggle = (vhost: VHost, enabled: boolean) => {
    const apply = async (): Promise<"reported"> => {
      setBusy(vhost.name, enabled ? "Enabling" : "Disabling")
      let outcome: ReloadOutcome | undefined
      try {
        const res = await post<VHostLinkResult>(
          `/proxy/vhosts/${encodeURIComponent(vhost.name)}/enabled`,
          { enabled, reload: true },
        )
        outcome = noteReload(vhost.name, res, true)
        if (enabled) {
          reportLink(res, outcome, `${vhost.name} enabled`, {
            notRunning: "It serves once nginx starts.",
            notReloaded: "It is not serving until nginx reloads.",
          })
        } else {
          reportLink(res, outcome, `${vhost.name} disabled`, {
            notRunning: "nginx starts without it.",
            notReloaded: "If nginx is running, it still serves it until a reload succeeds.",
          })
        }
      } catch (err) {
        reportRefusal(`Could not ${enabled ? "enable" : "disable"} ${vhost.name}`, vhost.name, err)
      } finally {
        reread(vhost.name, outcome === "reloaded")
      }
      return "reported"
    }
    if (!enabled) {
      confirm({
        title: `Disable ${vhost.name}`,
        confirmLabel: "Disable and reload",
        description: (
          <p>
            <b>{vhost.name}</b> stops serving as soon as nginx reloads. The config file stays on
            disk.
            {vhost.linkedAs?.length ? (
              <>
                {" "}
                Disabling removes{" "}
                {vhost.linkedAs.map((alias, i) => (
                  <span key={alias}>
                    {i > 0 && " and "}
                    <code className="font-mono break-all">sites-enabled/{alias}</code>
                  </span>
                ))}
                , which {vhost.linkedAs.length === 1 ? "serves" : "serve"} it under another name.
              </>
            ) : null}{" "}
            If taking it out breaks a configuration nginx was loading, the link goes back and
            nothing changes.
          </p>
        ),
        action: apply,
      })
      return
    }
    // The name in sites-enabled is how nginx reads some other file; the
    // enable points it at this one, and that file loses its only way in.
    if (vhost.broken === "stale" && vhost.linkTarget && !vhost.targetServedElsewhere) {
      const displaced = hosts.find((h) => h.linkedAs?.includes(vhost.name))
      confirm({
        title: `Enable ${vhost.name}`,
        confirmLabel: "Enable and reload",
        description: (
          <p>
            <code className="font-mono break-all">sites-enabled/{vhost.name}</code> is how nginx
            serves{" "}
            {displaced ? (
              <b>{displaced.name}</b>
            ) : (
              <code className="font-mono break-all">{vhost.linkTarget}</code>
            )}{" "}
            now. Enabling points it at this site&apos;s file, and{" "}
            {displaced ? (
              <>
                <b>{displaced.name}</b> stops serving once nginx reloads, until it is enabled under
                its own name.
              </>
            ) : (
              "nginx stops reading that file once it reloads; nothing on this page links it back."
            )}{" "}
            If nginx refuses {vhost.name}, the link goes back and nothing changes.
          </p>
        ),
        action: apply,
      })
      return
    }
    void apply()
  }

  /** Takes out a link in sites-enabled that no site's switch owns, and says how it went. */
  const removeLink = async (vhost: VHost): Promise<"reported"> => {
    setBusy(vhost.name, "Removing")
    let outcome: ReloadOutcome | undefined
    try {
      const res = await del<VHostLinkResult>(`/proxy/vhosts/${encodeURIComponent(vhost.name)}/link`)
      // A site in sites-available stays listed, now disabled; a link
      // that was all there was leaves the list with its card.
      outcome = noteReload(vhost.name, res, vhost.layout === "sites-available")
      reportLink(res, outcome, `sites-enabled/${vhost.name} removed`, {
        notRunning: "nginx starts without it.",
        notReloaded:
          "If nginx is running, it keeps the configuration from before until a reload succeeds.",
      })
    } catch (err) {
      reportRefusal(`Could not remove sites-enabled/${vhost.name}`, vhost.name, err)
    } finally {
      reread(vhost.name, outcome === "reloaded")
    }
    return "reported"
  }
  /** What a link removal asks, from a card or from the overview's finding. */
  const linkRemoval = (vhost: VHost) => ({
    title: `Remove sites-enabled/${vhost.name}`,
    confirmLabel: "Remove and reload",
    description:
      vhost.broken === "dangling" ? (
        <p>
          The link points at <code className="font-mono break-all">{vhost.linkTarget}</code>, which
          is missing. Removing it is what lets nginx load its configuration again; nothing else is
          deleted.
        </p>
      ) : (
        <p>
          nginx stops reading <code className="font-mono break-all">{vhost.linkTarget}</code> once
          it reloads. The file itself stays where it is, but nothing on this page links it back.
        </p>
      ),
  })
  const unlink = (vhost: VHost) =>
    confirm({ ...linkRemoval(vhost), action: () => removeLink(vhost) })
  // A save of the site as the form reads it with the switch changed, which
  // the server refuses for a file the form would not write back whole.
  const maintenance = (vhost: VHost, on: boolean) => {
    const apply = async (c?: string) => {
      setBusy(vhost.name, on ? "Starting maintenance" : "Ending maintenance")
      let reloaded = false
      try {
        const res = await post<SiteResult>(
          `/proxy/sites/${encodeURIComponent(vhost.name)}/maintenance`,
          { on },
          { confirm: c },
        )
        if (res.reloadError) {
          notify.warning(`${vhost.name} saved, but nginx did not reload`, {
            description: res.reloadError,
          })
        } else if (!on) {
          notify.success(`${vhost.name} is out of maintenance`)
        }
        reloaded = !res.reloadError
      } finally {
        reread(vhost.name, reloaded)
      }
    }
    if (on) {
      confirm({
        title: "Start maintenance",
        confirmLabel: "Start and reload",
        description: (
          <p>
            Visitors to <b>{vhost.name}</b> get its maintenance page with a 503 as soon as nginx
            reloads. Addresses let past it in the site form, and certificate renewals, still reach
            the site. Edit the page and those addresses in the form.
          </p>
        ),
        action: apply,
      })
      return
    }
    apply().catch((err) => notify.error("Could not end maintenance", err))
  }

  const remove = (vhost: VHost) =>
    confirm({
      title: `Delete ${vhost.name}`,
      confirmLabel: "Delete and reload",
      description: (
        <p>
          The file and its symlink are removed and nginx reloads. The previous content is kept
          beside it as <code className="font-mono">{vhost.name}.bak</code>, which nginx does not
          read and this list does not show.
        </p>
      ),
      action: async () => {
        setBusy(vhost.name, "Deleting")
        let outcome: ReloadOutcome | undefined
        try {
          const res = await del<SiteDeleteResult>(`/proxy/sites/${encodeURIComponent(vhost.name)}`)
          outcome = noteReload(vhost.name, res, false)
          // The file is gone, and a running nginx still serves what it
          // loaded: "completed" would send the operator away from a site
          // that is up.
          const reported = reportReload(res, outcome, `${vhost.name} deleted`, {
            notRunning: "nginx starts without it.",
            notReloaded: "If nginx is running, it still serves it until a reload succeeds.",
          })
          return reported ? "reported" : undefined
        } finally {
          reread(vhost.name, outcome === "reloaded")
        }
      },
    })

  const retire = (vhost: VHost) =>
    confirm({
      ...routeRetirement(vhost),
      action: async () => {
        setBusy(vhost.name, "Removing")
        try {
          return await retireRoute(vhost)
        } finally {
          reread(vhost.name, false)
        }
      },
    })

  /**
   * Moves a site to a new name. A refusal is thrown back into the dialog,
   * which keeps the typed name; on success whatever pointed at the old name
   * on this page — the URL's selection, the cursor, a ticked box — follows
   * it, since that card is gone.
   */
  const rename = async (vhost: VHost, to: string) => {
    setBusy(vhost.name, "Renaming")
    let outcome: ReloadOutcome | undefined
    try {
      const res = await post<SiteRenameResult>(
        `/proxy/sites/${encodeURIComponent(vhost.name)}/rename`,
        { to, reload: true },
      )
      outcome = noteReload(vhost.name, res, false)
      if (requested === vhost.name) setRequested(res.name)
      const moved = siteKey({ ...vhost, name: res.name })
      setCursor((c) => (c === siteKey(vhost) ? moved : c))
      setSelected((list) => list.map((name) => (name === vhost.name ? res.name : name)))
      reportLink(res, outcome, `${vhost.name} renamed to ${res.name}`, {
        notRunning: "nginx starts with it under the new name.",
        notReloaded:
          "If nginx is running, it keeps serving the site as it loaded it until a reload succeeds.",
      })
      for (const warning of res.warnings)
        notify.warning(`${res.name}: moved unchanged`, { description: warning })
    } finally {
      reread(vhost.name, outcome === "reloaded")
    }
  }

  /** A site an import or a restore added: said like any verb's reload, then read. */
  const placed = (res: SitePlacementResult, done: string) => {
    let outcome: ReloadOutcome | undefined
    try {
      outcome = res.enabled ? noteReload(res.name, res, false) : undefined
      reportLink(res, outcome, done, {
        notRunning: "nginx starts with it.",
        notReloaded: res.enabled
          ? "It is not serving until nginx reloads."
          : "It is in sites-available, not enabled.",
      })
      for (const warning of res.warnings) notify.warning(res.name, { description: warning })
    } finally {
      reread(res.name, outcome === "reloaded")
    }
  }

  /** What an NPM import added: said like a bulk change's reload, then read. */
  const npmImported = (res: NpmImportResult) => {
    const count = res.sites.length + res.disabled.length + res.streams.length + res.authFiles.length
    const title = `${plural(count, "item")} imported from Nginx Proxy Manager`
    const outcome = reloadOutcome(res, unitNow.current)
    setReloads((r) => hear(r, Date.now(), outcome))
    const failure = reloadFailure(res)
    if (!failure) {
      notify.success(title, {
        description: res.added.length
          ? `Also added, because a site uses it: ${res.added.join(", ")}.`
          : undefined,
      })
    } else {
      notify.warning(`${title}, not reloaded`, {
        description: `If nginx is running, it keeps the configuration from before until a reload succeeds. ${failure}`,
        duration: 12_000,
        action: showOutput(title, reloadOutput(res)),
      })
    }
    refresh()
    unitPoll.refresh()
    if (outcome === "reloaded") expectReload(true)
    else pendingPoll.refresh()
  }

  // A deployment's route, opened for editing all the same: the form where
  // it saves the file back, the raw editor otherwise.
  const override = (vhost: VHost) => {
    const owner = activeOwner(vhost)
    confirm({
      title: `Edit ${vhost.name} anyway`,
      confirmLabel: "Edit anyway",
      description: (
        <p>
          <b>{owner?.project ?? vhost.name}</b> writes this route on every deploy. The next deploy
          replaces whatever is changed here with what the deployment&apos;s own settings say, so a
          lasting change belongs there.
        </p>
      ),
      action: async (): Promise<"reported"> => {
        // Asked here, so the site's Settings open on the form rather than on
        // the same question.
        if (vhost.formEditable) router.push(`${siteSettingsHref(vhost.name)}&anyway=1`)
        else setEditing({ vhost, override: true })
        return "reported"
      },
    })
  }

  // Measured first, so the confirmation says what is about to go.
  const purgeCache = async (vhost: VHost) => {
    const path = `/proxy/sites/${encodeURIComponent(vhost.name)}/cache`
    let usage: SiteCacheUsage
    try {
      usage = await get<SiteCacheUsage>(path)
    } catch (err) {
      notify.error("Could not read the cache", err)
      return
    }
    confirm({
      title: `Purge the cache of ${vhost.name}`,
      confirmLabel: "Purge cache",
      description: usage.exists ? (
        <p>
          {bytes(usage.bytes)} in {plural(usage.files, "file")} under{" "}
          <code className="font-mono">{usage.path}</code> is removed. Every request then reaches the
          application until the cache fills again.
        </p>
      ) : (
        <p>Nothing has been cached for {vhost.name} yet, so there is nothing to remove.</p>
      ),
      action: async () => {
        setBusy(vhost.name, "Purging cache")
        try {
          const purged = await del<SiteCacheUsage>(path)
          notify.success(`${bytes(purged.bytes)} purged from ${vhost.name}`)
        } finally {
          reread(vhost.name, false)
        }
      },
    })
  }

  const handlers = {
    admin,
    onEdit: (v: VHost) => editSite(v.name),
    onRaw: (v: VHost) => setEditing({ vhost: v }),
    onServed: (v: VHost) => setEditing({ vhost: v, served: true }),
    onDuplicate: (v: VHost) => openNew(v.name),
    onToggle: toggle,
    onMaintenance: maintenance,
    onPurgeCache: purgeCache,
    onDelete: remove,
    onRename: setRenaming,
    onUnlink: unlink,
    onOverride: override,
    onRetire: retire,
    traffic: hasTraffic,
    history: hasHistory,
  }

  /**
   * Enables, disables or deletes the chosen sites as one change: the backend
   * runs nginx -t once over all of them and puts every one back if it
   * refuses, so the answer is about the whole set.
   */
  const runBulk = async (action: BulkAction, targets: VHost[]): Promise<"reported"> => {
    const names = targets.map((v) => v.name)
    const words = BULK[action]
    const title = `${plural(names.length, "site")} ${words.done}`
    const each = (p: Record<string, Busy>, next: (name: string) => Busy | undefined) => {
      const out = { ...p }
      for (const name of names) {
        const busy = next(name)
        if (busy) out[name] = busy
      }
      return out
    }
    setPending((p) => each(p, (name) => ({ verb: words.busy, unloaded: p[name]?.unloaded })))
    let outcome: ReloadOutcome | undefined
    try {
      const res = await post<SitesBulkResult>("/proxy/sites/bulk", { action, names })
      const at = Date.now()
      outcome = reloadOutcome(res, unitNow.current)
      setReloads((r) => hear(r, at, outcome))
      const mark =
        action !== "delete" && (outcome === "notRunning" || outcome === "failed")
          ? markUnloaded(at, unitNow.current)
          : undefined
      setPending((p) => each(p, (name) => p[name] && { ...p[name], unloaded: mark }))
      setSelected([])
      const failure = reloadFailure(res)
      const already = res.unchanged.length
        ? `${plural(res.unchanged.length, "site was", "sites were")} already ${words.done}.`
        : undefined
      if (!failure) {
        notify.success(title, { description: already })
      } else if (outcome === "notRunning") {
        notify.warning(`${title}; nginx is not running`, {
          description: "nginx starts with the change.",
          duration: 12_000,
          action: showOutput(title, reloadOutput(res)),
        })
      } else {
        notify.warning(`${title}, not reloaded`, {
          description: `If nginx is running, it keeps the configuration from before until a reload succeeds. ${failure}`,
          duration: 12_000,
          action: showOutput(title, reloadOutput(res)),
        })
      }
    } catch (err) {
      reportRefusal(`Could not ${action} ${plural(names.length, "site")}`, title, err)
    } finally {
      const answered = lastRead.current
      setPending((p) => each(p, (name) => p[name] && { ...p[name], answered }))
      refresh()
      unitPoll.refresh()
      if (outcome === "reloaded") expectReload(true)
      else pendingPoll.refresh()
    }
    return "reported"
  }
  const bulk = (action: BulkAction, targets: VHost[]) => {
    // Enabling is the switch a single site takes without asking.
    if (action === "enable") {
      void runBulk(action, targets)
      return
    }
    const words = BULK[action]
    confirm({
      title: `${words.word} ${plural(targets.length, "site")}`,
      confirmLabel: `${words.word} and reload`,
      description: (
        <>
          <p>
            {action === "disable"
              ? "These stop serving as soon as nginx reloads. Their files stay on disk."
              : "Their files and links are removed and nginx reloads. Each file's previous content is kept beside it as <name>.bak, which nginx does not read and this list does not show."}{" "}
            nginx tests the result once; if it refuses, or any one of them cannot be changed, every
            site is put back and nothing changes.
          </p>
          <p className="font-mono text-hint break-words">{targets.map((v) => v.name).join(", ")}</p>
        </>
      ),
      action: () => runBulk(action, targets),
    })
  }
  /** The chosen sites' files, as they are on disk now, in one text file. */
  const exportSites = async (targets: VHost[]) => {
    setExporting(true)
    try {
      const files = await Promise.all(
        targets.map(async (v) => ({
          path: v.path,
          content: (await get<{ content: string }>("/proxy/config", { path: v.path })).content,
        })),
      )
      const at = new Date()
      downloadText(
        `nginx-sites-${at.toISOString().slice(0, 10)}.conf`,
        exportBundle(files, at),
        "text/plain",
      )
      notify.success(`${plural(files.length, "site")} exported`)
    } catch (err) {
      notify.error("Could not export the sites", err)
    } finally {
      setExporting(false)
    }
  }

  // The keys: / searches, n starts a site, j and k move through the list,
  // and Enter opens the page of the site they are on. Never while typing, and never
  // under a sheet, a dialog or a menu, which have keys of their own.
  const onKey = useRef<(e: KeyboardEvent) => void>(undefined)
  useEffect(() => {
    onKey.current = (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return
      const target = e.target instanceof HTMLElement ? e.target : null
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return
      if (form.open || rawSite || document.querySelector("[role='dialog'], [role='menu']")) return
      if (e.key === "/") {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (e.key === "n" && admin && hasNginx) {
        e.preventDefault()
        openNew()
      } else if (e.key === "j" || e.key === "k") {
        e.preventDefault()
        setCursor(stepSelection(visible.map(siteKey), cursor, e.key === "j" ? 1 : -1))
      } else if (e.key === "Enter" && cursor && (!target || target === document.body)) {
        const site = visible.find((v) => siteKey(v) === cursor)
        if (site) {
          e.preventDefault()
          router.push(sitePath(site.name))
        }
      }
    }
  })
  useEffect(() => {
    const listener = (e: KeyboardEvent) => onKey.current?.(e)
    window.addEventListener("keydown", listener)
    return () => window.removeEventListener("keydown", listener)
  }, [])

  // A save that reloaded waits a moment for nginx to load it; one that did
  // not is read at once, and waiting for a load nobody asked for kept its
  // card reading "serving" for the whole wait.
  const saved = (reloaded: boolean) => {
    refresh()
    if (reloaded) expectReload(false)
    else pendingPoll.refresh()
  }

  const header = <PageContext eyebrow="Proxy" title="Sites" />

  // The cards wait for systemd's reading too, and for what nginx has not
  // loaded: drawn before either, a stopped nginx's sites — or an edit nginx
  // never read — read "serving" until it arrived.
  if ((loading && !data) || statusLoading || unitPoll.loading || pendingPoll.loading) {
    return (
      <Page>
        {header}
        {/* The page's own silhouette: the line it opens on and the rows
            under it, so nothing moves when the sites land. */}
        <HostIdentity
          fallback={Globe}
          title={<TextShimmer>Reading the sites…</TextShimmer>}
          facts={<span>&nbsp;</span>}
          state={<span>&nbsp;</span>}
        />
        <LoadingRows rows={6} />
      </Page>
    )
  }
  if (error && !data) {
    return (
      <Page>
        {header}
        {/* The list is read from nginx, a host Caddy and the ingress
            container, and the last can fail on a server where the first two
            are fine: the block says which, why, and can be asked again. */}
        <ErrorState error={sitesReadError(error)} onRetry={retry} retryable />
      </Page>
    )
  }

  const newSite = admin && hasNginx && (
    <Button size="sm" onClick={() => openNew()} aria-keyshortcuts="n" title="New site (N)">
      <Plus className="size-4" />
      New site
    </Button>
  )
  // Every site's file in a tar.gz with a manifest (password files are never
  // in it), and the way to bring one in: the list's two other commands.
  const listVerbs: Verb[] =
    admin && hasNginx
      ? [
          { key: "import", label: "Import", icon: CloudUpload, run: () => setImporting(true) },
          {
            key: "export",
            label: "Export all",
            icon: Download,
            run: () => downloadFrom("/proxy/sites-export"),
          },
        ]
      : []

  // Grouped only where the grouping says something a chip has not: once a
  // filter is on, its name *is* the group.
  // Nor once another order is chosen: the groups are the urgency order.
  const narrowed = filter.trim().length > 0 || chip !== "all" || sort !== "urgency"
  const active = visible.filter((v) => v.enabled)
  // Off, and nothing to decide about — the distribution's untouched default
  // site, a conf.d file nginx does not read. Drawn under "Serving", the
  // stock default read as one of the sites that serve.
  const idle = visible.filter((v) => !v.enabled)
  const sorted: { key: string; label: string; sites: VHost[] }[] = [
    { key: "serving", label: active.some(engineStopped) ? "Enabled" : "Serving", sites: active },
    { key: "idle", label: "Not in use", sites: idle },
  ].filter((group) => group.sites.length > 0)
  const groups =
    narrowed || sorted.length < 2 ? [{ key: "all", label: "", sites: visible }] : sorted
  const disabledServed = hosts.filter((v) => isDisabled(v) && notLive(v)).length

  // The line's facts, read over every site rather than the ones a filter
  // leaves, so narrowing the list does not narrow what the host is.
  const serving = hosts.filter(
    (v) => v.enabled && !v.broken && !engineStopped(v) && !isUnloaded(v) && !notLive(v),
  ).length
  const hour = hasTraffic
    ? hosts.reduce((sum, v) => sum + (siteTraffic(v, trafficPoll.data)?.requests ?? 0), 0)
    : undefined
  const busiest = Math.max(0, ...hosts.map((v) => siteTraffic(v, trafficPoll.data)?.requests ?? 0))
  const verdict = sitesVerdict({
    stopped: run?.running === false,
    engine: engineName,
    total: hosts.length,
  })
  const engines = [
    status?.nginx && { id: "nginx-static", name: status.nginxVersion || "nginx" },
    (status?.caddy || counts.caddy > 0) && {
      id: "caddy",
      name: status?.caddyVersion || (status?.ingressContainer ? "Caddy ingress" : "Caddy"),
    },
  ].filter((e): e is { id: string; name: string } => Boolean(e))
  // The host's sites as the applications they put a domain on, the commonest
  // first; a host whose sites proxy nothing it can name is its engines.
  const fronted = new Map<string, number>()
  for (const v of hosts) {
    const product = v.enabled ? appOf(v)?.product : undefined
    if (product) fronted.set(product, (fronted.get(product) ?? 0) + 1)
  }
  const marks = [...fronted.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id)
  const tiles = marks.length > 0 ? marks : engines.map((e) => e.id)

  return (
    <Page className="animate-rise">
      {header}

      {/* What the host serves, as one line: its sites as the applications
          they front, beside the name the verdict — a press of which narrows
          the rows to exactly what it counts — then the engines, how many
          serve and on TLS and the hour's requests, and at its far end the
          command that adds a site with the rest behind the menu. Four tiles
          stood here, each one count with no site named in it; site-band.tsx
          says where each went. */}
      <HostIdentity
        className="animate-rise"
        logo={
          tiles.length > 0 ? (
            <span className="flex h-12 shrink-0 items-center">
              <ProductLogos ids={tiles} size="md" />
            </span>
          ) : undefined
        }
        fallback={Globe}
        title={host?.hostname ?? "This server"}
        facts={
          <>
            {engines.map((engine, i) => (
              <Fragment key={engine.id}>
                {i > 0 && <FactDot />}
                <HostFact product={engine.id}>{engine.name}</HostFact>
              </Fragment>
            ))}
            {engines.length > 0 && <FactDot />}
            <span className="numeric">{plural(counts.all, "site")}</span>
            <FactDot />
            <span className="numeric">{serving} serving</span>
            <FactDot />
            <span className="numeric">{counts.tls} on TLS</span>
            {hour !== undefined && (
              <>
                <FactDot />
                <span className="numeric">{compact.format(hour)} requests in the hour</span>
              </>
            )}
            {counts.disabled > 0 && (
              <>
                <FactDot />
                <span className="numeric">
                  {counts.disabled} disabled
                  {disabledServed > 0 && (
                    <>
                      , <span className="text-warning">{disabledHint(disabledServed)}</span>
                    </>
                  )}
                </span>
              </>
            )}
          </>
        }
        state={
          <Verdict
            verdict={verdict}
            pressed={Boolean(verdict.chip) && chip === verdict.chip}
            onPress={() => verdict.chip && setChip(chip === verdict.chip ? "all" : verdict.chip)}
          />
        }
        // An empty list carries the command in its own empty state.
        command={hosts.length > 0 && newSite}
        menu={listVerbs.length > 0 && <VerbMenu verbs={listVerbs} label="More site actions" />}
      />

      {/* The rows stay on a failed read, and would pass for what nginx
          serves now: the readings and cards below are from the last good one. */}
      {error && (
        <div role="alert">
          <Notice tone="warning" icon={Warning} title="Could not read the sites again">
            <p className="break-words">{failure?.message}</p>
            {failure?.reason && <p className="break-words">{failure.reason}</p>}
            <p>The sites below are from the last read, and may be out of date.</p>
            {failure?.raw && (
              <details className="mt-1 min-w-0">
                <summary className="cursor-pointer hover:text-foreground">
                  What the server said
                </summary>
                <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
                  {failure.raw}
                </pre>
              </details>
            )}
            <Button
              size="xs"
              variant="outline"
              className="mt-1.5"
              onClick={retry}
              disabled={retrying}
            >
              {retrying ? "Reading…" : "Try again"}
            </Button>
          </Notice>
        </div>
      )}

      {run?.running === false && (
        <Notice tone="warning" icon={Warning} title={`${engineName} is not running`}>
          <p>
            {run.from === "unit" && unitReading
              ? `systemd reports ${unitReading.unit.name} ${unitState(unitReading.unit)}, so`
              : `The last reload found no ${engineName} process to signal, so`}{" "}
            no {engineName} site below is served until it starts.
          </p>
          {admin && run.from === "unit" && (
            <p>
              <Link href="/proxy" className="underline underline-offset-2">
                Start it from the Overview
              </Link>
            </p>
          )}
        </Notice>
      )}

      {run?.running !== false && pendingPoll.data && (
        <PendingStrip
          pending={pendingPoll.data}
          nginxDir={status?.nginxDir}
          admin={admin}
          onChanged={() => {
            refresh()
            pendingPoll.refresh()
          }}
          onOutput={(title, text) => setOutput({ title, output: text })}
        />
      )}

      {hosts.length > 0 && (
        <SiteBand
          hosts={hosts}
          appOf={appOf}
          healthOf={(v) => siteUpstreams(v, upstreamPoll.data)}
          traffics={hasNginx}
          traffic={trafficPoll.data}
          trafficError={trafficPoll.error}
          certs={certPoll.data}
          certsLoading={certPoll.loading}
          onChip={setChip}
        />
      )}

      {hosts.length === 0 ? (
        <EmptyState
          mark={<ProductLogos ids={["nginx-static", "caddy"]} size="md" />}
          title="No sites found"
          description={
            hasNginx
              ? "Put a domain in front of something running on this machine — the form writes the nginx config for you."
              : "No nginx configuration directory, Caddyfile or shared Caddy ingress was found on this host."
          }
          action={newSite}
        />
      ) : (
        <Panel plain aria-label="Sites">
          <PanelHeader
            title="Sites"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {visible.length === hosts.length
                  ? plural(hosts.length, "site")
                  : `${visible.length} of ${plural(hosts.length, "site")}`}
              </span>
            }
          />
          <PanelToolbar className="justify-between gap-x-4">
            <SearchInput
              ref={searchRef}
              dense
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Name, domain, upstream, port, file or path"
              aria-label="Search sites"
              aria-keyshortcuts="/"
            />
            <Select value={sort} onValueChange={(value) => setSort(value as SiteSort)}>
              <SelectTrigger size="sm" className="w-40" aria-label="Sort sites">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(SORT_LABEL) as SiteSort[])
                  .filter((key) => key !== "traffic" || hasTraffic)
                  .map((key) => (
                    <SelectItem key={key} value={key}>
                      {SORT_LABEL[key]}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
            <ChipStrip className="basis-full" aria-label="Filter sites">
              {(Object.keys(CHIP_LABEL) as SiteChip[])
                .filter((key) => key === "all" || key === chip || chipCounts[key] > 0)
                .map((key) => (
                  <FilterChip key={key} selected={chip === key} onClick={() => setChip(key)}>
                    <span className={CHIP_TONE[key]}>{CHIP_LABEL[key]}</span>
                    <ChipCount>{chipCounts[key]}</ChipCount>
                  </FilterChip>
                ))}
            </ChipStrip>
          </PanelToolbar>
          <PanelBody flush className="flex min-w-0 flex-col gap-3 pt-3">
            {chosen.length > 0 && (
              <div
                role="region"
                aria-label="Selected sites"
                className="flex flex-wrap items-center gap-2 rounded-xl border bg-card px-3 py-2"
              >
                <span className="text-body font-medium">{chosen.length} selected</span>
                {chosen.length < allChosen.length && (
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() => setSelected(allChosen.map((v) => v.name))}
                  >
                    Select all {allChosen.length}
                  </Button>
                )}
                <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
                  {toEnable.length > 0 && (
                    <Button size="xs" variant="outline" onClick={() => bulk("enable", toEnable)}>
                      Enable {toEnable.length}
                    </Button>
                  )}
                  {toDisable.length > 0 && (
                    <Button size="xs" variant="outline" onClick={() => bulk("disable", toDisable)}>
                      Disable {toDisable.length}
                    </Button>
                  )}
                  {toExport.length > 0 && (
                    <Button
                      size="xs"
                      variant="outline"
                      pending={exporting}
                      onClick={() => void exportSites(toExport)}
                    >
                      Export {toExport.length}
                    </Button>
                  )}
                  {toDelete.length > 0 && (
                    <Button size="xs" variant="outline" onClick={() => bulk("delete", toDelete)}>
                      Delete {toDelete.length}
                    </Button>
                  )}
                  <Button size="xs" variant="ghost" onClick={() => setSelected([])}>
                    Clear
                  </Button>
                </div>
              </div>
            )}

            {visible.length === 0 ? (
              <EmptyNote className="py-6">
                {filter.trim()
                  ? `No site matches “${filter.trim()}”`
                  : "No site is under this chip"}
                {filter.trim() && chip !== "all" ? " under this chip." : "."}
              </EmptyNote>
            ) : (
              groups.map((group) => (
                <div key={group.key} className="flex min-w-0 flex-col gap-2">
                  {group.label && <GroupRule label={group.label} count={group.sites.length} />}
                  <ChoiceList aria-label={group.label || "Sites"}>
                    {group.sites.map((vhost, index) => (
                      <SiteRow
                        key={siteKey(vhost)}
                        index={index}
                        vhost={vhost}
                        {...stateOf(vhost.name)}
                        stopped={engineStopped(vhost)}
                        unloaded={isUnloaded(vhost)}
                        notLive={notLive(vhost)}
                        ambiguous={shared.has(vhost.name)}
                        cert={siteCert(vhost, certPoll.data)}
                        health={siteUpstreams(vhost, upstreamPoll.data)}
                        app={appOf(vhost)}
                        hour={siteTraffic(vhost, trafficPoll.data)}
                        busiest={busiest}
                        arrived={arrived.has(siteKey(vhost))}
                        selectable={allChosen.includes(vhost)}
                        selected={selected.includes(vhost.name)}
                        onSelectedChange={(on) => toggleSelected(vhost, on)}
                        cursor={cursor === siteKey(vhost)}
                        {...handlers}
                      />
                    ))}
                  </ChoiceList>
                </div>
              ))
            )}
          </PanelBody>
        </Panel>
      )}

      {hasNginx && <DefaultSitePanel admin={admin} />}

      {admin && hasNginx && <AuthFilesPanel />}
      {admin && hasNginx && <AccessListsPanel />}
      {admin && hasNginx && <RouteResolver />}

      {admin && hasNginx && (
        <SiteBackupsPanel
          backups={backupsPoll}
          onRestored={(res) => placed(res, `${res.name} restored`)}
        />
      )}

      <SiteForm
        open={form.open}
        editing={null}
        copyFrom={form.copyFrom}
        session={form.session}
        onOpenChange={closeForm}
        onSaved={saved}
      />
      <ConfigEditor
        open={rawSite !== undefined}
        onOpenChange={closeRaw}
        path={(rawServed ? rawSite?.enabledPath : rawSite?.path) ?? ""}
        kind={rawSite?.kind ?? "nginx"}
        title={rawServed ? `sites-enabled/${rawSite?.name}` : (rawSite?.name ?? "Configuration")}
        readOnly={rawReadOnly}
        siteDisabled={
          !rawServed &&
          rawSite?.kind === "nginx" &&
          Boolean(rawSite.enabledPath) &&
          !rawSite.enabled
        }
        onSaved={saved}
        actions={(busy) =>
          rawSite &&
          !rawServed && (
            <SiteFileVerbs
              vhost={rawSite}
              admin={admin}
              busy={busy}
              onEdit={(v) => {
                setEditing(null)
                editSite(v.name)
              }}
            />
          )
        }
      />
      <ConfirmDialog
        request={
          linkedRemoval
            ? { ...linkRemoval(linkedRemoval), action: () => removeLink(linkedRemoval) }
            : null
        }
        onOpenChange={(open) => !open && setRequested(null)}
      />
      <SiteImportDialog
        open={importing}
        onOpenChange={setImporting}
        onImported={(res) => placed(res, `${res.name} imported`)}
        onNpmImported={npmImported}
      />
      <SiteRenameDialog
        vhost={renaming}
        onOpenChange={(open) => !open && setRenaming(null)}
        onRename={rename}
      />
      <Modal
        open={output !== null}
        onOpenChange={(open) => !open && setOutput(null)}
        title={`nginx output — ${output?.title ?? ""}`}
        size="lg"
        initialFocus="body"
      >
        <Well className="max-h-[60svh] break-all whitespace-pre-wrap">{output?.output}</Well>
      </Modal>
      {dialog}
    </Page>
  )
}

type CardProps = {
  vhost: VHost
  busy?: string
  /** Changed by a verb, and not read back since: see `ServingStatus`. */
  unread?: boolean
  /** Its engine is not running. */
  stopped?: boolean
  /** Changed by a verb that nginx did not reload. */
  unloaded?: boolean
  /** Its file changed since nginx loaded its configuration: see `ServingStatus`. */
  notLive?: string
  index: number
  ambiguous: boolean
  /** The certificate it serves that ends soonest, when the inventory has it. */
  cert?: Certificate
  /** Its upstreams as the health check last found them. */
  health: SiteUpstreamHealth[]
  /** What the site hands its requests to, where it proxies. */
  app?: SiteApp
  /** Its last hour, when the traffic summary could read its log. */
  hour?: SiteTrafficReading
  /** The busiest site's hour, which a card's meter is drawn against. */
  busiest: number
  /** New since the last read of the list: it rises in (§11). */
  arrived: boolean
  selectable: boolean
  selected: boolean
  onSelectedChange: (selected: boolean) => void
  /** The site j and k have moved to. */
  cursor: boolean
  /** The traffic and history pages answer; see `useSiteVerbs`. */
  traffic: boolean
  history: boolean
  admin: boolean
  onEdit: (v: VHost) => void
  onRaw: (v: VHost) => void
  onServed: (v: VHost) => void
  onDuplicate: (v: VHost) => void
  onToggle: (v: VHost, enabled: boolean) => void
  onMaintenance: (v: VHost, on: boolean) => void
  onPurgeCache: (v: VHost) => void
  onDelete: (v: VHost) => void
  onRename: (v: VHost) => void
  onUnlink: (v: VHost) => void
  onOverride: (v: VHost) => void
  onRetire: (v: VHost) => void
}

/**
 * A site as one line, the same height whatever it holds: what it fronts with
 * the engine serving it in the tile's corner, as the overview's Routes list
 * draws it, its name over what it is and where its requests go,
 * then in aligned columns its last hour, the ports it listens on, its TLS
 * and how long its certificate has, and its state. Opening it is the site's
 * page, for every site and every reader: a Docker Caddy route with no file
 * to edit still has requests to read, and a link to nothing is explained
 * there. Everything else it can do is in its menu, the same verbs the card
 * drew as three buttons and a menu.
 *
 * The state is one word: an upstream that refuses is what its visitors meet,
 * so it takes the place of "serving"; anything the switch or nginx says about
 * the site itself is said first.
 */
function SiteRow({
  vhost,
  busy,
  unread,
  stopped,
  unloaded,
  notLive,
  index,
  ambiguous,
  cert,
  health,
  app,
  hour,
  busiest,
  arrived,
  selectable,
  selected,
  onSelectedChange,
  cursor,
  ...handlers
}: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ambiguous, ...handlers })
  // A link to nothing has no domain or upstream to draw, only where it points.
  const linkOnly = vhost.broken === "dangling" && !vhost.path
  const names = vhost.serverNames.join(", ")
  const destination = siteDestination(vhost) || "served by configuration"
  const kind = app?.name ? `${app.name} · ${siteKind(vhost)}` : siteKind(vhost)
  const ports = listenPorts(vhost.listen)
  // The words the switch and nginx have about the site come before its
  // upstream's: a site that is off has no visitors for a dead upstream to fail.
  const plainlyServing =
    !busy && !unread && !vhost.broken && !stopped && !unloaded && !notLive && vhost.enabled
  const down = plainlyServing && health.some(isDown)
  return (
    <ChoiceRow
      href={sitePath(vhost.name)}
      verb={`Open ${vhost.name}`}
      index={index}
      busy={Boolean(busy)}
      className={cn(
        cursor && "ring-2 ring-ring",
        arrived && "animate-rise",
        !vhost.enabled && "opacity-80",
      )}
      leading={
        <>
          {cursor && <ScrollHere />}
          {/* One tile on every row, so the names stand on one edge: what
              the site's visitors reach, the engine in its corner. */}
          <RouteMark
            vhost={vhost}
            backend={{
              kind: isRedirect(vhost)
                ? "redirect"
                : isStatic(vhost)
                  ? "files"
                  : vhost.upstreams.length > 0
                    ? "app"
                    : "config",
              product: app?.product,
              projectId: activeOwner(vhost)?.projectId,
            }}
          />
        </>
      }
      title={vhost.name}
      description={
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="shrink-0 max-sm:hidden">{kind}</span>
          {!linkOnly && (
            <>
              <span aria-hidden className="shrink-0 max-sm:hidden">
                ·
              </span>
              <span
                data-slot="site-route"
                className="flex min-w-0 items-center gap-1.5 font-mono"
                title={`${names || "Default host"} → ${destination}`}
              >
                {/* The names are said once: where they are the site's own
                    name, the title already says them. */}
                {names !== vhost.name && (
                  <>
                    <span className={cn("truncate", !names && "font-sans")}>
                      {names || "Default host"}
                    </span>
                    <ArrowRight aria-hidden className="size-3 shrink-0" />
                  </>
                )}
                <span className="truncate text-foreground">{destination}</span>
              </span>
            </>
          )}
        </span>
      }
      trailing={
        <>
          {/* The hour's column is there on a host whose traffic summary
              answers, on every row or on none, so the columns after it stand
              in one line down the list. */}
          {handlers.traffic && (
            <span className="flex w-28 justify-end max-xl:hidden">
              {hour && !linkOnly && <HourMeter vhost={vhost} hour={hour} busiest={busiest} />}
            </span>
          )}
          <span
            className="w-16 truncate text-right font-mono text-hint text-muted-foreground max-lg:hidden"
            title={vhost.listen.join(" · ")}
          >
            {linkOnly ? "" : ports.map((port) => `:${port}`).join(" ")}
          </span>
          <span className="flex w-24 text-hint max-md:hidden">
            {!linkOnly && <SiteTLS vhost={vhost} />}
          </span>
          <span className="flex w-20 text-hint max-lg:hidden">
            {cert && !linkOnly && <ExpiryStatus cert={cert} />}
          </span>
          <span className="flex justify-end sm:w-36">
            {down ? (
              <UpstreamHealth targets={health} />
            ) : (
              <ServingStatus
                vhost={vhost}
                busy={busy}
                unread={unread}
                stopped={stopped}
                unloaded={unloaded}
                notLive={notLive}
              />
            )}
          </span>
        </>
      }
      actions={
        <>
          {/* The box keeps its place on a row that cannot be chosen, so the
              menus stand in one column. */}
          <span className="flex size-5 items-center justify-center max-sm:hidden">
            {selectable && (
              <Checkbox
                aria-label={`Select ${vhost.name}`}
                checked={selected}
                onCheckedChange={(on) => onSelectedChange(on === true)}
              />
            )}
          </span>
          {verbs.length > 0 ? (
            <VerbMenu verbs={verbs} label={`More actions for ${vhost.name}`} />
          ) : (
            <span aria-hidden className="size-8" />
          )}
        </>
      }
    />
  )
}

/** The ports a site listens on, each once, in the order nginx was told them. */
function listenPorts(listen: string[]): string[] {
  const ports: string[] = []
  for (const entry of listen) {
    const port = entry.match(/(\d+)(?!.*\d)/)?.[1]
    if (port && !ports.includes(port)) ports.push(port)
  }
  return ports
}

/**
 * The site's last hour: a short bar against the busiest site's, in the
 * site's hue, its requests, and its share answered with a 5xx where that is
 * worth saying.
 */
function HourMeter({
  vhost,
  hour,
  busiest,
}: {
  vhost: VHost
  hour: SiteTrafficReading
  busiest: number
}) {
  const tone = errorTone(hour.errorRate)
  return (
    <span
      className="inline-flex items-center gap-1.5 text-hint text-muted-foreground"
      title={`${hour.requests.toLocaleString()} requests in the last hour${tone ? `, ${(hour.errorRate * 100).toFixed(1)}% answered 5xx` : ""}`}
    >
      <span aria-hidden className="h-1 w-8 overflow-hidden rounded-full bg-meter-track">
        <span
          className="block h-full rounded-full transition-[width] duration-700 ease-out"
          style={{
            width: `${busiest > 0 ? (hour.requests / busiest) * 100 : 0}%`,
            background: siteHue(vhost.name),
          }}
        />
      </span>
      <span
        className={cn(
          "numeric",
          tone === "danger"
            ? "text-destructive"
            : tone === "warning"
              ? "text-warning"
              : "text-foreground",
        )}
      >
        {compact.format(hour.requests)}
      </span>
      <span>req/h</span>
    </span>
  )
}

/**
 * The identity line's verdict. A count of sites is a question about which,
 * so a press narrows the cards to exactly them; an all-clear has nothing to
 * press.
 */
function Verdict({
  verdict,
  pressed,
  onPress,
}: {
  verdict: SitesVerdict
  pressed: boolean
  onPress: () => void
}) {
  const status = <Status tone={verdict.tone} label={verdict.label} />
  if (!verdict.chip) return status
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onPress}
      className={cn(
        "rounded-md px-1.5 py-1 focus-ring transition-colors",
        pressed ? "bg-accent" : "hover:bg-row-hover",
      )}
    >
      <span className="flex flex-col items-end gap-0.5 max-sm:items-start">
        {status}
        {verdict.detail && (
          <span className="text-hint text-muted-foreground">{verdict.detail}</span>
        )}
      </span>
    </button>
  )
}

/** The site's verbs the file's sheet carries in its header. */
const EDITOR_VERBS = ["open", "scan", "log", "errors", "edit", "deployment"]

/** Brings the site j or k moved to into view as it becomes the one. */
function ScrollHere() {
  return <span aria-hidden ref={(el) => el?.scrollIntoView({ block: "nearest" })} />
}

/**
 * The site's own verbs in the raw editor's header: the ones that still make
 * sense with the file open. Edit waits while a save is in flight. The Sites
 * list and a site's own page open the same sheet.
 */
export function SiteFileVerbs({
  vhost,
  admin,
  busy,
  onEdit,
  verbs: keys = EDITOR_VERBS,
}: {
  vhost: VHost
  admin: boolean
  busy: boolean
  onEdit: (vhost: VHost) => void
  /** Which of the site's verbs its header carries: the site's page leaves out the way to itself. */
  verbs?: string[]
}) {
  const noop = () => {}
  const verbs = useSiteVerbs({
    vhost,
    admin,
    busy: busy ? "Saving" : undefined,
    onEdit,
    onRaw: noop,
    onServed: noop,
    onDuplicate: noop,
    onToggle: noop,
    onDelete: noop,
    onRename: noop,
    onUnlink: noop,
    onOverride: noop,
  }).filter((v) => keys.includes(v.key))
  return <VerbBar verbs={verbs.map((v) => ({ ...v, inline: true }))} className="ml-auto" />
}
