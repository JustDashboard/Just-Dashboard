"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowCircleUp,
  Box,
  Copy,
  Download,
  Eye,
  EyeOff,
  Information,
  Layers,
  Pencil,
  ShieldOff,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, ApiError } from "@/lib/api"
import { bytes, duration, relativeTime, timestamp } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import type {
  ContainerDetail,
  ContainerSpec,
  DockerDiagnosis,
  FailureDiagnosis,
  FileChange,
  LogLine,
  MigrationPlan,
  PortRoute,
  WritableEntry,
  WritableLayerReport,
} from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { LogViewer } from "@/components/log-viewer"
import { XtermPane } from "@/components/xterm-pane"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ContainerUsage } from "@/components/docker/container-usage"
import { ContainerFindings } from "@/components/docker/attention"
import { PortTag, RouteRow } from "@/components/docker/exposure"
import { Hint, Term } from "@/components/docker/explain"
import type { ConfirmFn } from "@/components/docker/shared"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList } from "@/components/page"
import { Group, Well } from "@/components/panel"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { copyText } from "@/lib/clipboard"

/** How many log lines the panel keeps before dropping the oldest. */
const LOG_LIMIT = 5000

export function ContainerDetailSheet({
  containerId,
  onOpenChange,
  diagnosis,
  confirm,
  onChanged,
  onDuplicate,
}: {
  containerId: string | null
  onOpenChange: (open: boolean) => void
  /** The page's one diagnosis pass, filtered to this container rather than refetched. */
  diagnosis?: DockerDiagnosis
  confirm?: ConfirmFn
  onChanged?: () => void
  /** Opens the create form pre-filled from this container. */
  onDuplicate?: (spec: ContainerSpec) => void
}) {
  return (
    <ContainerDetailPanel
      // Keyed on the container so selecting another one starts fresh rather
      // than briefly showing the previous container's detail.
      key={containerId ?? "none"}
      containerId={containerId}
      onOpenChange={onOpenChange}
      diagnosis={diagnosis}
      confirm={confirm}
      onChanged={onChanged}
      onDuplicate={onDuplicate}
    />
  )
}

