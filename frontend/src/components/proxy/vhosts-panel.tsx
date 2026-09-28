"use client"

import { useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Globe, Plus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { bytes, plural } from "@/lib/format"
import { del, get, post } from "@/lib/api"
import type { SiteCacheUsage, SiteResult, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceRow, GroupRule } from "@/components/flow"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ProductGlyph, ProductLogo, ProductLogos } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useNewSiteLink } from "@/components/proxy/site-link"
import { siteProduct } from "@/components/proxy/marks"
import { SiteForm } from "@/components/proxy/site-form"
import { ServingStatus, SiteRateLimited, SiteTLS } from "@/components/proxy/site-marks"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { ProxyGrid, RoutePath } from "@/components/proxy/route-path"
import { byUrgency, isDisabled, isPlain, waiting } from "@/components/proxy/site-order"
import { Button } from "@/components/ui/button"

type SiteFilter = "all" | "tls" | "plain" | "disabled"

const FILTER_LABEL: Record<SiteFilter, string> = {
  all: "All",
  tls: "TLS",
  plain: "Plain HTTP",
  disabled: "Disabled",
}

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
  const [editing, setEditing] = useState<VHost | null>(null)
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
  const [pending, setPending] = useState<Record<string, string>>({})
  // In the URL so a deployment finding can link straight at the site serving
  // its hostname, and so the browser's back button restores the selection.
  const [requested, setRequested] = useQuerySelection("site")
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<VHost[]>("/proxy/vhosts", undefined, signal),
    30_000,
  )

  const openForm = (name: string | null, copyFrom: string | null = null) => {
    setRequested(null)
    setForm((f) => ({ open: true, editing: name, copyFrom, session: f.session + 1 }))
  }
  useNewSiteLink(() => openForm(null))

  // A ?site= link from elsewhere in the dashboard opens that site as soon as
  // its row loads. The open panel is derived from the URL rather than copied
  // into state, so the back button closes it and a reload reopens it.
  const linked = requested ? data?.find((vhost) => vhost.name === requested) : undefined
  const rawEditing = editing ?? (linked && linked.kind !== "nginx" && linked.path ? linked : null)
  const formIsOpen = form.open || linked?.kind === "nginx"
  const formEditing = form.open ? form.editing : (linked?.name ?? null)

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
  const counts = useMemo(
    () => ({
      all: hosts.length,
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
        if (chip === "tls" && !v.tls) return false
        if (chip === "plain" && !isPlain(v)) return false
        if (chip === "disabled" && !isDisabled(v)) return false
        if (!needle) return true
        return (
          v.name.toLowerCase().includes(needle) ||
          v.serverNames.some((n) => n.toLowerCase().includes(needle)) ||
          v.upstreams.some((u) => u.toLowerCase().includes(needle))
        )
      })
      .sort(byUrgency)
  }, [hosts, filter, chip])

  const setBusy = (name: string, verb: string | null) =>
    setPending((p) => {
      const next = { ...p }
      if (verb) next[name] = verb
      else delete next[name]
      return next
    })

  const toggle = (vhost: VHost, enabled: boolean) => {
    const body = { enabled, reload: true }
    const apply = async (c?: string) => {
      setBusy(vhost.name, enabled ? "Enabling" : "Disabling")
      try {
        await post(`/proxy/vhosts/${encodeURIComponent(vhost.name)}/enabled`, body, {
          confirm: c,
        })
        if (enabled) notify.success(`${vhost.name} enabled`)
        refresh()
      } finally {
        setBusy(vhost.name, null)
      }
    }
    if (!enabled) {
      confirm({
        title: "Disable site",
        confirmLabel: "Disable and reload",
        description: (
          <p>
            <b>{vhost.name}</b> stops serving as soon as nginx reloads. The config file stays on
            disk.
          </p>
        ),
        action: apply,
      })
      return
    }
    apply().catch((err) => notify.error("Could not enable", err))
  }

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
        try {
          await del(`/proxy/sites/${encodeURIComponent(vhost.name)}`)
          refresh()
        } finally {
          setBusy(vhost.name, null)
        }
      },
    })

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
          setBusy(vhost.name, null)
        }
      },
    })
  }

  const handlers = {
    admin,
    onEdit: (v: VHost) => openForm(v.name),
    onRaw: (v: VHost) => setEditing(v),
    onDuplicate: (v: VHost) => openForm(null, v.name),
    onToggle: toggle,
    onMaintenance: maintenance,
    onPurgeCache: purgeCache,
    onDelete: remove,
  }

  const header = <PageContext eyebrow="Proxy" title="Sites" />

  if (loading && !data) {
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
  const attention = visible.filter(waiting)
  const settled = visible.filter((v) => !waiting(v))
  const groups: { key: string; label: string; sites: VHost[] }[] =
    narrowed || attention.length === 0 || settled.length === 0
      ? [{ key: "all", label: "", sites: visible }]
      : [
          { key: "waiting", label: "Needs attention", sites: attention },
          { key: "serving", label: "Serving", sites: settled },
        ]

  return (
    <Page className="animate-rise">
      {header}

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
          hint={counts.disabled > 0 ? "on disk, not serving" : "every site serving"}
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
                      key={`${vhost.kind}:${vhost.name}`}
                      vhost={vhost}
                      busy={pending[vhost.name]}
                      index={index}
                      {...handlers}
                    />
                  ))}
                </ProxyGrid>
              </div>
            ))
          )}
        </div>
      )}

      {admin && hasNginx && <AuthFilesPanel />}

      <SiteForm
        open={formIsOpen}
        editing={formEditing}
        copyFrom={form.open ? form.copyFrom : null}
        session={form.session}
        onOpenChange={closeForm}
        onSaved={refresh}
      />
      <ConfigEditor
        open={rawEditing !== null}
        onOpenChange={closeRaw}
        path={rawEditing?.path ?? ""}
        kind={rawEditing?.kind ?? "nginx"}
        title={rawEditing?.name ?? "Configuration"}
        readOnly={!admin}
        siteDisabled={
          rawEditing?.kind === "nginx" && Boolean(rawEditing.enabledPath) && !rawEditing.enabled
        }
        onSaved={refresh}
        actions={(busy) =>
          rawEditing && (
            <SiteFileVerbs
              vhost={rawEditing}
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
      {dialog}
    </Page>
  )
}

