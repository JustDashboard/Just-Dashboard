"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Globe, Plus, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { ApiError, del, errorMessage, get, post } from "@/lib/api"
import type {
  ProxyPending,
  SiteDeleteResult,
  SiteResult,
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
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { AccessListsPanel } from "@/components/proxy/access-lists-panel"
import { RouteResolver } from "@/components/proxy/route-resolver"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useNewSiteLink } from "@/components/proxy/site-link"
import { DefaultSitePanel } from "@/components/proxy/default-site"
import { siteProduct } from "@/components/proxy/marks"
import { SiteForm } from "@/components/proxy/site-form"
import {
  ServingStatus,
  SiteFeatures,
  SiteNotes,
  SiteTLS,
  siteKind,
} from "@/components/proxy/site-marks"
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
import { opensFile, sitePath, useSiteVerbs } from "@/components/proxy/site-verbs"
import { ProxyGrid, RoutePath } from "@/components/proxy/route-path"
import {
  byUrgency,
  isBroken,
  isDisabled,
  isPlain,
  sharedNames,
  waiting,
} from "@/components/proxy/site-order"
import { Button } from "@/components/ui/button"

type SiteFilter = "all" | "broken" | "tls" | "plain" | "disabled"

const FILTER_LABEL: Record<SiteFilter, string> = {
  all: "All",
  broken: "Broken links",
  tls: "TLS",
  plain: "Plain HTTP",
  disabled: "Disabled",
}

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
 * The cards retain the worst-first groups, and each opens the site's own
 * page — what it is, and its requests and errors read there — for every site
 * and every reader. Editing stays with its owner, as the card's verbs: nginx
 * opens the builder, file-backed Caddy opens its file, and Docker Caddy has
 * no editor because there is no host file to save.
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
  const [chip, setChip] = useSessionState<SiteFilter>("proxy.sites.chip", "all")
  const [pending, setPending] = useState<Record<string, Busy>>({})
  const [output, setOutput] = useState<NginxOutput | null>(null)
  // In the URL so a deployment finding can link straight at the site serving
  // its hostname, and so the browser's back button restores the selection.
  const [requested, setRequested] = useQuerySelection("site")
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<VHost[]>("/proxy/vhosts", undefined, signal),
    30_000,
  )
  // What on disk nginx has not loaded. `nginx -s reload` answers before
  // nginx has loaded anything, so a read after a reload the page asked for
  // names the load it saw before, and the backend waits for a newer one.
  // `warn` is a reload a verb knows it sent: nginx still on the load from
  // before it refused what it read, which the verb's toast could not know.
  const reloadFrom = useRef<{ after: string; warn: boolean } | undefined>(undefined)
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
  useNewSiteLink(() => openForm(null))

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
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return hosts
      .filter((v) => {
        if (chip === "broken" && !isBroken(v)) return false
        if (chip === "tls" && !v.tls) return false
        if (chip === "plain" && !isPlain(v)) return false
        if (chip === "disabled" && !isDisabled(v)) return false
        return matchesSearch(v, needle)
      })
      .sort(byUrgency)
  }, [hosts, filter, chip])

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
  // A save of the site as the form reads it with the switch changed, which
  // the server refuses for a file the form would not write back whole.
  const maintenance = (vhost: VHost, on: boolean) => {
    const apply = async (c?: string) => {
      setBusy(vhost.name, on ? "Starting maintenance" : "Ending maintenance")
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
        refresh()
      } finally {
        setBusy(vhost.name, null)
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
    onMaintenance: maintenance,
    onDelete: remove,
    onUnlink: unlink,
    onOverride: override,
  }

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
    <Button size="sm" onClick={() => openForm(null)}>
      <Plus className="size-4" />
      New site
    </Button>
  )

  // Grouped only where the grouping says something a chip has not: once a
  // filter is on, its name *is* the group.
  const narrowed = filter.trim().length > 0 || chip !== "all"
  // A change nginx did not load waits for a reload; with the engine
  // stopped, the rest are enabled rather than serving, and nothing is live.
  const notLive = (v: VHost) =>
    run?.running === false ? undefined : notLiveLabel(v, siteChanges(pendingPoll.data, v))
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
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Site, domain or upstream"
            />
            <ChipStrip>
              {(Object.keys(FILTER_LABEL) as SiteFilter[])
                .filter((key) => key === "all" || counts[key] > 0)
                .map((key) => (
                  <FilterChip key={key} selected={chip === key} onClick={() => setChip(key)}>
                    {FILTER_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
                  </FilterChip>
                ))}
            </ChipStrip>
          </Toolbar>

          {visible.length === 0 ? (
            <EmptyState icon={Globe} title="No sites match" />
          ) : (
            groups.map((group) => (
              <div key={group.key} className="flex min-w-0 flex-col gap-2">
                {group.label && <GroupRule label={group.label} count={group.sites.length} />}
                <ProxyGrid
                  aria-label={group.label || "Sites"}
                  className={group.sites.length === 1 ? "xl:grid-cols-1" : undefined}
                >
                  {group.sites.map((vhost, index) => (
                    <SiteCard
                      key={`${vhost.kind}:${vhost.layout ?? ""}:${vhost.name}`}
                      vhost={vhost}
                      {...stateOf(vhost.name)}
                      stopped={engineStopped(vhost)}
                      unloaded={isUnloaded(vhost)}
                      notLive={notLive(vhost)}
                      index={index}
                      ambiguous={shared.has(vhost.name)}
                      {...handlers}
                    />
                  ))}
                </ProxyGrid>
              </div>
            ))
          )}
        </div>
      )}

      {hasNginx && <DefaultSitePanel admin={admin} />}

      {admin && hasNginx && <AuthFilesPanel />}
      {admin && hasNginx && <AccessListsPanel />}
      {admin && hasNginx && <RouteResolver />}

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
  admin: boolean
  onEdit: (v: VHost) => void
  onRaw: (v: VHost) => void
  onServed: (v: VHost) => void
  onDuplicate: (v: VHost) => void
  onToggle: (v: VHost, enabled: boolean) => void
  onMaintenance: (v: VHost, on: boolean) => void
  onDelete: (v: VHost) => void
  onUnlink: (v: VHost) => void
  onOverride: (v: VHost) => void
}