function ContainerDetailPanel({
  containerId,
  onOpenChange,
  diagnosis,
  confirm,
  onChanged,
  onDuplicate,
}: {
  containerId: string | null
  onOpenChange: (open: boolean) => void
  diagnosis?: DockerDiagnosis
  confirm?: ConfirmFn
  onChanged?: () => void
  onDuplicate?: (spec: ContainerSpec) => void
}) {
  const { can } = useAuth()
  const [detail, setDetail] = useState<ContainerDetail>()
  const [error, setError] = useState<Error>()
  // Which tab a container opens on. Somebody watching a deploy wants Logs
  // every time, and reopening on Overview is a click paid per container.
  const [tab, setTab] = useViewState("docker.container.tab", "overview")
  const [reloads, setReloads] = useState(0)

  useEffect(() => {
    if (!containerId) return
    const controller = new AbortController()
    get<ContainerDetail>(
      `/docker/containers/${encodeURIComponent(containerId)}`,
      undefined,
      controller.signal,
    )
      .then(setDetail)
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [containerId, reloads])

  const shell = can("terminal") && detail?.state === "running"

  return (
    <SidePanel
      open={containerId !== null}
      onOpenChange={onOpenChange}
      icon={Box}
      title={
        <>
          {detail?.name ?? "Container"}
          {detail && <Status state={detail.state} />}
        </>
      }
      description={detail?.image ?? containerId ?? undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
      actions={
        detail && (
          <ContainerActions
            detail={detail}
            confirm={confirm}
            onDuplicate={onDuplicate}
            onChanged={() => {
              setReloads((n) => n + 1)
              onChanged?.()
            }}
          />
        )
      }
    >
      {error && <ErrorState error={error} />}
      {!detail && !error && <LoadingRows />}

      {detail && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-3">
          <TabsList className="w-fit shrink-0">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="usage">Usage</TabsTrigger>
            <TabsTrigger value="logs">Logs</TabsTrigger>
            <TabsTrigger value="env">Environment</TabsTrigger>
            <TabsTrigger value="mounts">Storage</TabsTrigger>
            <TabsTrigger value="inspect">Inspect</TabsTrigger>
            {shell && <TabsTrigger value="shell">Shell</TabsTrigger>}
          </TabsList>

          <TabsContent value="overview" className="min-h-0 flex-1 space-y-4 overflow-y-auto">
            {/*
              Why it is not working, then what is wrong with it, then the facts
              about it. An operator who opened this panel opened it for the
              first of those, and the version this replaces led with the third.
            */}
            <FailurePanel containerId={detail.id} />
            <ContainerFindings diagnosis={diagnosis} containerId={detail.id} />
            <OverviewFields detail={detail} />
            <Reachability containerId={detail.id} />
          </TabsContent>

          {/* Recorded history rather than a live feed: the point is the spike
              that happened while nobody had this panel open. The limits sit
              above the charts because this is where somebody realises theirs
              are wrong. */}
          <TabsContent value="usage" className="min-h-0 flex-1 space-y-3 overflow-y-auto">
            <ResourceLimitsEditor detail={detail} onSaved={() => setReloads((n) => n + 1)} />
            <ContainerUsage containerId={detail.id} name={detail.name} />
          </TabsContent>

          <TabsContent value="logs" className="min-h-0 flex-1">
            <ContainerLogs containerId={detail.id} active={tab === "logs"} />
          </TabsContent>

          <TabsContent value="env" className="min-h-0 flex-1">
            <EnvironmentList env={detail.env} />
          </TabsContent>

          <TabsContent value="mounts" className="min-h-0 flex-1 space-y-3 overflow-y-auto">
            <WritableLayer containerId={detail.id} />
            {detail.mounts.map((mount, i) => (
              <Group key={i} className="text-xs">
                <div className="mb-1.5 flex items-center gap-2">
                  <Tag>{mount.type}</Tag>
                  <Tag>{mount.rw ? "read-write" : "read-only"}</Tag>
                </div>
                <p className="font-mono break-all">
                  <span className="text-muted-foreground">{mount.source}</span>
                  {" → "}
                  {mount.destination}
                </p>
              </Group>
            ))}
            {detail.mounts.length === 0 && (
              <Hint>
                Nothing is mounted, so everything this container writes lives in{" "}
                <Term name="writableLayer">its own filesystem</Term> and is destroyed when it is
                replaced.
              </Hint>
            )}
          </TabsContent>

          <TabsContent value="inspect" className="min-h-0 flex-1">
            {tab === "inspect" && <RawInspect containerId={detail.id} />}
          </TabsContent>

          {shell && (
            <TabsContent value="shell" className="min-h-0 flex-1">
              {tab === "shell" && <ContainerShell detail={detail} />}
            </TabsContent>
          )}
        </Tabs>
      )}
    </SidePanel>
  )
}

/**
 * A shell *inside* the container — which is the whole point of it, and the
 * thing most easily mistaken for the Terminal page.
 *
 * The two answer different questions. This one lands wherever the image says:
 * as the image's USER, in its WORKDIR. For most images that is root in `/` or
 * `/app`, and that is not a bug to be fixed — a container shell that quietly
 * became a host login would leave no way to look inside a container at all.
 * The Terminal page is the host; this is the box running on it.
 *
 * Docker already honours the image's user by default, so "default" sends no
 * user at all rather than guessing one. Root is offered because the common
 * reason to open this at all is that something needs installing or reading in
 * an image that deliberately runs unprivileged.
 */
function ContainerShell({ detail }: { detail: ContainerDetail }) {
  const [asRoot, setAsRoot] = useState(false)

  // Most images declare no USER, so the container already runs as root and
  // there is no second account to offer. The toggle used to be drawn anyway,
  // from `detail.user || "root"` — which rendered two buttons both labelled
  // "root" that switched between a request with no user and a request for
  // root, i.e. between the same thing twice.
  const imageUser = detail.user.trim()
  const runsAsRoot =
    imageUser === "" || imageUser === "root" || imageUser.startsWith("0:") || imageUser === "0"
  const account = asRoot || runsAsRoot ? "root" : imageUser

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1">
          Inside <span className="font-mono text-foreground">{detail.name}</span>, not on the host —
          the Terminal page is the server itself.
        </span>
        {runsAsRoot ? (
          // Worth stating rather than leaving blank: "which account am I" is
          // the first thing you need to know in a container shell, and this
          // one is the answer most people assume without checking.
          <span className="shrink-0">
            as <span className="font-mono text-foreground">root</span> — this image sets no user
          </span>
        ) : (
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={asRoot ? "root" : "default"}
            onValueChange={(v) => v && setAsRoot(v === "root")}
          >
            <ToggleGroupItem value="default" className="px-2 text-hint">
              {imageUser}
            </ToggleGroupItem>
            <ToggleGroupItem value="root" className="px-2 text-hint">
              root
            </ToggleGroupItem>
          </ToggleGroup>
        )}
      </div>
      <XtermPane
        // Keyed on the account, so switching it opens a new exec rather than
        // leaving you in the previous one with a stale label above it.
        key={account}
        path={`/docker/containers/${detail.id}/exec`}
        // No user at all when the image's own is wanted: Docker already
        // honours it, and naming it would override a `user:group` form with
        // just the user half.
        query={{ rows: 30, cols: 100, user: asRoot && !runsAsRoot ? "root" : undefined }}
        className="min-h-0 flex-1"
        subtitle={`${account}@${detail.name} · container shell`}
      />
    </div>
  )
}

/**
 * Names that conventionally hold a credential. The server already withholds
 * these values from anyone below system.admin; this list is what decides
 * whether an admin's copy is printed on screen or kept behind a click, so it
 * errs towards hiding — a needless extra click costs less than a key read over
 * someone's shoulder or captured in a screen share.
 */
const SECRET_ENV_HINTS = [
  "SECRET",
  "PASSWORD",
  "PASSWD",
  "TOKEN",
  "CREDENTIAL",
  "PRIVATE",
  "SALT",
  "SIGNATURE",
  "CIPHER",
  "APIKEY",
  "API_KEY",
  "AUTH",
  "DSN",
  "_KEY",
  "KEY_",
  "MASTER_KEY",
  "ACCESS",
  "SESSION",
  // A connection string carries the password inside it, so the variable name
  // gives no hint that the value is a credential — DATABASE_URL is the single
  // most common way a password ends up on somebody's screen.
  "JWT",
  "DATABASE_URL",
  "DB_URL",
  "CONNECTION_STRING",
  "_URI",
  "WEBHOOK",
]

function isSecretEnvKey(name: string) {
  const upper = name.toUpperCase()
  return upper === "KEY" || SECRET_ENV_HINTS.some((hint) => upper.includes(hint))
}

