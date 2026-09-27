"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Code, Connection, Pencil, Plus, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, errorMessage, get, post } from "@/lib/api"
import type {
  StreamDeleteResult,
  StreamEntry,
  StreamResult,
  StreamSpec,
  StreamStatus,
} from "@/lib/types"
import {
  byUrgency,
  carries,
  formatDuration,
  includedPlace,
  listenFamily,
  listenLabel,
  moduleMissing,
  moduleRemedy,
  parseDuration,
  protocolLabel,
  saveBlocked,
  streamBody,
  streamOutage,
  streamSpecOf,
  streamsLive,
} from "@/lib/streams"
import { duration } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChoiceRow } from "@/components/flow"
import { Page, PageContext } from "@/components/page"
import { Pane, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, ProductLogos, portProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useProxy } from "@/components/proxy/proxy-context"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"
import { ProxyGrid, RoutePath } from "@/components/proxy/route-path"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * Forwarding the things that do not speak HTTP.
 *
 * A Postgres replica, a game server, an SSH bastion, a syslog collector —
 * nginx carries all of them through its stream module, and without it a
 * single-server operator with one non-HTTP service has to leave the dashboard
 * and write nginx by hand.
 *
 * Two things stand between a file here and a forwarded port, and the page has
 * to be loud about both, in order: nginx needs the stream module — Debian and
 * Ubuntu ship it as a separate package, and a stream block without it stops
 * nginx reloading at all — and nginx.conf has to include this directory from a
 * top-level stream block, or the files are written and silently ignored.
 */
