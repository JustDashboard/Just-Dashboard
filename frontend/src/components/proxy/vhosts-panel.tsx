"use client"

import { useEffect, useMemo, useState } from "react"
import { Globe, Plus, ShieldCheck, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ProxyValidation, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Pane, Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, VerbBar } from "@/components/verbs"
import { AuthFilesPanel } from "@/components/proxy/auth-files-panel"
import { SiteForm } from "@/components/proxy/site-form"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { Button } from "@/components/ui/button"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type SiteFilter = "all" | "tls" | "plain" | "disabled"

const FILTER_LABEL: Record<SiteFilter, string> = {
  all: "All",
  tls: "TLS",
  plain: "Plain HTTP",
  disabled: "Disabled",
}

/**
 * Every site this host serves, nginx and Caddy alike, as one plain table:
 * four readings, a filter row, and a site's verbs as words.
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
  const [form, setForm] = useState<{
    open: boolean
    editing: string | null
    copyFrom: string | null
    session: number
  }>({ open: false, editing: null, copyFrom: null, session: 0 })
  const [filter, setFilter] = useState("")
  const [chip, setChip] = useState<SiteFilter>("all")
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

  // A ?site= link from elsewhere in the dashboard opens that site as soon as
  // its row loads. The open panel is derived from the URL rather than copied
  // into state, so the back button closes it and a reload reopens it.
  const linked = requested ? data?.find((vhost) => vhost.name === requested) : undefined
  const rawEditing = editing ?? (linked && linked.kind !== "nginx" && linked.path ? linked : null)
  const formIsOpen = form.open || linked?.kind === "nginx"
  const formEditing = form.open ? form.editing : (linked?.name ?? null)

  const closeForm = (open: boolean) => {
    setForm((f) => ({ ...f, open }))
    if (!open) setRequested(null)
  }
  const closeRaw = (open: boolean) => {
    if (open) return
    setEditing(null)
    setRequested(null)
  }

  const hosts = useMemo(() => data ?? [], [data])
  const counts = useMemo(() => {
    const plain = hosts.filter((v) => v.enabled && !v.tls && v.upstreams.length > 0)
    const disabled = hosts.filter((v) => v.kind === "nginx" && !v.enabled && Boolean(v.enabledPath))
    return {
      all: hosts.length,
      tls: hosts.filter((v) => v.tls).length,
      plain: plain.length,
      disabled: disabled.length,
      nginx: hosts.filter((v) => v.kind === "nginx").length,
      caddy: hosts.filter((v) => v.kind === "caddy").length,
    }
  }, [hosts])
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return hosts.filter((v) => {
      if (chip === "tls" && !v.tls) return false
      if (chip === "plain" && !(v.enabled && !v.tls && v.upstreams.length > 0)) return false
      if (chip === "disabled" && !(v.kind === "nginx" && !v.enabled && v.enabledPath)) return false
      if (!needle) return true
      return (
        v.name.toLowerCase().includes(needle) ||
        v.serverNames.some((n) => n.toLowerCase().includes(needle)) ||
        v.upstreams.some((u) => u.toLowerCase().includes(needle))
      )
    })
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

  const header = (
    <PageHeader
      eyebrow="Proxy"
      title="Sites"
      actions={
        admin &&
        hasNginx && (
          <Button size="sm" onClick={() => openForm(null)}>
            <Plus className="size-4" />
            New site
          </Button>
        )
      }
    />
  )

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

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4}>
        <StatTile
          label="Sites"
          value={counts.all}
          hint={
            counts.caddy > 0
              ? `${counts.nginx} nginx · ${counts.caddy} Caddy`
              : counts.all === 0
                ? "none configured"
                : "all nginx"
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

      <Panel plain>
        <PanelHeader title="Sites" />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Site, domain or upstream"
            containerClassName="sm:w-64"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {(Object.keys(FILTER_LABEL) as SiteFilter[]).map((key) => (
              <FilterChip key={key} selected={chip === key} onClick={() => setChip(key)}>
                {FILTER_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ))}
          </div>
        </PanelToolbar>
        <PanelBody flush>
          {hosts.length === 0 ? (
            <EmptyState
              icon={Globe}
              title="No sites found"
              description={
                hasNginx
                  ? "Put a domain in front of something running on this machine — the form writes the nginx config for you."
                  : "No nginx configuration directory, Caddyfile or shared Caddy ingress was found on this host."
              }
              action={
                admin &&
                hasNginx && (
                  <Button size="sm" onClick={() => openForm(null)}>
                    <Plus className="size-4" />
                    New site
                  </Button>
                )
              }
              className="mt-4"
            />
          ) : visible.length === 0 ? (
            <EmptyState icon={Globe} title="No sites match" className="mt-4" />
          ) : (
            <>
              <div className="-mx-4 hidden min-w-0 lg:block">
                <Table containerClassName="max-h-[calc(100svh-24rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-full">Site</TableHead>
                      <TableHead>Domains</TableHead>
                      <TableHead>Upstream</TableHead>
                      <TableHead>TLS</TableHead>
                      <TableHead>Serving</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((vhost) => (
                      <SiteRow
                        key={`${vhost.kind}:${vhost.name}`}
                        vhost={vhost}
                        busy={pending[vhost.name]}
                        {...handlers}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
              <ul className="divide-y divide-hairline lg:hidden">
                {visible.map((vhost) => (
                  <SiteNarrowRow
                    key={`${vhost.kind}:${vhost.name}`}
                    vhost={vhost}
                    busy={pending[vhost.name]}
                    {...handlers}
                  />
                ))}
              </ul>
            </>
          )}
        </PanelBody>
      </Panel>

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

type RowProps = {
  vhost: VHost
  busy?: string
  admin: boolean
  onEdit: (v: VHost) => void
  onRaw: (v: VHost) => void
  onDuplicate: (v: VHost) => void
  onToggle: (v: VHost, enabled: boolean) => void
  onDelete: (v: VHost) => void
}

/** Whether the site is serving, said as a state rather than a switch. */
function ServingStatus({ vhost, busy }: { vhost: VHost; busy?: string }) {
  if (busy) return <Status state="activating" label={`${busy}…`} />
  if (vhost.kind === "nginx" && !vhost.enabledPath && vhost.enabled) {
    // conf.d: every present .conf file is active and there is nothing to
    // toggle, which "always on" says without offering a control.
    return <Status state="active" label="always on" />
  }
  return vhost.enabled ? (
    <Status state="active" label="serving" />
  ) : (
    <Status state="inactive" label="disabled" />
  )
}

