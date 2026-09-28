"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { forgetSessionState, useSessionState, useViewState } from "@/lib/view-state"
import { CloudUpload, Download, Globe, Plus, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { ApiError, del, errorMessage, get, post } from "@/lib/api"
import type {
  Certificate,
  ProxyPending,
  SiteBackup,
  SiteDeleteResult,
  SitePlacementResult,
  SiteRenameResult,
  SitesBulkResult,
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
import { ConfirmDialog, useConfirm } from "@/components/confirm-dialog"
import { ChoiceRow, GroupRule } from "@/components/flow"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ProductGlyph, ProductLogo, ProductLogos } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { DefaultSitePanel } from "@/components/proxy/default-site"
import { siteProduct } from "@/components/proxy/marks"
import { SiteForm } from "@/components/proxy/site-form"
import { SiteRenameDialog } from "@/components/proxy/site-rename"
import { SiteImportDialog } from "@/components/proxy/site-import"
import { SiteBackupsPanel } from "@/components/proxy/site-backups"
import {
  ServingStatus,
  SiteFeatures,
  SiteNotes,
  SiteTLS,
  UpstreamHealth,
  siteKind,
} from "@/components/proxy/site-marks"
import { ExpiryStatus } from "@/components/proxy/expiry-status"
import {
  CHIP_LABEL,
  SORT_LABEL,
  exportBundle,
  matchesChip,
  siteCert,
  siteRequests,
  siteUpstreams,
  sortSites,
  stepSelection,
  type SiteChip,
  type SiteReadings,
  type SiteSort,
} from "@/components/proxy/site-filters"
import { activeOwner, matchesSearch, upstreamTargets } from "@/components/proxy/site-details"
import { reloadFailure, reloadOutput } from "@/components/proxy/site-outcome"
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
import { engineUnit, useProxy } from "@/components/proxy/proxy-context"
import {
  downloadFrom,
  opensFile,
  siteDeletable,
  siteSwitchable,
  useSiteVerbs,
} from "@/components/proxy/site-verbs"
import { ProxyGrid, RoutePath } from "@/components/proxy/route-path"
import { isBroken, isDisabled, isPlain, sharedNames, waiting } from "@/components/proxy/site-order"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** What a bulk change is called while it runs and once it has landed. */
const BULK = {
  enable: { word: "Enable", busy: "Enabling", done: "enabled" },
  disable: { word: "Disable", busy: "Disabling", done: "disabled" },
  delete: { word: "Delete", busy: "Deleting", done: "deleted" },
} as const

type BulkAction = keyof typeof BULK

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
 * A route needs two readable ends and commands separate from its readings.
 * The cards retain the worst-first groups and the existing editor ownership:
 * nginx opens the builder, file-backed Caddy opens its file, and Docker Caddy
 * has no editor because there is no host file to save.
 */
export function SitesPage({ hasNginx }: { hasNginx: boolean }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const { status, loading: statusLoading } = useProxy()
  const [editing, setEditing] = useState<OpenFile | null>(null)
  // The form's open state and its fields are kept for the tab: a site is a
  // long form, and checking a port or a certificate half-way through it
  // should not mean typing it again. Closing it is what forgets it.
  const [form, setForm] = useSessionState<{
    open: boolean
    editing: string | null
    copyFrom: string | null
    session: number
  }>("proxy.sites.form", { open: false, editing: null, copyFrom: null, session: 0 })
  const [filter, setFilter] = useSessionState("proxy.sites.query", "")
  const [chip, setChip] = useSessionState<SiteChip>("proxy.sites.chip", "all")
  const [sortChoice, setSort] = useSessionState<SiteSort>("proxy.sites.sort", "urgency")
  const [view, setView] = useViewState<"cards" | "table">("proxy.sites.view", "cards")
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
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<VHost[]>("/proxy/vhosts", undefined, signal),
    30_000,
  )
  // Readings other parts of the proxy own. Each draws nothing where its
  // endpoint does not answer — no certificates readable, no health check or
  // traffic summary on this build — rather than an error on a page about
  // sites, and nothing drawn is never read as "fine".
  const certPoll = usePoll(
    (signal) => get<Certificate[]>("/certificates/", undefined, signal),
    300_000,
  )
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

  const openForm = (name: string | null, copyFrom: string | null = null) => {
    setRequested(null)
    setForm((f) => ({ open: true, editing: name, copyFrom, session: f.session + 1 }))
  }

  // A ?site= link from elsewhere in the dashboard opens that site as soon as
  // its row loads. The open panel is derived from the URL rather than copied
  // into state, so the back button closes it and a reload reopens it. The
  // form is for an administrator and a file it saves back where it found it;
  // anyone else, and any other file, gets the config viewer — read-only
  // unless they may write it. A deployment's route, which a deployment
  // links to, opens read-only: its deployment is where it is changed.
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
  const formIsOpen = form.open || Boolean(linkedForm)
  const formEditing = form.open ? form.editing : (linkedForm?.name ?? null)

  const closeForm = (open: boolean) => {
    setForm((f) => ({ ...f, open }))
    if (!open) {
      setRequested(null)
      forgetSessionState("proxy.site.form.")
    }
  }
  const closeRaw = (open: boolean) => {
    if (open) return
    setEditing(null)
    setRequested(null)
  }

  const hosts = useMemo(() => data ?? [], [data])
  const shared = useMemo(() => sharedNames(hosts), [hosts])
  const counts = useMemo(
    () => ({
      all: hosts.length,
      broken: hosts.filter(isBroken).length,
      tls: hosts.filter((v) => v.tls).length,
      plain: hosts.filter(isPlain).length,
      disabled: hosts.filter(isDisabled).length,
      nginx: hosts.filter((v) => v.kind === "nginx").length,
      caddy: hosts.filter((v) => v.kind === "caddy").length,
    }),
    [hosts],
  )
  // A change nginx did not load waits for a reload; with the engine
  // stopped, the rest are enabled rather than serving, and nothing is live.
  const notLive = (v: VHost) =>
    run?.running === false ? undefined : notLiveLabel(v, siteChanges(pendingPoll.data, v))
  const readings: SiteReadings = {
    notLive: (v) => Boolean(notLive(v)),
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
  // A card whose change nginx has not loaded is not drawn as serving it.
  // Only a card with a link in sites-enabled can carry one: a verb acts on
  // that link, and a conf.d file of the same name is not what it changed.
  const isUnloaded = (v: VHost) =>
    Boolean(v.enabledPath) &&
    stillUnloaded(pending[v.name]?.unloaded, reloads.loadedAt, unitReading)
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
        if (vhost.formEditable) openForm(vhost.name)
        else setEditing({ vhost, override: true })
        return "reported"
      },
    })
  }

  const handlers = {
    admin,
    onEdit: (v: VHost) => openForm(v.name),
    onRaw: (v: VHost) => setEditing({ vhost: v }),
    onServed: (v: VHost) => setEditing({ vhost: v, served: true }),
    onDuplicate: (v: VHost) => openForm(null, v.name),
    onToggle: toggle,
    onDelete: remove,
    onRename: setRenaming,
    onUnlink: unlink,
    onOverride: override,
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
  // and Enter opens the site they are on. Never while typing, and never
  // under a sheet, a dialog or a menu, which have keys of their own.
  const openSite = (v: VHost) => {
    if (admin && v.formEditable && !activeOwner(v)) openForm(v.name)
    else if (opensFile(v)) setEditing({ vhost: v })
  }
  const onKey = useRef<(e: KeyboardEvent) => void>(undefined)
  useEffect(() => {
    onKey.current = (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return
      const target = e.target instanceof HTMLElement ? e.target : null
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return
      if (formIsOpen || rawSite || document.querySelector("[role='dialog'], [role='menu']")) return
      if (e.key === "/") {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (e.key === "n" && admin && hasNginx) {
        e.preventDefault()
        openForm(null)
      } else if (e.key === "j" || e.key === "k") {
        e.preventDefault()
        setCursor(stepSelection(visible.map(siteKey), cursor, e.key === "j" ? 1 : -1))
      } else if (e.key === "Enter" && cursor && (!target || target === document.body)) {
        const site = visible.find((v) => siteKey(v) === cursor)
        if (site) {
          e.preventDefault()
          openSite(site)
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
        <LoadingPanel />
      </Page>
    )
  }
  if (error && !data) {
    return (
      <Page>
        {header}
        <ErrorState error={error} />
      </Page>
    )
  }

  const newSite = admin && hasNginx && (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        onClick={() => downloadFrom("/proxy/sites-export")}
        title="Every site's file in a tar.gz, with a manifest. Password files are never in it."
      >
        <Download className="size-4" />
        Export all
      </Button>
      <Button size="sm" variant="outline" onClick={() => setImporting(true)}>
        <CloudUpload className="size-4" />
        Import
      </Button>
      <Button size="sm" onClick={() => openForm(null)}>
        <Plus className="size-4" />
        New site
      </Button>
    </div>
  )

  // Grouped only where the grouping says something a chip has not: once a
  // filter is on, its name *is* the group.
  // Nor once another order is chosen: the groups are the urgency order.
  const narrowed = filter.trim().length > 0 || chip !== "all" || sort !== "urgency"
  const needsMe = (v: VHost) => waiting(v) || isUnloaded(v) || Boolean(notLive(v))
  const attention = visible.filter(needsMe)
  const active = visible.filter((v) => !needsMe(v) && v.enabled)
  // Off, and nothing to decide about — the distribution's untouched default
  // site, a conf.d file nginx does not read. Drawn under "Serving", the
  // stock default read as one of the sites that serve.
  const idle = visible.filter((v) => !needsMe(v) && !v.enabled)
  const sorted: { key: string; label: string; sites: VHost[] }[] = [
    { key: "waiting", label: "Needs attention", sites: attention },
    { key: "serving", label: active.some(engineStopped) ? "Enabled" : "Serving", sites: active },
    { key: "idle", label: "Not in use", sites: idle },
  ].filter((group) => group.sites.length > 0)
  const groups =
    narrowed || sorted.length < 2 ? [{ key: "all", label: "", sites: visible }] : sorted
  const allServing = run?.running !== false && !hosts.some((v) => isUnloaded(v) || notLive(v))
  const disabledServed = hosts.filter((v) => isDisabled(v) && notLive(v)).length

  return (
    <Page className="animate-rise">
      {header}

      {/* The rows stay on a failed read, and would pass for what nginx
          serves now: the tiles and cards below are from the last good one. */}
      {error && (
        <div role="alert">
          <Notice tone="warning" icon={Warning} title="Could not read the sites again">
            <p className="break-words">{errorMessage(error)}</p>
            <p>The sites below are from the last read, and may be out of date.</p>
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

      <StatGrid columns={4} dense>
        <StatTile
          label="Sites"
          value={counts.all}
          hint={
            counts.all === 0 ? (
              "none configured"
            ) : (
              <span className="inline-flex items-center gap-1.5">
                {counts.nginx > 0 && (
                  <span className="inline-flex items-center gap-1">
                    <ProductGlyph id="nginx-static" className="size-3" />
                    {counts.nginx} nginx
                  </span>
                )}
                {counts.caddy > 0 && (
                  <span className="inline-flex items-center gap-1">
                    <ProductGlyph id="caddy" className="size-3" />
                    {counts.caddy} Caddy
                  </span>
                )}
              </span>
            )
          }
        />
        <StatTile
          label="On TLS"
          value={counts.tls}
          hint={counts.all > 0 ? `of ${counts.all}` : "—"}
          tone={counts.all > 0 && counts.tls === counts.all ? "success" : "default"}
        />
        <StatTile
          label="Plain HTTP"
          value={counts.plain}
          tone={counts.plain > 0 ? "warning" : "default"}
          hint={counts.plain > 0 ? "proxying an app unencrypted" : "every app on TLS"}
        />
        <StatTile
          label="Disabled"
          value={counts.disabled}
          hint={
            counts.disabled > 0
              ? disabledHint(disabledServed)
              : counts.broken > 0
                ? `none, but ${plural(counts.broken, "broken link")}`
                : allServing
                  ? "every site serving"
                  : "every site enabled"
          }
        />
      </StatGrid>

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
        <div className="flex min-w-0 flex-col gap-4">
          {/* The filters stand on the page rather than inside a panel
              header: the list is the whole page, and the command to add to
              it sits with the filters that narrow it. */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-section font-semibold">Routing</h2>
            {newSite}
          </div>
          <Toolbar className="justify-between gap-x-4">
            <SearchInput
              ref={searchRef}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Name, domain, upstream, port, file or path"
              aria-label="Search sites"
              aria-keyshortcuts="/"
            />
            <div className="flex flex-wrap items-center gap-2">
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
              {/* The table needs the width; below it the cards are drawn either way. */}
              <ChipStrip aria-label="View" className="max-lg:hidden">
                <FilterChip selected={view === "cards"} onClick={() => setView("cards")}>
                  Cards
                </FilterChip>
                <FilterChip selected={view === "table"} onClick={() => setView("table")}>
                  Table
                </FilterChip>
              </ChipStrip>
            </div>
            <ChipStrip className="basis-full">
              {(Object.keys(CHIP_LABEL) as SiteChip[])
                .filter((key) => key === "all" || key === chip || chipCounts[key] > 0)
                .map((key) => (
                  <FilterChip key={key} selected={chip === key} onClick={() => setChip(key)}>
                    {CHIP_LABEL[key]} <ChipCount>{chipCounts[key]}</ChipCount>
                  </FilterChip>
                ))}
            </ChipStrip>
          </Toolbar>

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
            <EmptyState icon={Globe} title="No sites match" />
          ) : (
            <>
              {view === "table" && (
                <div className="hidden min-w-0 lg:block">
                  <Table
                    className="table-fixed"
                    containerClassName="rounded-xl border bg-card"
                    aria-label="Sites"
                  >
                    <TableHeader className={stickyTableHeader}>
                      <TableRow>
                        <TableHead className="w-10">
                          <Checkbox
                            aria-label="Select every site shown"
                            disabled={allChosen.length === 0}
                            checked={
                              chosen.length > 0 && chosen.length === allChosen.length
                                ? true
                                : chosen.length > 0
                                  ? "indeterminate"
                                  : false
                            }
                            onCheckedChange={(on) =>
                              setSelected(on === true ? allChosen.map((v) => v.name) : [])
                            }
                          />
                        </TableHead>
                        <TableHead className="w-[22%]">Site</TableHead>
                        <TableHead>Route</TableHead>
                        <TableHead className="w-44">TLS</TableHead>
                        <TableHead className="w-44">State</TableHead>
                        <TableHead className="w-28">Edited</TableHead>
                        <TableHead className="w-40">
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visible.map((vhost) => (
                        <SiteTableRow
                          key={siteKey(vhost)}
                          index={0}
                          vhost={vhost}
                          {...stateOf(vhost.name)}
                          stopped={engineStopped(vhost)}
                          unloaded={isUnloaded(vhost)}
                          notLive={notLive(vhost)}
                          ambiguous={shared.has(vhost.name)}
                          cert={siteCert(vhost, certPoll.data)}
                          health={siteUpstreams(vhost, upstreamPoll.data)}
                          requests={siteRequests(vhost, trafficPoll.data)}
                          selectable={allChosen.includes(vhost)}
                          selected={selected.includes(vhost.name)}
                          onSelectedChange={(on) => toggleSelected(vhost, on)}
                          cursor={cursor === siteKey(vhost)}
                          {...handlers}
                        />
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
              <div className={cn("flex min-w-0 flex-col gap-4", view === "table" && "lg:hidden")}>
                {groups.map((group) => (
                  <div key={group.key} className="flex min-w-0 flex-col gap-2">
                    {group.label && <GroupRule label={group.label} count={group.sites.length} />}
                    <ProxyGrid
                      aria-label={group.label || "Sites"}
                      className={group.sites.length === 1 ? "xl:grid-cols-1" : undefined}
                    >
                      {group.sites.map((vhost, index) => (
                        <SiteCard
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
                          requests={siteRequests(vhost, trafficPoll.data)}
                          selectable={allChosen.includes(vhost)}
                          selected={selected.includes(vhost.name)}
                          onSelectedChange={(on) => toggleSelected(vhost, on)}
                          cursor={cursor === siteKey(vhost)}
                          {...handlers}
                        />
                      ))}
                    </ProxyGrid>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {hasNginx && <DefaultSitePanel admin={admin} />}

      {admin && hasNginx && <AuthFilesPanel />}

      {admin && hasNginx && (
        <SiteBackupsPanel
          backups={backupsPoll}
          onRestored={(res) => placed(res, `${res.name} restored`)}
        />
      )}

      <SiteForm
        open={formIsOpen}
        editing={formEditing}
        copyFrom={form.open ? form.copyFrom : null}
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
                openForm(v.name)
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
  /** Requests in the last hour, when the traffic summary has the site. */
  requests?: number
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
  onDelete: (v: VHost) => void
  onRename: (v: VHost) => void
  onUnlink: (v: VHost) => void
  onOverride: (v: VHost) => void
}

/**
 * The route owns the body; service state and commands each have their own
 * line. The card opens the form for an administrator and a file the form
 * saves back; everything else with a file opens that file, read-only unless
 * the reader may write it — and a deployment's route read-only for everyone,
 * since its deployment writes it. A link to nothing has nothing to open, and
 * a file outside the proxy's directories is one the editor refuses.
 */
function SiteCard({
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
  requests,
  selectable,
  selected,
  onSelectedChange,
  cursor,
  ...handlers
}: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ambiguous, ...handlers })
  const form = handlers.admin && vhost.formEditable && !activeOwner(vhost)
  const primary = () => (form ? handlers.onEdit(vhost) : handlers.onRaw(vhost))
  const canOpen = form || opensFile(vhost)
  // A link to nothing has no domain or upstream to draw, only where it points.
  const linkOnly = vhost.broken === "dangling" && !vhost.path
  return (
    <ChoiceRow
      actions={
        selectable && (
          <Checkbox
            aria-label={`Select ${vhost.name}`}
            checked={selected}
            onCheckedChange={(on) => onSelectedChange(on === true)}
          />
        )
      }
      verb={canOpen ? `Open ${vhost.name}` : vhost.name}
      onSelect={canOpen ? primary : undefined}
      disabled={!canOpen}
      index={index}
      busy={Boolean(busy)}
      className={cn("h-full gap-4 p-4", cursor && "ring-2 ring-ring")}
      leading={<ProductLogo id={siteProduct(vhost)} size="md" />}
      title={<span className="text-title">{vhost.name}</span>}
      description={siteKind(vhost)}
      trailing={
        <ServingStatus
          vhost={vhost}
          busy={busy}
          unread={unread}
          stopped={stopped}
          unloaded={unloaded}
          notLive={notLive}
        />
      }
    >
      {cursor && <ScrollHere />}
      {!linkOnly && (
        <RoutePath
          source={vhost.serverNames.join(", ") || "Default host"}
          destination={upstreamTargets(vhost).join(", ") || "Served by configuration"}
        />
      )}
      <SiteNotes vhost={vhost} />
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
        {!linkOnly && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 text-hint text-muted-foreground">
            <SiteTLS vhost={vhost} />
            {cert && <ExpiryStatus cert={cert} />}
            <UpstreamHealth targets={health} />
            <span className="font-mono">{vhost.listen.join(" · ") || "No listener reported"}</span>
            {requests !== undefined && <span>{compact.format(requests)} req/h</span>}
            <Edited vhost={vhost} />
            <SiteFeatures vhost={vhost} />
          </div>
        )}
        <VerbBar verbs={verbs} menuLabel={`More actions for ${vhost.name}`} className="ml-auto" />
      </div>
    </ChoiceRow>
  )
}

/** When the site's file last changed, where the listing could read it. */
function Edited({ vhost }: { vhost: VHost }) {
  // A link to nothing reports the link's own time, and a zero time none.
  if (!vhost.path || vhost.modified.startsWith("0001")) return null
  return <span title={vhost.modified}>edited {relativeTime(vhost.modified)}</span>
}

/** Brings the site j or k moved to into view as it becomes the one. */
function ScrollHere() {
  return <span aria-hidden ref={(el) => el?.scrollIntoView({ block: "nearest" })} />
}

/** The table view's row: the card's readings in columns, and the same verbs. */
function SiteTableRow({
  vhost,
  busy,
  unread,
  stopped,
  unloaded,
  notLive,
  ambiguous,
  cert,
  health,
  requests,
  selectable,
  selected,
  onSelectedChange,
  cursor,
  ...handlers
}: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ambiguous, ...handlers })
  const form = handlers.admin && vhost.formEditable && !activeOwner(vhost)
  const canOpen = form || opensFile(vhost)
  const linkOnly = vhost.broken === "dangling" && !vhost.path
  return (
    <TableRow data-state={selected ? "selected" : undefined} className="group">
      <TableCell>
        {selectable && (
          <Checkbox
            aria-label={`Select ${vhost.name}`}
            checked={selected}
            onCheckedChange={(on) => onSelectedChange(on === true)}
          />
        )}
      </TableCell>
      <TableCell className={cn(cursor && "outline-2 -outline-offset-2 outline-ring")}>
        {cursor && <ScrollHere />}
        <div className="flex min-w-0 items-center gap-2.5">
          <ProductLogo id={siteProduct(vhost)} size="sm" />
          <div className="min-w-0">
            {canOpen ? (
              <button
                type="button"
                aria-label={`Open ${vhost.name}`}
                onClick={() => (form ? handlers.onEdit(vhost) : handlers.onRaw(vhost))}
                className="block max-w-full truncate rounded-sm text-body font-medium focus-ring"
              >
                {vhost.name}
              </button>
            ) : (
              <span className="block truncate text-body font-medium">{vhost.name}</span>
            )}
            <span className="block truncate text-hint text-muted-foreground">
              {siteKind(vhost)}
            </span>
          </div>
        </div>
      </TableCell>
      <TableCell>
        {!linkOnly && (
          <div className="min-w-0 text-hint">
            <p className="truncate" title={vhost.serverNames.join(", ")}>
              {vhost.serverNames.join(", ") || "Default host"}
            </p>
            <p className="truncate font-mono text-muted-foreground">
              → {upstreamTargets(vhost).join(", ") || "Served by configuration"}
            </p>
          </div>
        )}
      </TableCell>
      <TableCell>
        <div className="flex flex-col items-start gap-1 text-hint">
          <SiteTLS vhost={vhost} />
          {cert && <ExpiryStatus cert={cert} />}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex flex-col items-start gap-1 text-hint">
          <ServingStatus
            vhost={vhost}
            busy={busy}
            unread={unread}
            stopped={stopped}
            unloaded={unloaded}
            notLive={notLive}
          />
          <UpstreamHealth targets={health} />
        </div>
      </TableCell>
      <TableCell className="text-hint text-muted-foreground">
        <Edited vhost={vhost} />
        {requests !== undefined && <p>{compact.format(requests)} req/h</p>}
      </TableCell>
      <TableCell>
        <VerbBar verbs={verbs} menuLabel={`More actions for ${vhost.name}`} />
      </TableCell>
    </TableRow>
  )
}

/**
 * The site's own verbs in the raw editor's header: the ones that still make
 * sense with the file open. Edit waits while a save is in flight.
 */
function SiteFileVerbs({
  vhost,
  admin,
  busy,
  onEdit,
}: {
  vhost: VHost
  admin: boolean
  busy: boolean
  onEdit: (vhost: VHost) => void
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
  }).filter((v) => ["open", "scan", "log", "errors", "edit", "deployment"].includes(v.key))
  return <VerbBar verbs={verbs.map((v) => ({ ...v, inline: true }))} className="ml-auto" />
}