export function StreamsPage() {
  const { can } = useAuth()
  const proxy = useProxy()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useSessionState<StreamEntry | null>("proxy.streams.editing", null)
  const [form, setForm] = useSessionState("proxy.streams.form", { open: false, session: 0 })
  const [raw, setRaw] = useState<StreamEntry | null>(null)
  const { data, error, loading, refresh } = usePoll<StreamStatus>(
    (signal) => get("/proxy/streams/", undefined, signal),
    60_000,
  )
  const admin = can("system.admin")
  const noNginx = !proxy.loading && !proxy.hasNginx

  const open = (stream: StreamEntry | null) => {
    setEditing(stream)
    setForm((f) => ({ open: true, session: f.session + 1 }))
  }

  const counts = useMemo(() => {
    const streams = data?.streams ?? []
    return {
      all: streams.length,
      // A stream of both protocols is a TCP forward and a UDP one.
      tcp: streams.filter((s) => carries(s, "tcp")).length,
      udp: streams.filter((s) => carries(s, "udp")).length,
      open: streams.filter((s) => s.open).length,
    }
  }, [data])

  const live = data ? streamsLive(data) : false
  const blocked = data ? saveBlocked(data) : null
  const outage = data ? streamOutage(data) : null

  const remove = (stream: StreamEntry) => {
    let result: StreamDeleteResult | undefined
    // A file the listing could not read has no port to name and no content
    // to keep; a link goes as a link, and what it points to stays.
    const port = stream.error ? null : stream.listen
    // Included inside http, nginx does read these — as http, refusing them.
    const misplaced = data?.includedIn
      ? `nginx reads these ${includedPlace(data.includedIn)}, where its test refuses them`
      : ""
    confirm({
      title: `Delete ${stream.name}`,
      confirmLabel: live ? "Delete and reload" : "Delete",
      description: (
        <p>
          {port === null
            ? live
              ? "nginx reloads without it."
              : misplaced
                ? `${misplaced}, so this file never forwarded anything.`
                : "nginx is not reading these, so this only removes the file."
            : live
              ? `Port ${port} stops being forwarded as soon as nginx reloads.`
              : misplaced
                ? `${misplaced}, so port ${port} was never forwarded.`
                : `nginx is not reading these, so port ${port} was never forwarded — this removes the file before it ever took effect.`}{" "}
          {stream.link ? (
            <>
              This removes the link only, not <code className="font-mono">{stream.link}</code>.
            </>
          ) : stream.error ? (
            "It could not be read, so no copy of it is kept."
          ) : (
            <>
              The file is kept as <code className="font-mono">{stream.name}.conf.bak</code>.
            </>
          )}
        </p>
      ),
      action: async () => {
        result = await del<StreamDeleteResult>(`/proxy/streams/${encodeURIComponent(stream.name)}`)
        refresh()
      },
      // The delete itself completed; a reload that did not is its own news,
      // because the port stays forwarded until one succeeds.
      onDone: () => {
        if (result?.reloadError) {
          notify.warning("nginx did not reload", {
            description:
              live && port !== null
                ? `Port ${port} is still forwarded until nginx reloads. ${result.reloadError}`
                : result.reloadError,
          })
        }
        // The dialog promised a .bak for a file the listing could read.
        if (result?.unread && !stream.error) {
          notify.warning("No copy was kept", {
            description: `${stream.name}.conf could not be read when it was deleted: ${result.unread}`,
          })
        }
      },
    })
  }

  const verbsFor = (stream: StreamEntry): Verb[] => [
    {
      key: "edit",
      label: "Edit",
      icon: Pencil,
      inline: true,
      disabled: Boolean(stream.error),
      run: () => open(stream),
    },
    {
      key: "raw",
      label: "Raw file",
      icon: Code,
      disabled: Boolean(stream.error),
      run: () => setRaw(stream),
    },
    {
      key: "delete",
      label: "Delete",
      icon: Trash,
      danger: true,
      run: () => remove(stream),
    },
  ]

  const header = <PageContext eyebrow="Proxy" title="Streams" />

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
        <ErrorState error={error} onRetry={refresh} />
      </Page>
    )
  }
  if (!data) return null

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4} dense>
        <StatTile
          label="Streams"
          value={counts.all}
          hint={
            counts.all === 0
              ? "nothing forwarded"
              : outage
                ? "every reload refused"
                : live
                  ? "read by nginx"
                  : "not read by nginx"
          }
          tone={counts.all === 0 ? "default" : outage ? "danger" : live ? "default" : "warning"}
        />
        <StatTile label="TCP" value={counts.tcp} hint="connection-oriented forwards" />
        <StatTile label="UDP" value={counts.udp} hint="datagram forwards" />
        <StatTile
          label="Open to anyone"
          value={counts.open}
          tone={counts.open > 0 ? "warning" : "default"}
          hint={counts.open > 0 ? "anyone may connect to these" : "every stream restricted"}
        />
      </StatGrid>

      {error && (
        // The poll failed after an earlier one did: what is below is that
        // earlier reading, and a directory that became unreadable must not
        // pass for one that is fine.
        <Notice tone="warning" icon={Warning} title="The list below is from the last read">
          <div className="space-y-2">
            <p className="break-words">{errorMessage(error)}</p>
            <Button size="sm" variant="outline" onClick={refresh}>
              Try again
            </Button>
          </div>
        </Notice>
      )}

      {noNginx ? (
        <Notice tone="warning" icon={Warning} title="nginx is not installed on this host">
          Streams are forwarded by nginx&rsquo;s stream module, so there is nothing here to
          configure until nginx is installed.
        </Notice>
      ) : (
        <Readiness status={data} />
      )}

      <Panel plain>
        <PanelHeader
          title="Port forwarding"
          actions={
            admin &&
            !noNginx &&
            // Staging a forward is still useful before nginx reads the
            // directory, but not while its test refuses every file there: the
            // notice above says why, and a form could only end in "Not saved".
            !blocked && (
              <Button size="sm" variant={live ? "default" : "outline"} onClick={() => open(null)}>
                <Plus className="size-4" />
                {live ? "New stream" : "Prepare a stream"}
              </Button>
            )
          }
        />
        <PanelBody flush>
          {data.streams.length === 0 ? (
            <EmptyState
              mark={<ProductLogos ids={["postgresql", "redis", "minecraft-java"]} size="md" />}
              title="Nothing forwarded"
              description="Point a port on this host at a service somewhere else — a database replica, a bastion, a game server. Anything TCP or UDP."
              className="mt-2"
            />
          ) : (
            // Every row opens the stream's form, so it is a choice and carries
            // the edge (§16). Each is drawn as the service its port is — a
            // forward on 5432 as Postgres — where the port says so, and as a
            // bare connection where it does not.
            <ProxyGrid aria-label="Streams">
              {[...data.streams].sort(byUrgency).map((stream, index) => (
                <ChoiceRow
                  key={stream.name}
                  verb={admin ? `Edit ${stream.name}` : stream.name}
                  onSelect={admin && !stream.error ? () => open(stream) : undefined}
                  disabled={!admin || Boolean(stream.error)}
                  index={index}
                  className="h-full gap-4 p-4"
                  leading={
                    <ProductLogo id={portProduct(stream.listen)} size="md" fallback={Connection} />
                  }
                  title={<span className="text-title">{stream.name}</span>}
                  description={describe(stream)}
                  trailing={
                    stream.error ? (
                      <Status verdict="critical" label="unreadable" />
                    ) : (
                      <Status
                        verdict={live ? "ok" : "warning"}
                        label={live ? "configured" : "not live"}
                      />
                    )
                  }
                >
                  {stream.error ? (
                    <p className="text-hint break-all text-muted-foreground">{stream.error}</p>
                  ) : (
                    <RoutePath
                      sourceLabel={
                        listenFamily(stream.address)
                          ? `Listen on every ${listenFamily(stream.address)} address`
                          : "Listen on this host"
                      }
                      source={
                        <span className="numeric text-2xl font-semibold">
                          {listenLabel(stream)}
                        </span>
                      }
                      destinationLabel="Forward to"
                      destination={stream.upstream}
                    />
                  )}
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <span className="block text-hint text-muted-foreground">Allowed sources</span>
                      <Restriction stream={stream} />
                    </div>
                    {admin && (
                      <VerbBar
                        verbs={verbsFor(stream)}
                        menuLabel={`More actions for ${stream.name}`}
                      />
                    )}
                  </div>
                </ChoiceRow>
              ))}
            </ProxyGrid>
          )}
        </PanelBody>
      </Panel>

      <StreamForm
        key={`${editing?.name ?? "new"}:${form.session}`}
        open={form.open}
        stream={editing}
        status={data}
        onOpenChange={(open) => {
          setForm((f) => ({ ...f, open }))
          if (!open) forgetSessionState("proxy.stream.form.")
        }}
        onSaved={refresh}
        onRaw={(stream) => {
          setForm((f) => ({ ...f, open: false }))
          forgetSessionState("proxy.stream.form.")
          setRaw(stream)
        }}
      />
      <ConfigEditor
        open={raw !== null}
        onOpenChange={(o) => !o && setRaw(null)}
        path={raw?.path ?? ""}
        kind="nginx"
        title={raw ? `${raw.name}.conf` : ""}
        onSaved={refresh}
      />
      {dialog}
    </Page>
  )
}