type CardProps = {
  vhost: VHost
  busy?: string
  index: number
  admin: boolean
  onEdit: (v: VHost) => void
  onRaw: (v: VHost) => void
  onDuplicate: (v: VHost) => void
  onToggle: (v: VHost, enabled: boolean) => void
  onMaintenance: (v: VHost, on: boolean) => void
  onPurgeCache: (v: VHost) => void
  onDelete: (v: VHost) => void
}

/** The route owns the body; service state and commands each have their own line. */
function SiteCard({ vhost, busy, index, ...handlers }: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ...handlers })
  const primary = () => (vhost.kind === "nginx" ? handlers.onEdit(vhost) : handlers.onRaw(vhost))
  const canOpen = vhost.kind === "nginx" ? handlers.admin : Boolean(vhost.path)
  return (
    <ChoiceRow
      verb={canOpen ? `Open ${vhost.name}` : vhost.name}
      onSelect={canOpen ? primary : undefined}
      disabled={!canOpen}
      index={index}
      busy={Boolean(busy)}
      className="h-full gap-4 p-4"
      leading={<ProductLogo id={siteProduct(vhost)} size="md" />}
      title={<span className="text-title">{vhost.name}</span>}
      description={
        vhost.kind === "nginx" ? "nginx site" : vhost.path ? "Caddyfile" : "Docker Caddy ingress"
      }
      trailing={<ServingStatus vhost={vhost} busy={busy} />}
    >
      <RoutePath
        source={vhost.serverNames.join(", ") || "Default host"}
        destination={vhost.upstreams.join(", ") || "Served by configuration"}
      />
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 text-hint text-muted-foreground">
          <SiteTLS vhost={vhost} />
          <SiteRateLimited vhost={vhost} />
          <span className="font-mono">{vhost.listen.join(" · ") || "No listener reported"}</span>
        </div>
        <VerbBar verbs={verbs} menuLabel={`More actions for ${vhost.name}`} />
      </div>
    </ChoiceRow>
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
    onDuplicate: noop,
    onToggle: noop,
    onDelete: noop,
  }).filter((v) => v.key === "open" || v.key === "scan" || v.key === "log" || v.key === "edit")
  return <VerbBar verbs={verbs.map((v) => ({ ...v, inline: true }))} className="ml-auto" />
}