function EnvironmentList({ env }: { env: string[] }) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})
  const rows = useMemo(
    () =>
      env.map((line) => {
        const eq = line.indexOf("=")
        const name = eq === -1 ? line : line.slice(0, eq)
        const value = eq === -1 ? "" : line.slice(eq + 1)
        return { line, name, value, secret: isSecretEnvKey(name) }
      }),
    [env],
  )
  const secretCount = rows.filter((r) => r.secret).length

  if (rows.length === 0) {
    return <EmptyNote>No environment variables set.</EmptyNote>
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      {secretCount > 0 && (
        <p className="flex items-start gap-2 text-xs text-muted-foreground">
          <ShieldOff className="mt-px size-3.5 shrink-0" />
          <span>
            {secretCount} {secretCount === 1 ? "value looks" : "values look"} like a credential and
            {secretCount === 1 ? " is" : " are"} hidden. Reveal only when nobody is watching your
            screen.
          </span>
        </p>
      )}
      <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto font-mono text-xs">
        {rows.map((row) => {
          const show = !row.secret || revealed[row.name]
          return (
            <div
              key={row.line}
              className="flex items-start gap-2 rounded-md px-2 py-1 hover:bg-row-hover"
            >
              <span className="shrink-0 text-muted-foreground">{row.name}=</span>
              {show ? (
                <span className="break-all">{row.value}</span>
              ) : (
                <span className="text-muted-foreground select-none">••••••••••••</span>
              )}
              {/*
                Copy sits beside Reveal so the common case — pasting a
                credential into a client — does not require putting it on
                screen first. The value goes to the clipboard and nowhere
                else: it is never logged, notified with, or sent anywhere.
              */}
              {row.secret && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="ml-auto shrink-0 font-normal"
                  aria-label={`Copy ${row.name}`}
                  onClick={() => void copyText(row.value, `${row.name} copied`)}
                >
                  <Copy />
                  Copy
                </Button>
              )}
              {row.secret && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="shrink-0 font-normal"
                  aria-label={`${revealed[row.name] ? "Hide" : "Reveal"} ${row.name}`}
                  onClick={() => setRevealed((prev) => ({ ...prev, [row.name]: !prev[row.name] }))}
                >
                  {revealed[row.name] ? <EyeOff /> : <Eye />}
                  {revealed[row.name] ? "Hide" : "Reveal"}
                </Button>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function OverviewFields({ detail }: { detail: ContainerDetail }) {
  return (
    <div className="space-y-5">
      <DetailList>
        <Detail label="Container ID">
          <span className="font-mono break-all">{detail.id.slice(0, 20)}</span>
        </Detail>
        <Detail label="Image">
          <span className="font-mono break-all">{detail.image}</span>
        </Detail>
        <Detail label="Command">
          <span className="font-mono break-all">{detail.command || "—"}</span>
        </Detail>
        <Detail label="Created">{timestamp(detail.createdAt)}</Detail>
        <Detail label="Started">
          {detail.startedAt
            ? `${timestamp(detail.startedAt)} (${duration(detail.uptimeSeconds)})`
            : "—"}
        </Detail>
        <Detail label={<Term name="restart">Restart policy</Term>}>
          {detail.restartPolicy || "none"}
        </Detail>
        <Detail label="Restarts">{detail.restartCount}</Detail>
        <Detail label="Exit code">{detail.state === "running" ? "—" : detail.exitCode}</Detail>
        <Detail label={<Term name="networkMode">Network mode</Term>}>
          {detail.networkMode === "host" ? (
            <span className="text-warning">host — the server&rsquo;s own network</span>
          ) : (
            detail.networkMode
          )}
        </Detail>
        <Detail label="Working dir">{detail.workingDir || "—"}</Detail>
        <Detail label={<Term name="containerUser">User</Term>}>{detail.user || "default"}</Detail>
        <Detail label={<Term name="privileged">Privileged</Term>}>
          {detail.privileged ? (
            <span className="text-destructive">yes — full host access</span>
          ) : (
            "no"
          )}
        </Detail>
      </DetailList>

      {detail.networkDetails.length > 0 && (
        <div className="space-y-2">
          <p className="eyebrow">Networks</p>
          {detail.networkDetails.map((net) => (
            <Group key={net.networkId} className="text-xs">
              <div className="font-medium">{net.name}</div>
              <p className="font-mono text-muted-foreground">
                {net.ipAddress || "no address"} · gateway {net.gateway || "—"}
              </p>
            </Group>
          ))}
        </div>
      )}

      {detail.ports.length > 0 && (
        <div className="space-y-2">
          <p className="eyebrow">Ports</p>
          <div className="flex flex-wrap gap-1.5">
            {/*
              A published port on a known address becomes a link, and every
              badge carries what its binding means — see the exposure model. A
              port on every interface deliberately gets no link: which of this
              machine's addresses is the right one depends on where the reader
              is, and guessing produces a link that leads somewhere else.
            */}
            {(detail.exposure ?? []).map((port, i) => (
              <PortTag key={`${port.hostIp}-${port.hostPort}-${i}`} port={port} />
            ))}
          </div>
          {detail.ports.some((p) => p.publicPort && (!p.ip || p.ip === "0.0.0.0")) && (
            <Hint>
              A port published on every interface is reachable from anywhere that can route to this
              server. Docker writes it as a NAT rule, which is consulted before the firewall&apos;s
              own.
            </Hint>
          )}
        </div>
      )}
    </div>
  )
}

function ContainerLogs({ containerId, active }: { containerId: string; active: boolean }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [timestamps, setTimestamps] = useState(true)

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "logs") return
    const batch = envelope.data as { stream: string; text: string }[]
    setLines((prev) => {
      const next = [
        ...prev,
        ...batch.map((l) => ({
          text: l.text,
          level: l.stream === "stderr" ? "error" : undefined,
        })),
      ]
      return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
    })
  }, [])

  const query = useMemo(
    () => ({ tail: 500, timestamps: timestamps ? "true" : "false" }),
    [timestamps],
  )
  const { state } = useSocket(`/docker/containers/${containerId}/logs/stream`, {
    onMessage,
    enabled: active,
    query,
  })

  return (
    <LogViewer
      className="h-full"
      lines={lines}
      showTimestamps={false}
      onClear={() => setLines([])}
      emptyMessage={state === "open" ? "No output yet." : "Connecting…"}
      toolbar={
        <>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => {
              setLines([])
              setTimestamps((t) => !t)
            }}
          >
            {timestamps ? "Hide times" : "Show times"}
          </Button>
          {/*
            Saved from what is already in the browser rather than re-fetched.
            The pane holds the tail it was given plus everything since; asking
            the server for a file would be a second, differently-truncated copy
            of the same thing, and this is what the reader is actually looking
            at.
          */}
          <Button
            size="xs"
            variant="ghost"
            onClick={() => downloadLines(containerId, lines)}
            disabled={lines.length === 0}
          >
            <Download className="size-3" />
            Save
          </Button>
        </>
      }
    />
  )
}

/**
 * The actions that change what a container *is*, rather than what it is doing.
 *
 * Docker has no notion of editing a container: every field but a handful of
 * resource limits is fixed at creation, and the universal workaround is to
 * destroy and recreate. Every UI in this class therefore either omits these
 * entirely or hides them behind a "duplicate" button that quietly leaves the
 * original running. Here they are named for what they do, and the server does
 * the destroy-and-recreate with the original parked aside until the
 * replacement is up.
 */
function ContainerActions({
  detail,
  confirm,
  onChanged,
  onDuplicate,
}: {
  detail: ContainerDetail
  confirm?: ConfirmFn
  onChanged: () => void
  onDuplicate?: (spec: ContainerSpec) => void
}) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const composeManaged = Boolean(detail.composeStack)

  const duplicate = async () => {
    setBusy(true)
    try {
      const spec = await get<ContainerSpec>(`/docker/containers/${detail.id}/spec`)
      // A copy under the same name would collide, and Docker's error for that
      // is a 409 the operator has to decode. Naming it here is friendlier and
      // is what they were going to type anyway.
      onDuplicate?.({ ...spec, name: `${spec.name}-copy`, start: true })
    } catch (err) {
      notify.error("Could not read this container's settings", err)
    } finally {
      setBusy(false)
    }
  }

  const update = () =>
    confirm?.({
      title: "Update this container",
      confirmLabel: "Update",
      description: (
        <>
          <p>
            Pulls a newer <b>{detail.image}</b> and replaces <b>{detail.name}</b> with a container
            built from it, keeping every setting it has now.
          </p>
          <p>
            Its volumes come with it. Anything written inside the container rather than into a
            volume does not — that is destroyed with the old container.
          </p>
        </>
      ),
      action: async (phrase) => {
        await post(
          `/docker/containers/${detail.id}/recreate`,
          { pullLatest: true },
          { confirm: phrase },
        )
        onChanged()
      },
    })

  /*
    Compose owns this container, so anything that changes its configuration is
    undone by the next deploy — silently, and days later, which is the worst
    way to find out. Rename and Duplicate used to sit here for a compose
    container exactly as they do for a standalone one: renaming one breaks
    compose's own lookup, and duplicating one produces a container compose does
    not know about and will remove as an orphan.

    They are not hidden — an operator who means it should be able to do it —
    but they move behind a statement of what will happen, and the actions that
    are safe on a compose container come first.
  */
  if (composeManaged) {
    return (
      <>
        <Tag>managed by compose</Tag>
        <Button size="sm" variant="outline" asChild>
          <Link href={`/docker/stacks?stack=${encodeURIComponent(detail.composeStack ?? "")}`}>
            <Layers className="size-3.5" />
            Open {detail.composeStack}
          </Link>
        </Button>
        {can("service.control") && (
          <ComposeDriftMenu
            detail={detail}
            confirm={confirm}
            busy={busy}
            onDuplicate={onDuplicate ? duplicate : undefined}
            onChanged={onChanged}
          />
        )}
      </>
    )
  }

  return (
    <>
      {can("destructive") && confirm && (
        <Button size="sm" variant="outline" onClick={update} disabled={busy}>
          <ArrowCircleUp className="size-3.5" />
          Update
        </Button>
      )}
      {can("service.control") && onDuplicate && (
        <Button size="sm" variant="outline" onClick={duplicate} pending={busy}>
          <Copy className="size-3.5" />
          Duplicate
        </Button>
      )}
      {can("service.control") && <RenameButton detail={detail} onRenamed={onChanged} />}
    </>
  )
}