/** The kind of forward, in the card's second line. */
function describe(stream: StreamEntry): string {
  if (stream.error) return "could not be read"
  return [
    `${protocolLabel(stream.protocol)} forwarding`,
    stream.udpMode === "request" &&
      (stream.protocol === "both" ? "one reply per UDP session" : "one reply per session"),
    stream.proxyProtocol && "PROXY header",
    stream.timeout && `${duration(stream.timeout)} idle timeout`,
    stream.connectTimeout && `${duration(stream.connectTimeout)} connect timeout`,
    !stream.managed && "written by hand",
    stream.link && "a symbolic link",
  ]
    .filter(Boolean)
    .join(" · ")
}

/**
 * What stands between this directory and a forwarded port, in the order it
 * has to be fixed: an outage first — the directory stopping every reload on
 * the host — then the module, because the include snippet breaks a nginx
 * that has none; then where the directory is included.
 */
function Readiness({ status }: { status: StreamStatus }) {
  const { module } = status
  const missing = moduleMissing(module)
  const outage = streamOutage(status)
  const place = status.includedIn ? includedPlace(status.includedIn) : ""
  const unchecked =
    module.state === "unknown"
      ? ` Whether this nginx has the stream module could not be checked (${module.detail ?? "no answer"}); if nginx then reports an unknown directive "stream", it does not.`
      : ""
  if (outage === "module") {
    return (
      <Notice
        tone="danger"
        icon={Warning}
        title="nginx.conf has a stream block this nginx cannot read"
      >
        <p>
          nginx has no stream module, so its configuration test fails on the{" "}
          <code className="font-mono">stream</code> block and every reload is refused — for every
          site on this host, not only the streams. {moduleRemedy(module)} Or take the stream block
          out of nginx.conf.
        </p>
      </Notice>
    )
  }
  if (outage === "misplaced") {
    // nginx does read these files, as whatever block the include sits in, and
    // refuses them there: "not reading these" would be the opposite of it.
    return (
      <Notice tone="danger" icon={Warning} title="These files stop every nginx reload">
        <div className="space-y-2">
          <p>
            nginx.conf includes <code className="font-mono">{status.dir}</code> {place}, where a
            stream is not allowed, so nginx&rsquo;s configuration test fails on these files and
            every reload is refused — for every site on this host, not only the streams.
          </p>
          <p>
            {missing
              ? `Take out that include, which ends the refusals. ${moduleRemedy(module)} Then add this at the top level of nginx.conf — beside the http block, not inside it:`
              : "Move the include into a stream block of its own at the top level of nginx.conf — beside the http block, not inside it:"}
          </p>
          <Well className="whitespace-pre">{status.snippet}</Well>
          {!missing && <p>Deleting the files below also ends the refusals.{unchecked}</p>}
        </div>
      </Notice>
    )
  }
  if (missing) {
    return (
      <Notice tone="warning" icon={Warning} title="This nginx cannot forward streams yet">
        <div className="space-y-2">
          <p>
            nginx has no stream module, and a stream block in nginx.conf would fail its
            configuration test until it does. {moduleRemedy(module)}
          </p>
          {status.includedIn && (
            <p>
              nginx.conf includes <code className="font-mono">{status.dir}</code> {place} instead,
              where its test refuses any stream file, so nothing can be saved here until that
              include comes out.
            </p>
          )}
          <p>
            Then add this at the top level of nginx.conf — beside the{" "}
            <code className="font-mono">http</code> block, not inside it:
          </p>
          <Well className="whitespace-pre">{status.snippet}</Well>
        </div>
      </Notice>
    )
  }
  if (status.included) return null
  return (
    <Notice
      tone="warning"
      icon={Warning}
      title={
        status.includedIn
          ? "This directory is included in the wrong place"
          : "nginx is not reading these yet"
      }
    >
      <div className="space-y-2">
        {status.includedIn ? (
          <p>
            nginx.conf includes <code className="font-mono">{status.dir}</code> {place}, where nginx
            does not read the files as streams: its test refuses any file there, so nothing can be
            saved here until the include moves into a stream block of its own at the top level:
          </p>
        ) : status.includeError ? (
          <p>
            nginx.conf could not be read to tell whether it includes this directory:{" "}
            {status.includeError}. It needs a top-level stream block like this one:
          </p>
        ) : (
          <p>
            A stream lives in nginx&rsquo;s top-level <code className="font-mono">stream</code>{" "}
            block, which a site file cannot reach. Until{" "}
            <code className="font-mono">nginx.conf</code> includes this directory, anything
            configured here is written and ignored.
          </p>
        )}
        <Well className="whitespace-pre">{status.snippet}</Well>
        <p>
          Add that at the top level of nginx.conf — beside the{" "}
          <code className="font-mono">http</code> block, not inside it. The dashboard does not edit
          nginx.conf itself: every other configuration on the host depends on that file, and a bad
          write there is a server that will not start.
          {unchecked}
        </p>
      </div>
    </Notice>
  )
}