/**
 * The route owns the body; service state and commands each have their own
 * line. Opening it is the site's page, for every site and every reader: a
 * Docker Caddy route with no file to edit still has requests to read, and a
 * link to nothing is explained there. Editing stays with its owner, as the
 * card's verbs: the form for an administrator and a file the form saves back,
 * the file itself for everything else with one, read-only unless the reader
 * may write it — and a deployment's route read-only for everyone.
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
  ...handlers
}: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ambiguous, ...handlers })
  // A link to nothing has no domain or upstream to draw, only where it points.
  const linkOnly = vhost.broken === "dangling" && !vhost.path
  return (
    <ChoiceRow
      verb={`Open ${vhost.name}`}
      href={sitePath(vhost.name)}
      index={index}
      busy={Boolean(busy)}
      className="h-full gap-4 p-4"
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
            <span className="font-mono">{vhost.listen.join(" · ") || "No listener reported"}</span>
            <SiteFeatures vhost={vhost} />
          </div>
        )}
        <VerbBar verbs={verbs} menuLabel={`More actions for ${vhost.name}`} className="ml-auto" />
      </div>
    </ChoiceRow>
  )
}

/** The site's verbs the file's sheet carries in its header. */
const EDITOR_VERBS = ["open", "scan", "log", "errors", "edit", "deployment"]

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
    onUnlink: noop,
    onOverride: noop,
  }).filter((v) => keys.includes(v.key))
  return <VerbBar verbs={verbs.map((v) => ({ ...v, inline: true }))} className="ml-auto" />
}