/**
 * The configuration changes that compose will undo, behind a statement saying
 * so.
 *
 * Not hidden and not disabled: an operator who genuinely wants to rename a
 * compose container — to get it out of the project, say — should be able to.
 * What they should not be able to do is press it without being told that the
 * next deploy puts it back, because the failure is silent and arrives days
 * later.
 */
function ComposeDriftMenu({
  detail,
  confirm,
  busy,
  onDuplicate,
  onChanged,
}: {
  detail: ContainerDetail
  confirm?: ConfirmFn
  busy: boolean
  onDuplicate?: () => void
  onChanged: () => void
}) {
  const [open, setOpen] = useState(false)
  if (!open) {
    return (
      <Button size="sm" variant="ghost" disabled={busy} onClick={() => setOpen(true)}>
        Advanced
      </Button>
    )
  }
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Hint className="basis-full text-warning">
        This container is managed by <b>{detail.composeStack}</b>. Changes made here are replaced
        the next time the stack is deployed — edit the compose file instead if you want them to
        last.
      </Hint>
      {onDuplicate && (
        <Button
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() =>
            confirm?.({
              title: "Duplicate a compose-managed container",
              confirmLabel: "Duplicate",
              description: (
                <>
                  <p>
                    The copy is created outside <b>{detail.composeStack}</b> and compose will not
                    know about it. It carries the project labels, so the next deploy with{" "}
                    <code>--remove-orphans</code> — which is what Deploy runs — removes it again.
                  </p>
                  <p>Adding a service to the compose file is the version of this that survives.</p>
                </>
              ),
              action: async () => onDuplicate(),
            })
          }
        >
          <Copy className="size-3.5" />
          Duplicate anyway
        </Button>
      )}
      <RenameButton detail={detail} onRenamed={onChanged} />
    </span>
  )
}