/** Who may reach the port, as a reading: a list of sources, or the fact that there is none. */
function Restriction({ stream }: { stream: StreamEntry }) {
  if (stream.error) {
    return <span className="block text-hint text-muted-foreground">unknown</span>
  }
  if (stream.open) {
    const service = DANGEROUS_PORTS[stream.listen]
    return (
      <Status
        verdict={service ? "critical" : "warning"}
        label={service ? `${service} to anyone` : "anyone"}
      />
    )
  }
  if (stream.allowFrom.length > 0) {
    return (
      <span className="block font-mono text-hint break-all text-muted-foreground">
        {stream.allowFrom.join(", ")}
      </span>
    )
  }
  return <span className="block text-hint text-muted-foreground">set in the file</span>
}

/**
 * The form's reminder, at the point of commit, of what keeps a saved stream
 * from forwarding — with the fix to hand, in the order it has to be made.
 */
function NotLive({ status }: { status: StreamStatus }) {
  const missing = moduleMissing(status.module)
  return (
    <Notice tone="warning" icon={Warning} title="This will not forward anything yet">
      <div className="space-y-2">
        <p>
          {missing
            ? `nginx has no stream module, so nothing can read what you save here. ${moduleRemedy(status.module)} Then include this directory from a top-level stream block:`
            : "nginx.conf has no stream block including this directory, so what you save here is written and ignored. Add this at the top level of nginx.conf — beside the http block, not inside it — and this stream starts forwarding on the next reload:"}
        </p>
        <Well className="whitespace-pre">{status.snippet}</Well>
      </div>
    </Notice>
  )
}

