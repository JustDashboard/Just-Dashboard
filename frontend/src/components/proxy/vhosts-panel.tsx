"use client"

import { useEffect, useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Globe, Plus, ShieldCheck, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ProxyValidation, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Pane, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo, ProductLogos } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { VerbActions, VerbBar } from "@/components/verbs"
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { siteProduct } from "@/components/proxy/marks"
import { SiteForm } from "@/components/proxy/site-form"
import { ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { Button } from "@/components/ui/button"

type SiteFilter = "all" | "tls" | "plain" | "disabled"

const FILTER_LABEL: Record<SiteFilter, string> = {
  all: "All",
  tls: "TLS",
  plain: "Plain HTTP",
  disabled: "Disabled",
}

/** A site with something to act on: proxying an application in plain text, or on disk and not serving. */
function isPlain(v: VHost) {
  return v.enabled && !v.tls && v.upstreams.length > 0
}
function isDisabled(v: VHost) {
  return v.kind === "nginx" && !v.enabled && Boolean(v.enabledPath)
}
function waiting(v: VHost) {
  return isPlain(v) || isDisabled(v)
}

/** Worst first, then by name. The order *is* the page's answer to "which of these needs me". */
function byUrgency(a: VHost, b: VHost): number {
  const rank = (v: VHost) => (isPlain(v) ? 0 : isDisabled(v) ? 1 : 2)
  if (rank(a) !== rank(b)) return rank(a) - rank(b)
  return a.name.localeCompare(b.name)
}

/**
 * Every site this host serves, nginx and Caddy alike: four readings, the
 * filters on the page, and the sites as cards ordered worst first.
 *
 * The cards replaced a six-column table on a wide screen and a hairlined
 * list on a phone, for the reason the Docker containers took the same pass:
 * every row here opens the site — its form, or its file — so it is a choice
 * and carries the lit edge §16 gives to things you take, and the table's
 * header was naming six columns that each card now says for itself. Each is
 * drawn as the engine serving it (nginx or Caddy), with its domains and
 * where they go on the second line, and its two states held out on the
 * right — on TLS by whose certificate, and serving — so a column of cards is
 * still scanned down the way the table was.
 *
 * A site is editable through the form that writes its config, or as raw
 * text for the ones the form does not own — a Caddyfile, a hand-written
 * nginx file. A Docker Caddy route has no file on the host at all, and used
 * to be offered an editor that could only fail; it is listed, and its verbs
 * are the ones that can work.
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
  // One shape per width, chosen once, so a reading is in the document once.
  const wide = useMediaQuery("(min-width: 1024px)")

  const openForm = (name: string | null, copyFrom: string | null = null) => {
    setRequested(null)
    setForm((f) => ({ open: true, editing: name, copyFrom, session: f.session + 1 }))
  }

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

  const handlers = {
    admin,
    onEdit: (v: VHost) => openForm(v.name),
    onRaw: (v: VHost) => setEditing(v),
    onDuplicate: (v: VHost) => openForm(null, v.name),
    onToggle: toggle,
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

      <StatGrid columns={4}>
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
            {newSite && <span className="ml-auto">{newSite}</span>}
          </Toolbar>

          {visible.length === 0 ? (
            <EmptyState icon={Globe} title="No sites match" />
          ) : (
            groups.map((group) => (
              <div key={group.key} className="flex min-w-0 flex-col gap-2">
                {group.label && <GroupRule label={group.label} count={group.sites.length} />}
                <ChoiceList aria-label={group.label || "Sites"} className="animate-rise">
                  {group.sites.map((vhost, index) => (
                    <SiteCard
                      key={`${vhost.kind}:${vhost.name}`}
                      vhost={vhost}
                      busy={pending[vhost.name]}
                      wide={wide}
                      index={index}
                      {...handlers}
                    />
                  ))}
                </ChoiceList>
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
        vhost={rawEditing}
        admin={admin}
        confirm={confirm}
        onOpenChange={closeRaw}
        onSaved={refresh}
        onEdit={(v) => {
          setEditing(null)
          openForm(v.name)
        }}
      />
      {dialog}
    </Page>
  )
}

type CardProps = {
  vhost: VHost
  busy?: string
  wide: boolean
  index: number
  admin: boolean
  onEdit: (v: VHost) => void
  onRaw: (v: VHost) => void
  onDuplicate: (v: VHost) => void
  onToggle: (v: VHost, enabled: boolean) => void
  onDelete: (v: VHost) => void
}

/**
 * One site, as a card you open. The engine's mark, the name with its state's
 * dot, the domains and where they go; TLS and serving held out at the right
 * on a wide screen and under the name on a phone, and the verbs beside them.
 *
 * Opening it is the form for an nginx site and the file for anything else
 * with a file; a Docker Caddy route has neither, keeps its card and its
 * verbs, and loses the arrow — a card that looks pressable and does nothing
 * is the defect `ChoiceRow`'s `disabled` exists to prevent.
 */
function SiteCard({ vhost, busy, wide, index, ...handlers }: CardProps) {
  const verbs = useSiteVerbs({ vhost, busy, ...handlers })
  const primary = () => (vhost.kind === "nginx" ? handlers.onEdit(vhost) : handlers.onRaw(vhost))
  const canOpen = vhost.kind === "nginx" ? handlers.admin : Boolean(vhost.path)
  const route = [vhost.serverNames.join(", "), vhost.upstreams[0]].filter(Boolean).join(" → ")
  const states = (
    <>
      <span className={cn(wide && "w-24")}>
        <SiteTLS vhost={vhost} />
      </span>
      <span className={cn(wide && "w-28")}>
        <ServingStatus vhost={vhost} busy={busy} />
      </span>
    </>
  )
  return (
    <ChoiceRow
      verb={canOpen ? `Open ${vhost.name}` : vhost.name}
      onSelect={canOpen ? primary : undefined}
      disabled={!canOpen}
      index={index}
      className={cn(busy && "opacity-70")}
      leading={<ProductLogo id={siteProduct(vhost)} size="sm" />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <StatusDot state={vhost.enabled ? "running" : "stopped"} />
          <span className="truncate">{vhost.name}</span>
        </span>
      }
      description={
        <span className="font-mono">
          {route || vhost.path || `${vhost.kind} route`}
          {vhost.upstreams.length > 1 && (
            <span className="numeric text-muted-foreground/60"> +{vhost.upstreams.length - 1}</span>
          )}
        </span>
      }
      trailing={wide ? states : undefined}
      actions={<VerbActions dim verbs={verbs} menuLabel={`More actions for ${vhost.name}`} />}
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 pl-11">
          {states}
          {vhost.path && (
            <span className="truncate font-mono text-hint text-muted-foreground">{vhost.path}</span>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * The file itself, for a site the form does not own — and for the operator
 * who would rather see the nginx than the form. Keyed on the file so opening
 * another vhost never inherits the previous one's buffer; saving that to the
 * wrong path would be a real outage.
 */
function ConfigEditor({
  vhost,
  admin,
  confirm,
  onOpenChange,
  onSaved,
  onEdit,
}: {
  vhost: VHost | null
  admin: boolean
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onSaved: () => void
  onEdit: (vhost: VHost) => void
}) {
  return (
    <ConfigEditorBody
      key={vhost?.path ?? "none"}
      vhost={vhost}
      admin={admin}
      confirm={confirm}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
      onEdit={onEdit}
    />
  )
}

function ConfigEditorBody({
  vhost,
  admin,
  confirm,
  onOpenChange,
  onSaved,
  onEdit,
}: {
  vhost: VHost | null
  admin: boolean
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onSaved: () => void
  onEdit: (vhost: VHost) => void
}) {
  const [content, setContent] = useState("")
  const [original, setOriginal] = useState("")
  const [busy, setBusy] = useState(false)
  const [validation, setValidation] = useState<ProxyValidation | null>(null)
  const noop = () => {}
  const verbs = useSiteVerbs({
    vhost: vhost ?? EMPTY_VHOST,
    admin,
    busy: busy ? "Saving" : undefined,
    onEdit,
    onRaw: noop,
    onDuplicate: noop,
    onToggle: noop,
    onDelete: noop,
  }).filter((v) => v.key === "open" || v.key === "scan" || v.key === "log" || v.key === "edit")

  useEffect(() => {
    if (!vhost) return
    const controller = new AbortController()
    get<{ content: string }>("/proxy/config", { path: vhost.path }, controller.signal)
      .then((r) => {
        setContent(r.content)
        setOriginal(r.content)
      })
      .catch((err) => !controller.signal.aborted && notify.error("Could not read the file", err))
    return () => controller.abort()
  }, [vhost])

  const validate = async () => {
    if (!vhost) return
    setBusy(true)
    try {
      setValidation(
        await post<ProxyValidation>("/proxy/validate", {
          kind: vhost.kind,
          path: vhost.path,
          content,
        }),
      )
    } catch (err) {
      notify.error("Validation failed", err)
    } finally {
      setBusy(false)
    }
  }

  const save = async (reload: boolean) => {
    if (!vhost) return
    setBusy(true)
    try {
      await put("/proxy/config", { kind: vhost.kind, path: vhost.path, content, reload })
      notify.success(reload ? "Saved and reloaded" : "Saved")
      setOriginal(content)
      onSaved()
    } catch (err) {
      notify.error("Not applied", err)
    } finally {
      setBusy(false)
    }
  }

  const dirty = content !== original
  const disabledNginx =
    vhost?.kind === "nginx" && Boolean(vhost.enabledPath) && !vhost.enabled && !validation?.note

  return (
    <SidePanel
      open={vhost !== null}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      width="xl"
      title={vhost?.name ?? "Configuration"}
      description={vhost?.path}
      actions={
        vhost && (
          <>
            <span className="truncate font-mono text-hint text-muted-foreground">{vhost.path}</span>
            <VerbBar verbs={verbs.map((v) => ({ ...v, inline: true }))} className="ml-auto" />
          </>
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col gap-3 p-4"
      footer={
        admin && vhost ? (
          <>
            <Button size="sm" variant="outline" onClick={validate} pending={busy}>
              Test config
            </Button>
            <span className="flex-1" />
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                dirty &&
                confirm({
                  title: "Discard changes",
                  confirmLabel: "Discard",
                  description: (
                    <p>What you typed here is thrown away and the file is left as it is.</p>
                  ),
                  action: async () => {
                    setContent(original)
                    setValidation(null)
                  },
                })
              }
              disabled={busy || !dirty}
            >
              Discard
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => save(false)}
              disabled={busy || !dirty}
            >
              Save only
            </Button>
            <Button size="sm" onClick={() => save(true)} disabled={busy || !dirty}>
              Save and reload
            </Button>
          </>
        ) : undefined
      }
    >
      {admin && (
        <Notice icon={ShieldCheck} title="Validated before it takes effect">
          The server runs its own config test first. A config that fails is rolled back and never
          reloaded, so a typo here cannot take your sites offline.
        </Notice>
      )}
      {disabledNginx && (
        <Notice tone="warning" icon={Warning} title="nginx is not reading this file">
          The site is disabled, so a config test cannot see it — it will pass whatever is in it.
          Enable the site before trusting the result.
        </Notice>
      )}

      <Pane className="min-h-0 flex-1">
        <CodeEditor
          className="h-full"
          language="ini"
          value={content}
          readOnly={!admin}
          onChange={(v) => {
            setContent(v)
            setValidation(null)
          }}
        />
      </Pane>

      {validation && (
        <Notice
          tone={validation.valid ? "success" : "danger"}
          title={validation.valid ? "Config is valid" : "Config is refused"}
        >
          {validation.note && <p>{validation.note}</p>}
          {validation.output && (
            <Well className="mt-2 max-h-40 whitespace-pre-wrap">{validation.output}</Well>
          )}
        </Notice>
      )}
    </SidePanel>
  )
}

const EMPTY_VHOST: VHost = {
  name: "",
  kind: "nginx",
  path: "",
  enabled: false,
  serverNames: [],
  listen: [],
  upstreams: [],
  tls: false,
  modified: "",
  size: 0,
}