function RenameButton({ detail, onRenamed }: { detail: ContainerDetail; onRenamed: () => void }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(detail.name)

  const save = async () => {
    try {
      await post(`/docker/containers/${detail.id}/rename`, { name })
      notify.success(`Renamed to ${name}`)
      setEditing(false)
      onRenamed()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error("Could not rename it", message)
    }
  }

  if (!editing) {
    return (
      <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
        <Pencil className="size-3.5" />
        Rename
      </Button>
    )
  }
  return (
    <span className="flex items-center gap-1.5">
      <Input
        autoFocus
        value={name}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") save()
          if (e.key === "Escape") setEditing(false)
        }}
        className="h-8 w-40 font-mono text-xs"
      />
      <Button size="xs" onClick={save} disabled={!name.trim() || name === detail.name}>
        Save
      </Button>
      <Button size="xs" variant="ghost" onClick={() => setEditing(false)}>
        Cancel
      </Button>
    </span>
  )
}

/**
 * Where the writable layer went, and whether any of it matters.
 *
 * "This container has written 38.7 GB into itself" is a true sentence nobody
 * can act on. The questions behind it are which directory holds it, whether
 * that directory looks like data somebody meant to keep, and whether anything
 * is mounted at it — because a writable layer is destroyed by every recreate,
 * so 35 GB under /app/data with no volume there is a database that will vanish
 * on the next image update.
 *
 * The breakdown is measured by running `du` inside the container, which means
 * it needs the container running and needs the image to ship `du`. Both
 * failures are reported as themselves rather than as an empty result: an
 * unmeasurable layer is a different thing from an empty one.
 */
function WritableLayer({ containerId }: { containerId: string }) {
  const [analyzing, setAnalyzing] = useState(false)
  const [report, setReport] = useState<WritableLayerReport | null>(null)
  const [failed, setFailed] = useState<Error | null>(null)

  const changes = usePoll<FileChange[]>(
    (signal) => get<FileChange[]>(`/docker/containers/${containerId}/changes`, undefined, signal),
    0,
    [containerId],
  )

  const interesting = useMemo(() => {
    const noise =
      /^\/(tmp|run|proc|sys|dev|var\/(run|log|cache|tmp|lib\/(apt|dpkg))|etc\/(hosts|hostname|resolv\.conf|mtab))/
    return (changes.data ?? []).filter((c) => c.kind !== "deleted" && !noise.test(c.path))
  }, [changes.data])

  // Directories with children collapse to the directory: fifty files under
  // /var/lib/postgresql/data is one fact, not fifty.
  const roots = useMemo(() => {
    const out: string[] = []
    for (const change of interesting) {
      if (!out.some((r) => change.path === r || change.path.startsWith(r + "/"))) {
        out.push(change.path)
      }
    }
    return out.slice(0, 40)
  }, [interesting])

  const analyze = async (refresh = false) => {
    setAnalyzing(true)
    setFailed(null)
    try {
      setReport(
        await get<WritableLayerReport>(
          `/docker/containers/${containerId}/writable-layer`,
          refresh ? { refresh: true } : undefined,
        ),
      )
    } catch (err) {
      setFailed(err as Error)
    } finally {
      setAnalyzing(false)
    }
  }

  if (changes.loading || (roots.length === 0 && !report)) return null

  return (
    <div className="space-y-3">
      <Notice title="Written inside the container" icon={Warning} tone="warning">
        <p>
          These paths are in <Term name="writableLayer">the container&apos;s own filesystem</Term>{" "}
          rather than a volume. They are not backed up, and they are destroyed the next time this
          container is recreated — which includes every image update.
        </p>
        <div className="mt-2 max-h-32 space-y-0.5 overflow-auto font-mono text-hint">
          {roots.map((path) => (
            <div key={path} className="truncate">
              {path}
            </div>
          ))}
        </div>
        <Button
          size="xs"
          variant="outline"
          className="mt-2"
          onClick={() => analyze(Boolean(report))}
          pending={analyzing}
        >
          {report ? "Measure again" : "Analyze writable layer"}
        </Button>
      </Notice>

      {failed && <ErrorState error={failed} />}
      {report && <WritableLayerReportView report={report} containerId={containerId} />}
    </div>
  )
}