function TLSStatus({ vhost }: { vhost: VHost }) {
  return vhost.tls ? (
    <Status state="active" label="TLS" icon={ShieldCheck} />
  ) : (
    <span className="text-xs text-muted-foreground">plain</span>
  )
}

function SiteRow({ vhost, busy, ...handlers }: RowProps) {
  const verbs = useSiteVerbs({ vhost, busy, ...handlers })
  const primary = () => (vhost.kind === "nginx" ? handlers.onEdit(vhost) : handlers.onRaw(vhost))
  const canOpen = vhost.kind === "nginx" ? handlers.admin : Boolean(vhost.path)
  return (
    <TableRow className="group" onActivate={canOpen ? primary : undefined}>
      <TableCell>
        <div className="max-w-[24rem] min-w-0">
          {canOpen ? (
            <RowLink onClick={primary}>{vhost.name}</RowLink>
          ) : (
            <span className="block truncate text-body font-medium">{vhost.name}</span>
          )}
          <p className="truncate font-mono text-hint text-muted-foreground">
            {vhost.path || `${vhost.kind} route`}
          </p>
        </div>
      </TableCell>
      <TableCell className="max-w-[18rem] truncate">
        {vhost.serverNames.join(", ") || <span className="text-muted-foreground">—</span>}
      </TableCell>
      <TableCell className="max-w-[14rem]">
        <Upstreams vhost={vhost} />
      </TableCell>
      <TableCell>
        <TLSStatus vhost={vhost} />
      </TableCell>
      <TableCell>
        <ServingStatus vhost={vhost} busy={busy} />
      </TableCell>
      <TableCell>
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

function Upstreams({ vhost }: { vhost: VHost }) {
  if (vhost.upstreams.length === 0) return <span className="text-muted-foreground">—</span>
  return (
    <span className="flex min-w-0 items-baseline gap-1.5">
      <span className="truncate font-mono text-hint">{vhost.upstreams[0]}</span>
      {vhost.upstreams.length > 1 && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          +{vhost.upstreams.length - 1}
        </span>
      )}
    </span>
  )
}

function SiteNarrowRow({ vhost, busy, ...handlers }: RowProps) {
  const verbs = useSiteVerbs({ vhost, busy, ...handlers })
  const primary = () => (vhost.kind === "nginx" ? handlers.onEdit(vhost) : handlers.onRaw(vhost))
  const canOpen = vhost.kind === "nginx" ? handlers.admin : Boolean(vhost.path)
  return (
    <li
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
      onClick={(event) => {
        if (!canOpen) return
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        primary()
      }}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          {canOpen ? (
            <RowLink onClick={primary}>{vhost.name}</RowLink>
          ) : (
            <span className="truncate text-body font-medium">{vhost.name}</span>
          )}
          <Tag>{vhost.kind}</Tag>
        </div>
        <p className="truncate text-hint text-muted-foreground">
          {vhost.serverNames.join(", ") || vhost.path}
        </p>
        {vhost.upstreams[0] && (
          <p className="truncate font-mono text-hint text-muted-foreground">
            → {vhost.upstreams[0]}
          </p>
        )}
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          <ServingStatus vhost={vhost} busy={busy} />
          <TLSStatus vhost={vhost} />
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
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