/**
 * Why the form cannot save at all: nginx's test refuses every stream file in
 * the directory, so the fix is outside the form and comes first — and where
 * nginx has no stream module, the module comes before the stream block.
 */
function SaveBlocked({ status, reason }: { status: StreamStatus; reason: "module" | "misplaced" }) {
  if (reason === "module") {
    return (
      <Notice tone="danger" icon={Warning} title="No stream can pass nginx’s test yet">
        <p>
          nginx.conf has a stream block this nginx cannot read, so its configuration test fails for
          every file. {moduleRemedy(status.module)} Or take the stream block out of nginx.conf.
        </p>
      </Notice>
    )
  }
  const outage = streamOutage(status) !== null
  return (
    <Notice
      tone={outage ? "danger" : "warning"}
      icon={Warning}
      title="No stream can pass nginx’s test yet"
    >
      <div className="space-y-2">
        <p>
          nginx.conf includes this directory {includedPlace(status.includedIn ?? "")}, where its
          configuration test refuses a stream
          {outage ? " — and with files there, it refuses every reload on this host" : ""}.{" "}
          {moduleMissing(status.module)
            ? `Take that include out. ${moduleRemedy(status.module)} Then add this stream block at the top level, beside the http block:`
            : "Move the include into a top-level stream block, beside the http block:"}
        </p>
        <Well className="whitespace-pre">{status.snippet}</Well>
      </div>
    </Notice>
  )
}

const BLANK: StreamSpec = {
  name: "",
  listen: 0,
  protocol: "tcp",
  upstream: "",
  proxyProtocol: false,
  allowFrom: [],
}

type Preview = { content: string; warnings: string[] }

const DURATION_ERROR = "Write it as 90s, 10m or 1h30m, up to 24h."