function WritableLayerReportView({
  report,
  containerId,
}: {
  report: WritableLayerReport
  containerId: string
}) {
  if (report.state !== "measured") {
    return (
      <Notice
        title={
          report.state === "failed"
            ? "The breakdown could not be measured"
            : "The breakdown is not available for this container"
        }
        icon={Information}
      >
        <p>{report.reason}</p>
        {report.total > 0 && (
          <p className="mt-1">
            Docker still reports the total: <b>{bytes(report.total)}</b>.
          </p>
        )}
      </Notice>
    )
  }

  const biggest = report.entries.filter((e) => e.size > 0).slice(0, 8)
  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="eyebrow">Where it went</p>
        <p className="text-hint text-muted-foreground">
          {report.method} · {relativeTime(report.measuredAt)}
        </p>
      </div>

      <div className="space-y-1">
        {biggest.map((entry) => (
          <div
            key={entry.path}
            className="flex min-w-0 items-center gap-2 rounded-md border border-hairline px-2.5 py-1.5 text-xs"
          >
            <span className="min-w-0 flex-1 truncate font-mono text-hint">{entry.path}</span>
            {entry.mounted ? (
              <Tag>on a mount</Tag>
            ) : entry.persistent ? (
              <Tag tone="warning">not backed by storage</Tag>
            ) : (
              <Tag>{entry.kind}</Tag>
            )}
            <span className="numeric shrink-0 font-mono text-hint">{bytes(entry.size)}</span>
          </div>
        ))}
      </div>

      {/*
        The two figures come from two different measurements — Docker's diff
        accounting and `du` counting allocated blocks — so they will not match
        exactly. Stating the gap is more trustworthy than reconciling it
        silently.
      */}
      <Hint>
        Docker reports {bytes(report.total)} for the whole layer; the directories above account for{" "}
        {bytes(report.accounted)}. The two are measured differently — Docker counts the difference
        from the image, `du` counts allocated blocks — so they agree only approximately.
      </Hint>

      {report.unbacked.length > 0 && (
        <MigrationSuggestion containerId={containerId} entries={report.unbacked} />
      )}
    </section>
  )
}

/**
 * A directory holding real data with no volume under it, and what to do about
 * it — described, never performed.
 *
 * The safe version of this migration stops the service, copies data the
 * operator has just been told they cannot afford to lose, edits the compose
 * file and starts it again, with a rollback if any step fails. That is not a
 * thing to do silently behind a button, so the dashboard produces the exact
 * plan and the exact commands and the operator runs them, able to stop between
 * any two.
 */
function MigrationSuggestion({
  containerId,
  entries,
}: {
  containerId: string
  entries: WritableEntry[]
}) {
  const [plan, setPlan] = useState<MigrationPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const worst = entries[0]

  const build = async () => {
    setBusy(true)
    try {
      setPlan(
        await get<MigrationPlan>(`/docker/containers/${containerId}/migration-plan`, {
          path: worst.path,
        }),
      )
    } catch (err) {
      notify.error("Could not work out a migration plan", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Notice title="This data will not survive a recreate" icon={Warning} tone="warning">
      <p>
        <span className="font-mono">{worst.path}</span> holds {bytes(worst.size)} and nothing is
        mounted there, so it lives in the container&apos;s own filesystem. Every image update
        replaces that filesystem.
      </p>
      {entries.length > 1 && (
        <p className="mt-1">
          {entries.length - 1} other {entries.length === 2 ? "directory is" : "directories are"} in
          the same position:{" "}
          {entries
            .slice(1)
            .map((e) => e.path)
            .join(", ")}
          .
        </p>
      )}
      {!plan ? (
        <Button size="xs" variant="outline" className="mt-2" onClick={build} pending={busy}>
          Create a migration plan
        </Button>
      ) : (
        <div className="mt-2 space-y-2">
          <ol className="space-y-1.5 text-hint">
            {plan.steps.map((step, i) => (
              <li key={i}>
                <b>
                  {i + 1}. {step.title}
                </b>
                <span className="block text-muted-foreground">{step.detail}</span>
              </li>
            ))}
          </ol>
          <div>
            <p className="eyebrow mb-1">The commands</p>
            <Well className="max-h-48 font-mono text-micro whitespace-pre">
              {plan.commands.join("\n")}
            </Well>
          </div>
          {plan.composePatch && (
            <div>
              <p className="eyebrow mb-1">Add to the compose file</p>
              <Well className="font-mono text-micro whitespace-pre">{plan.composePatch}</Well>
            </div>
          )}
          {plan.warnings.map((warning, i) => (
            <p key={i} className="text-hint text-warning">
              {warning}
            </p>
          ))}
        </div>
      )}
    </Notice>
  )
}

/**
 * Masks credential-shaped environment values inside a decoded inspect document.
 *
 * Mirrors what the server does for anyone below system.admin. For an admin the
 * server sends the real values — correctly, they are allowed to see them — but
 * the Environment tab still puts them behind a deliberate reveal, and one tab
 * over printing the same secrets unprompted made that gesture worthless. The
 * threat here is a screen, not a permission.
 *
 * Only the two places the Engine puts an environment are walked, not every
 * string in the document: a blanket scrub mangles labels and commands that
 * legitimately contain the word "key".
 */
function maskRawEnv(doc: Record<string, unknown>): {
  doc: Record<string, unknown>
  masked: number
} {
  let masked = 0
  const config = doc.Config
  if (!config || typeof config !== "object") return { doc, masked }
  const env = (config as Record<string, unknown>).Env
  if (!Array.isArray(env)) return { doc, masked }

  const maskedEnv = env.map((entry) => {
    if (typeof entry !== "string") return entry
    const eq = entry.indexOf("=")
    if (eq === -1) return entry
    const name = entry.slice(0, eq)
    if (!isSecretEnvKey(name)) return entry
    masked++
    return `${name}=••••••••••••`
  })
  if (masked === 0) return { doc, masked }
  return {
    doc: { ...doc, Config: { ...(config as Record<string, unknown>), Env: maskedEnv } },
    masked,
  }
}

/**
 * The Engine's own inspect output.
 *
 * Every panel here is a chosen subset of something, and eventually somebody
 * needs the field nobody chose. This is also the check on the rest of the
 * page: an operator who suspects the dashboard is misreporting something can
 * see what it was reading. Credential-shaped environment values are masked on
 * the server for anyone below system.admin — the raw route is not a way around
 * that — and masked again here for the admin who is allowed to read them, so
 * that opening a tab is never by itself what puts a master key on screen.
 */
function RawInspect({ containerId }: { containerId: string }) {
  const { data, error, loading } = usePoll<Record<string, unknown>>(
    (signal) =>
      get<Record<string, unknown>>(`/docker/containers/${containerId}/raw`, undefined, signal),
    0,
    [containerId],
  )
  const [revealed, setRevealed] = useState(false)
  const { text, masked } = useMemo(() => {
    if (!data) return { text: "", masked: 0 }
    const result = maskRawEnv(data)
    return {
      text: JSON.stringify(revealed ? data : result.doc, null, 2),
      masked: result.masked,
    }
  }, [data, revealed])

  if (error) return <ErrorState error={error} />
  if (loading) return <LoadingRows />
  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex items-center gap-2">
        <Hint className="flex-1">
          What Docker itself reports about this container. Everything above is a reading of this.
        </Hint>
        {masked > 0 && (
          <Button size="xs" variant="ghost" onClick={() => setRevealed((r) => !r)}>
            {revealed ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
            {revealed ? "Hide" : "Reveal"}
          </Button>
        )}
        {/* Copies what is on screen, masked included: a raw inspect pasted
            into a ticket is the other way these values get away. */}
        <Button
          size="xs"
          variant="outline"
          onClick={() =>
            void copyText(text, revealed || masked === 0 ? "Copied" : "Copied, credentials masked")
          }
        >
          <Copy className="size-3" />
          Copy
        </Button>
      </div>
      {masked > 0 && (
        <p className="flex items-start gap-2 text-xs text-muted-foreground">
          <ShieldOff className="mt-px size-3.5 shrink-0" />
          <span>
            {masked} {masked === 1 ? "value looks" : "values look"} like a credential and{" "}
            {masked === 1 ? "is" : "are"} hidden here too, the same as on the Environment tab.
          </span>
        </p>
      )}
      <Well className="min-h-0 flex-1 whitespace-pre">{text}</Well>
    </div>
  )
}

/**
 * Where this container is actually reachable from — Docker, the reverse proxy
 * and the firewall, correlated.
 *
 * Three facts this dashboard already holds in three separate panels, and the
 * question an operator has is the one none of them answers alone: can the
 * internet reach my database. Docker knows the binding; the proxy panel knows
 * the vhost; the firewall panel knows the rules. Joining them is the thing
 * nothing else in this class of tool can do, because nothing else manages all
 * three.
 *
 * And it is the easiest place to overclaim, so every verdict carries the
 * reasoning that produced it and says whether it was read or worked out. "Bound
 * to every interface" is a fact; "reachable from the internet" is a conclusion.
 */
function Reachability({ containerId }: { containerId: string }) {
  const { data } = usePoll<PortRoute[]>(
    (signal) => get<PortRoute[]>(`/docker/containers/${containerId}/routes`, undefined, signal),
    0,
    [containerId],
  )
  if (!data || data.length === 0) return null

  const exposed = data.filter((r) => r.reach === "external")
  const bypassed = data.filter((r) => r.firewall.dockerBypass && r.firewall.verdict === "denied")

  return (
    <section className="space-y-2">
      <p className="eyebrow">Reachable at</p>
      <div className="space-y-1.5">
        {data.map((route) => (
          <RouteRow key={`${route.hostIp}-${route.hostPort}-${route.protocol}`} route={route} />
        ))}
      </div>
      {bypassed.length > 0 && (
        <Notice title="The firewall does not apply to these ports" icon={Warning} tone="warning">
          Docker publishes a port by writing NAT rules that are consulted before the firewall&apos;s
          own filter chain, so a rule denying the port has no effect on it. The way to close a
          published port is to bind it to 127.0.0.1 rather than to deny it in the firewall.
        </Notice>
      )}
      {exposed.some((r) => r.vhost) && (
        <Hint>
          A port that is both published on every interface <em>and</em> proxied can be reached
          directly, skipping whatever the proxy site enforces in front of it. Binding it to
          127.0.0.1 leaves the proxy as the only way in.
        </Hint>
      )}
    </section>
  )
}

/**
 * Why it stopped, and why it keeps stopping.
 *
 * The facts are already on the page and none of them is an answer: a restart
 * count, an exit code, a memory limit. Somebody who has done this for years
 * reads those three as "it is exceeding its limit and the kernel is killing
 * it"; everybody else reads three numbers. This is the assembly — and because
 * it is an assembly rather than a reading, the conclusion says "likely".
 */
function FailurePanel({ containerId }: { containerId: string }) {
  const { data } = usePoll<FailureDiagnosis>(
    (signal) =>
      get<FailureDiagnosis>(`/docker/containers/${containerId}/failure`, undefined, signal),
    0,
    [containerId],
  )
  if (!data) return null

  // A container that has been up for a week with nothing to say deserves to be
  // told so once, quietly, rather than given a panel.
  if (data.state === "running" && !data.restarts.looping) {
    return <Hint>{data.headline}</Hint>
  }

  const tone = data.state === "looping" || data.state === "unhealthy" ? "danger" : "warning"
  return (
    <Notice title={data.headline} icon={Warning} tone={tone}>
      {data.likely && (
        <p>
          {data.likely}
          <span className="ml-1 text-hint text-muted-foreground">
            ({data.confidence === "observed" ? "recorded by Docker" : "inferred from the evidence"})
          </span>
        </p>
      )}

      <div className="mt-2 space-y-1">
        <p className="eyebrow">Evidence</p>
        {data.evidence.map((item, i) => (
          <p key={i} className="text-hint">
            <span className="font-medium">{item.label}: </span>
            <span className="text-muted-foreground">{item.value}</span>
            <span className="ml-1 text-muted-foreground/70">— {item.source}</span>
          </p>
        ))}
      </div>

      {data.suggestions.length > 0 && (
        <ul className="mt-2 space-y-1 text-hint">
          {data.suggestions.map((suggestion, i) => (
            <li key={i} className="text-muted-foreground">
              · {suggestion}
            </li>
          ))}
        </ul>
      )}

      {data.logWindow && (
        <p className="mt-2 text-hint text-muted-foreground">
          The logs worth reading are {data.logWindow.reason} — {timestamp(data.logWindow.since)} to{" "}
          {timestamp(data.logWindow.until)}. By the time you look, the tail is the next attempt
          starting up.
        </p>
      )}
    </Notice>
  )
}

/** Saves the lines currently on screen as a text file. */
function downloadLines(containerId: string, lines: LogLine[]) {
  const blob = new Blob([lines.map((l) => l.text).join("\n")], { type: "text/plain" })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = `${containerId.slice(0, 12)}-logs.txt`
  anchor.click()
  URL.revokeObjectURL(url)
}

/**
 * The limits, editable in place.
 *
 * Almost nothing about a container can be changed after it is created — this
 * is the exception, and it is worth surfacing separately for that reason: an
 * operator who set a memory limit and got the number wrong should not have to
 * destroy the container to fix it. Docker applies the change to the running
 * cgroup immediately.
 *
 * It sits next to the usage charts because that is where the mistake becomes
 * visible: a container repeatedly touching its ceiling, or one with no ceiling
 * at all climbing towards the host's.
 */
function ResourceLimitsEditor({
  detail,
  onSaved,
}: {
  detail: ContainerDetail
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [memory, setMemory] = useState("")
  const [cpus, setCpus] = useState("")
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)

  if (!can("service.control")) return null

  const save = async () => {
    setBusy(true)
    try {
      const res = await post<{ warnings: string[] }>(
        `/docker/containers/${detail.id}/resources`,
        {
          memoryMb: Number(memory) || undefined,
          cpus: Number(cpus) || undefined,
        },
        {},
      )
      if (res.warnings?.length) {
        notify.warning("Applied, with a caveat", { description: res.warnings[0] })
      } else {
        notify.success("Limits updated")
      }
      setOpen(false)
      onSaved()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error("Could not change the limits", message)
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-hairline px-3 py-2">
        <span className="min-w-0 flex-1 text-xs text-muted-foreground">
          <Term name="memoryLimit">Limits</Term> can be changed without recreating this container —
          the one part of its configuration Docker will edit in place.
        </span>
        <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
          <Pencil className="size-3" />
          Change limits
        </Button>
      </div>
    )
  }

  return (
    <Group className="space-y-2">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-32">
          <label className="text-micro text-muted-foreground" htmlFor="limit-memory">
            Memory (MB)
          </label>
          <Input
            id="limit-memory"
            type="number"
            value={memory}
            placeholder="unlimited"
            onChange={(e) => setMemory(e.target.value)}
            className="h-8 text-xs"
          />
        </div>
        <div className="w-28">
          <label className="text-micro text-muted-foreground" htmlFor="limit-cpus">
            CPU cores
          </label>
          <Input
            id="limit-cpus"
            type="number"
            step="0.5"
            value={cpus}
            placeholder="unlimited"
            onChange={(e) => setCpus(e.target.value)}
            className="h-8 text-xs"
          />
        </div>
        <Button size="sm" onClick={save} pending={busy}>
          Apply
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </Button>
      </div>
      <Hint>
        Leaving a field empty means no change. A memory limit is what makes the kernel kill this
        container rather than choosing a victim across the whole server; the trade is that it will
        be killed when it exceeds it.
      </Hint>
    </Group>
  )
}