function StreamForm({
  open,
  stream,
  status,
  onOpenChange,
  onSaved,
  onRaw,
}: {
  open: boolean
  /** The stream this form opened on; null for a new one. */
  stream: StreamEntry | null
  /** Whether nginx reads the directory and can: everything this form says about taking effect hangs on it. */
  status: StreamStatus
  onOpenChange: (open: boolean) => void
  onSaved: () => void
  /** Opens the stream's file in the raw editor instead. */
  onRaw: (stream: StreamEntry) => void
}) {
  const key = `proxy.stream.form.${stream?.name ?? "new"}`
  const [spec, setSpec] = useSessionState<StreamSpec>(
    `${key}.spec`,
    stream ? streamSpecOf(stream) : BLANK,
  )
  const [allow, setAllow] = useSessionState(`${key}.allow`, (stream?.allowFrom ?? []).join(", "))
  // The timeouts are kept as typed — "10m" — and read on the way out.
  const [idle, setIdle] = useSessionState(`${key}.idle`, formatDuration(stream?.timeout))
  const [connect, setConnect] = useSessionState(
    `${key}.connect`,
    formatDuration(stream?.connectTimeout),
  )
  const [preview, setPreview] = useState<Preview | null>(null)
  const [previewError, setPreviewError] = useState("")
  const [refused, setRefused] = useState<{ field: string; message: string } | null>(null)
  const [busy, setBusy] = useState(false)

  const live = streamsLive(status)
  const blocked = saveBlocked(status)
  const idleSeconds = parseDuration(idle)
  const connectSeconds = parseDuration(connect)
  const timed = idleSeconds !== null && connectSeconds !== null
  const body = streamBody(
    { ...spec, timeout: idleSeconds || undefined, connectTimeout: connectSeconds || undefined },
    allow,
  )
  // A file the form cannot say everything about is not saved over: the
  // form would drop what it cannot show — a deny rule, a second server.
  const locked = Boolean(stream && (stream.error || stream.unsupported.length > 0))
  const readOnly = locked || blocked !== null
  const renaming = stream !== null && spec.name !== stream.name
  const service = DANGEROUS_PORTS[spec.listen]
  const family = listenFamily(spec.address)
  const edit = (change: Partial<StreamSpec>, field?: string) => {
    setSpec((s) => ({ ...s, ...change }))
    if (field && refused?.field === field) setRefused(null)
  }

  useEffect(() => {
    if (!open || readOnly) return
    const controller = new AbortController()
    const ready = spec.name !== "" && spec.listen > 0 && spec.upstream !== "" && timed
    const timer = setTimeout(
      () => {
        if (!ready) {
          setPreview(null)
          setPreviewError("")
          return
        }
        post<Preview>("/proxy/streams/preview", { spec: body }, { signal: controller.signal })
          .then((r) => {
            setPreview(r)
            setPreviewError("")
          })
          .catch((err) => {
            if (controller.signal.aborted) return
            setPreview(null)
            setPreviewError(errorMessage(err))
          })
      },
      ready ? 400 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, readOnly, spec, allow, idle, connect])

  const save = async () => {
    setBusy(true)
    setRefused(null)
    try {
      // previous names the file this form opened on, so the server can tell
      // a rename from a new stream that happens to reuse a taken name — the
      // old form sent "overwrite" with the new name, and renaming bastion to
      // postgres-replica replaced postgres-replica's file in silence.
      const res = await post<StreamResult>("/proxy/streams/", {
        spec: body,
        previous: stream?.name ?? "",
        reload: live,
      })
      const title = res.renamed ? `${res.renamed} renamed to ${res.name}` : res.name
      const kept = res.renamed ? ` ${res.renamed}.conf is kept as ${res.renamed}.conf.bak.` : ""
      if (res.reloadError) {
        notify.warning(`${title} saved, reload failed`, {
          description: `The file passed nginx's test and is on disk, but nginx did not reload, so it is not forwarding yet: ${res.reloadError}`,
        })
      } else if (live) {
        notify.success(`${title} saved and reloaded`, {
          description: [...res.warnings, kept.trim()].filter(Boolean).join(" ") || undefined,
        })
      } else {
        notify.warning(`${title} saved, not yet live`, {
          description: `nginx does not read the stream directory yet, so its test could not check this file.${kept}`,
        })
      }
      onSaved()
      onOpenChange(false)
    } catch (err) {
      if (err instanceof ApiError && err.field) {
        setRefused({ field: err.field, message: err.message })
      }
      notify.error("Not saved", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      width="lg"
      title={
        <>
          <ProductLogo id={portProduct(spec.listen)} size="sm" fallback={Connection} />
          {stream ? `Edit ${stream.name}` : "New stream"}
        </>
      }
      description="A port on this host, forwarded somewhere else"
      bodyClassName="flex min-h-0 flex-1 flex-col gap-4 p-4"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {locked
              ? "This file is changed by hand."
              : blocked === "module"
                ? "nginx’s test fails until it has the stream module."
                : blocked === "misplaced"
                  ? `nginx reads this directory ${includedPlace(status.includedIn ?? "")}, where its test refuses a stream.`
                  : live
                    ? "Tested with nginx’s own parser before it takes effect."
                    : "nginx does not read this directory yet, so its test cannot check this file."}
          </span>
          <Button
            size="sm"
            onClick={save}
            disabled={readOnly || busy || !spec.name || !spec.listen || !spec.upstream || !timed}
            pending={busy}
          >
            {live ? "Save and reload" : "Save for later"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {stream && locked && (
          <Notice tone="warning" icon={Warning} title="Written by hand">
            <div className="space-y-2">
              <p>
                {stream.error
                  ? `The file could not be read: ${stream.error}.`
                  : `This file uses ${stream.unsupported.join(", ")}, which this form cannot keep, so it is shown read-only.`}
                {stream.link && !stream.error && ` It links to ${stream.link}.`}
              </p>
              {!stream.error && (
                <Button size="sm" variant="outline" onClick={() => onRaw(stream)}>
                  <Code className="size-4" />
                  Edit the file
                </Button>
              )}
            </div>
          </Notice>
        )}
        {blocked && <SaveBlocked status={status} reason={blocked} />}
        {!live && !readOnly && <NotLive status={status} />}
        {stream && !stream.managed && !readOnly && (
          <FormNote>
            Written by hand. Saving rewrites it in the dashboard&rsquo;s layout and keeps the
            original as <code className="font-mono">{stream.name}.conf.bak</code>.
          </FormNote>
        )}

        <fieldset disabled={readOnly} className="min-w-0 space-y-4">
          <Field
            label="Name"
            htmlFor="stream-name"
            hint={
              renaming
                ? `Saving renames ${stream?.name}.conf to ${spec.name || "…"}.conf and keeps the old file as ${stream?.name}.conf.bak.`
                : "Names the file. Lowercase letters, digits, dots, dashes and underscores."
            }
            error={refused?.field === "spec.name" && refused.message}
          >
            <Input
              id="stream-name"
              value={spec.name}
              onChange={(e) => edit({ name: e.target.value }, "spec.name")}
              placeholder="postgres-replica"
              className="font-mono text-xs"
            />
          </Field>

          <FieldRow>
            <Field
              label="Listen on"
              htmlFor="stream-listen"
              hint={
                family
                  ? `Every ${family} address, as the file has it — it has no ${family === "IPv4" ? "IPv6" : "IPv4"} listen.`
                  : spec.address
                    ? `Only on ${spec.address}, as the file has it.`
                    : service
                      ? `${service}'s usual port — restrict who may connect.`
                      : "The port on this host."
              }
              error={
                refused?.field === "spec.listen" && (
                  <>
                    {refused.message}.{" "}
                    <Link href="/proxy/ports" className="underline underline-offset-2">
                      See who holds it
                    </Link>
                  </>
                )
              }
            >
              <Input
                id="stream-listen"
                value={spec.listen || ""}
                inputMode="numeric"
                onChange={(e) => edit({ listen: Number(e.target.value) || 0 }, "spec.listen")}
                placeholder="5432"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Protocol">
              <ToggleGroup
                type="single"
                value={spec.protocol}
                onValueChange={(v) =>
                  v && edit({ protocol: v as StreamSpec["protocol"] }, "spec.listen")
                }
                variant="outline"
                size="sm"
                className="w-full"
              >
                <ToggleGroupItem value="tcp" className="flex-1 text-hint">
                  TCP
                </ToggleGroupItem>
                <ToggleGroupItem value="udp" className="flex-1 text-hint">
                  UDP
                </ToggleGroupItem>
                <ToggleGroupItem value="both" className="flex-1 text-hint">
                  TCP+UDP
                </ToggleGroupItem>
              </ToggleGroup>
            </Field>
          </FieldRow>

          {spec.protocol !== "tcp" && (
            <Field
              label="UDP sessions"
              hint={
                (spec.udpMode === "request"
                  ? "Each reply ends the session: one question, one answer, as DNS works."
                  : "One session per client until it goes quiet, so a game server, WireGuard or VoIP sees one peer.") +
                (spec.protocol === "both" ? " TCP connections are not affected." : "")
              }
            >
              <ToggleGroup
                type="single"
                value={spec.udpMode ?? "session"}
                onValueChange={(v) => v && edit({ udpMode: v as StreamSpec["udpMode"] })}
                variant="outline"
                size="sm"
                className="w-full"
              >
                <ToggleGroupItem value="session" className="flex-1 text-hint">
                  Long-lived
                </ToggleGroupItem>
                <ToggleGroupItem value="request" className="flex-1 text-hint">
                  One reply
                </ToggleGroupItem>
              </ToggleGroup>
            </Field>
          )}

          <Field
            label="Forward to"
            htmlFor="stream-upstream"
            hint="host:port of the service behind it, or unix:/path for a local socket."
          >
            <Input
              id="stream-upstream"
              value={spec.upstream}
              onChange={(e) => edit({ upstream: e.target.value })}
              placeholder="10.0.0.5:5432"
              className="font-mono text-xs"
            />
          </Field>

          <Field
            label="Allow only these"
            htmlFor="stream-allow"
            hint="A stream has no authentication of any kind — anything that reaches this port is through to the backend. Leave this empty only when the service behind it authenticates for itself."
          >
            <Input
              id="stream-allow"
              value={allow}
              onChange={(e) => setAllow(e.target.value)}
              placeholder="10.0.0.0/8, 203.0.113.9"
              className="font-mono text-xs"
            />
          </Field>

          <OptionList>
            <OptionRow
              title="Send the PROXY header"
              hint="Lets the backend see the real client address. It has to be expecting the header, or it reads it as the first bytes of the connection and fails in a way that looks like a protocol mismatch."
              checked={spec.proxyProtocol}
              onCheckedChange={(v) => edit({ proxyProtocol: v })}
            />
          </OptionList>

          <FieldRow>
            <Field
              label="Idle timeout"
              htmlFor="stream-timeout"
              hint="Silence before nginx closes the connection, as 90s, 10m or 1h. Empty is 10m."
              error={idleSeconds === null && DURATION_ERROR}
            >
              <Input
                id="stream-timeout"
                value={idle}
                onChange={(e) => setIdle(e.target.value)}
                placeholder="10m"
                aria-invalid={idleSeconds === null || undefined}
                className="font-mono text-xs"
              />
            </Field>
            <Field
              label="Connect timeout"
              htmlFor="stream-connect-timeout"
              hint="How long to wait for the backend to accept. Empty is 60s."
              error={connectSeconds === null && DURATION_ERROR}
            >
              <Input
                id="stream-connect-timeout"
                value={connect}
                onChange={(e) => setConnect(e.target.value)}
                placeholder="60s"
                aria-invalid={connectSeconds === null || undefined}
                className="font-mono text-xs"
              />
            </Field>
          </FieldRow>
        </fieldset>

        {preview && preview.warnings.length > 0 && (
          <div className="space-y-1" aria-live="polite">
            {preview.warnings.map((warning) => (
              <FormNote key={warning} tone="warning">
                {warning}
              </FormNote>
            ))}
          </div>
        )}
      </div>

      {!readOnly && (
        <Pane className="min-h-48 flex-1">
          {previewError ? (
            <EmptyNote className="my-auto text-destructive">{previewError}</EmptyNote>
          ) : preview ? (
            <CodeEditor className="h-full" language="ini" value={preview.content} readOnly />
          ) : (
            <EmptyNote className="my-auto">
              {timed
                ? "Fill in a name, a port and an upstream, and the nginx appears here."
                : "Correct the timeout, and the nginx appears here."}
            </EmptyNote>
          )}
        </Pane>
      )}
    </SidePanel>
  )
}
