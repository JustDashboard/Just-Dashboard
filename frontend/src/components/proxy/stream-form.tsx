"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { Code, Connection, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { useSessionState } from "@/lib/view-state"
import type {
  Container,
  PortConflict,
  StreamAccess,
  StreamEntry,
  StreamPreview,
  StreamResult,
  StreamSpec,
  StreamStatus,
} from "@/lib/types"
import {
  accessError,
  accessOf,
  containerTargets,
  durationError,
  formatDuration,
  includedPlace,
  listensOn,
  moduleMissing,
  moduleRemedy,
  parseDuration,
  protocolLabel,
  saveBlocked,
  saveOutcome,
  streamBody,
  streamOutage,
  streamSpecOf,
  streamsLive,
} from "@/lib/streams"
import { STREAM_PRESETS, applyPreset } from "@/lib/stream-presets"
import { CodeEditor } from "@/components/code-editor"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Pane, Well } from "@/components/panel"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Segments } from "@/components/deploy/settings/segments"
import { SettingForm, SettingSection } from "@/components/deploy/settings/setting-card"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { AccessRules } from "@/components/proxy/access-rules"
import { StreamRoutes } from "@/components/proxy/stream-routes"
import { StreamServers } from "@/components/proxy/stream-servers"
import { StreamListen } from "@/components/proxy/stream-listen"
import { StreamTLS } from "@/components/proxy/stream-tls"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/**
 * A stream's fields, in two places: the sheet a new stream is made in, and a
 * stream's own Settings tab, where the same fields are a settings column with
 * the page's one floating save bar. Both share the draft (`useStreamDraft`),
 * which outlives a visit to another tab, and the checks a save would make:
 * the preview asks the server for the nginx it would write and whether
 * something holds the port, and says so on the field before Save is pressed.
 */

/** A count or a rate as typed: a whole number above zero, or unset. */
function positive(text: string): number | undefined {
  const n = Number(text)
  return Number.isInteger(n) && n > 0 ? n : undefined
}

export const BLANK: StreamSpec = {
  name: "",
  listen: 0,
  protocol: "tcp",
  upstream: "",
  proxyProtocol: false,
  allowFrom: [],
  // On for a new stream: an open port nobody can see the use of is the one
  // an operator cannot judge.
  logConnections: true,
}

/** What a draft started from, to tell an edit from the stream as it is. */
function baseline(stream: StreamEntry | null, draft: StreamSpec | null) {
  const from = stream ? streamSpecOf(stream) : (draft ?? BLANK)
  return {
    spec: from,
    access: accessOf(stream ?? draft ?? BLANK),
    idle: formatDuration((stream ?? draft)?.timeout),
    connect: formatDuration((stream ?? draft)?.connectTimeout),
  }
}

export type StreamDraft = ReturnType<typeof useStreamDraft>

/**
 * One stream's form state, kept for the tab under `proxy.stream.form.<name>`
 * (or `.new`), so a reload or a visit to another tab finds it as it was left.
 */
export function useStreamDraft({
  stream,
  draft,
  status,
  open,
}: {
  /** The stream the form opened on; null for a new one. */
  stream: StreamEntry | null
  /** A new stream's starting fields: a duplicate's, or a link's. */
  draft: StreamSpec | null
  status: StreamStatus
  /** Whether the form is on screen: the preview and the container list are read only then. */
  open: boolean
}) {
  const key = `proxy.stream.form.${stream?.name ?? "new"}`
  const start = useMemo(() => baseline(stream, draft), [stream, draft])
  const [spec, setSpec] = useSessionState<StreamSpec>(`${key}.spec`, start.spec)
  const [access, setAccess] = useSessionState<StreamAccess>(`${key}.access`, start.access)
  // The timeouts are kept as typed — "10m" — and read on the way out.
  const [idle, setIdle] = useSessionState(`${key}.idle`, start.idle)
  const [connect, setConnect] = useSessionState(`${key}.connect`, start.connect)
  const [preset, setPreset] = useSessionState(`${key}.preset`, "")
  const [preview, setPreview] = useState<StreamPreview | null>(null)
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
    access,
  )
  const accessProblem = accessError(access)
  // A file the form cannot say everything about is not saved over: the
  // form would drop what it cannot show — a max_conns, a reuseport listener.
  const locked = Boolean(stream && (stream.error || stream.unsupported.length > 0))
  const readOnly = locked || blocked !== null
  const edit = (change: Partial<StreamSpec>, field?: string) => {
    setSpec((s) => ({ ...s, ...change }))
    if (field && refused?.field === field) setRefused(null)
  }
  // The preview asks what the save would: a port something holds is said on
  // the field before the save is pressed, with the next one free. It is the
  // preview of this very spec only while nothing has been typed since.
  const conflict =
    preview?.conflict && listensOn(spec, preview.conflict.port) ? preview.conflict : undefined

  // Running containers' published ports, as places to forward to. Read once
  // per opening; without Docker, or without the right to list it, the
  // picker is simply not there.
  const [containers, setContainers] = useState<Container[]>([])
  useEffect(() => {
    if (!open || readOnly) return
    const controller = new AbortController()
    get<Container[]>("/docker/containers/", undefined, controller.signal)
      .then(setContainers)
      .catch(() => setContainers([]))
    return () => controller.abort()
  }, [open, readOnly])

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
        // previous is the stream this form opened on, whose own port is no
        // conflict.
        post<StreamPreview>(
          "/proxy/streams/preview",
          { spec: body, previous: stream?.name ?? "" },
          { signal: controller.signal },
        )
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
  }, [open, readOnly, spec, access, idle, connect])

  const incomplete =
    !spec.name ||
    !spec.listen ||
    spec.listenEnd === 0 ||
    !spec.upstream ||
    (spec.acceptProxy && !spec.trustedProxies?.some((entry) => entry.trim())) ||
    spec.servers?.some((server) => !server.address.trim()) ||
    spec.routes?.some((route) => !route.name.trim() || !route.upstream.trim()) ||
    !timed ||
    accessProblem !== "" ||
    Boolean(spec.tls && (!spec.certPath || !spec.keyPath))

  // What differs from the stream as it is, field by field, for the save bar.
  // A timeout typed in another way ("600s" for "10m") is no change; one that
  // does not parse yet is.
  const original = streamBody(
    {
      ...start.spec,
      timeout: parseDuration(start.idle) || undefined,
      connectTimeout: parseDuration(start.connect) || undefined,
    },
    start.access,
  )
  const keys = new Set([...Object.keys(original), ...Object.keys(body)]) as Set<keyof StreamSpec>
  let changes = timed ? 0 : 1
  for (const k of keys) if (JSON.stringify(original[k]) !== JSON.stringify(body[k])) changes++

  /**
   * Saves the draft: the toast says how far it got, a refusal for one field
   * lands on that field. Resolves with what the server did, or undefined.
   */
  const save = async (): Promise<StreamResult | undefined> => {
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
      return res
    } catch (err) {
      if (err instanceof ApiError && err.field) {
        setRefused({ field: err.field, message: err.message })
      }
      notify.error("Not saved", err)
      return undefined
    } finally {
      setBusy(false)
    }
  }

  const reset = () => {
    setSpec(start.spec)
    setAccess(start.access)
    setIdle(start.idle)
    setConnect(start.connect)
    setRefused(null)
  }

  const pickPreset = (id: string) => {
    const chosen = STREAM_PRESETS.find((p) => p.id === id)
    if (!chosen) return
    const next = applyPreset(chosen, spec, access)
    setPreset(id)
    setSpec(next.spec)
    setAccess(next.access)
    if (refused?.field === "spec.listen" || refused?.field === "spec.name") setRefused(null)
  }

  return {
    stream,
    status,
    spec,
    access,
    setAccess,
    idle,
    setIdle,
    connect,
    setConnect,
    idleSeconds,
    connectSeconds,
    timed,
    preset,
    pickPreset,
    preview,
    previewError,
    refused,
    conflict,
    edit,
    body,
    accessProblem,
    locked,
    readOnly,
    live,
    blocked,
    incomplete,
    changes,
    busy,
    save,
    reset,
    targets: containerTargets(containers, spec.protocol),
  }
}

/** The toast a save ends with, the same from the sheet and from Settings. */
function announce(res: StreamResult, live: boolean, onRecheck: () => void) {
  const outcome = saveOutcome(res, live)
  notify[outcome.tone](outcome.title, {
    description: outcome.description,
    action: outcome.recheck ? { label: "Re-check", onClick: onRecheck } : undefined,
  })
}

/**
 * A port a save is refused for, on the Listen field: the refusal, where to
 * see what holds it — the site, or the list of listening ports; a stream is
 * on the Streams page — and the next port free as one press.
 */
function PortTaken({
  message,
  conflict,
  onUse,
}: {
  message: string
  conflict?: PortConflict
  onUse: (port: number) => void
}) {
  const suggest = conflict?.suggest
  return (
    <>
      {message}.{" "}
      {conflict?.kind === "site" && conflict.site ? (
        <Link
          href={`/proxy/sites/${encodeURIComponent(conflict.site)}`}
          className="underline underline-offset-2"
        >
          Open the site
        </Link>
      ) : conflict?.kind === "stream" ? null : (
        <Link href="/proxy/ports" className="underline underline-offset-2">
          See who holds it
        </Link>
      )}
      {suggest ? (
        <>
          {" "}
          <button
            type="button"
            onClick={() => onUse(suggest)}
            className="rounded-sm underline underline-offset-2 focus-ring"
          >
            Use {suggest}
          </button>
        </>
      ) : null}
    </>
  )
}

/**
 * The form's reminder, at the point of commit, of what keeps a saved stream
 * from forwarding — and where the fix is: the Streams page installs the
 * module and connects the directory.
 */
function NotLive({ status }: { status: StreamStatus }) {
  const missing = moduleMissing(status.module)
  return (
    <Notice tone="warning" icon={Warning} title="This will not forward anything yet">
      <p>
        {missing
          ? `nginx has no stream module, so nothing can read what you save here. ${moduleRemedy(status.module)} Then connect this directory on the Streams page; this stream forwards from the reload that follows.`
          : "nginx.conf does not include this directory yet, so what you save here is written and ignored until it is connected on the Streams page. It forwards from the reload that follows."}
      </p>
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

/** What stands above the fields: why the form is read-only, or why a save will not forward yet. */
function StreamNotices({ draft, onRaw }: { draft: StreamDraft; onRaw: () => void }) {
  const { stream, status, locked, blocked, live, readOnly } = draft
  return (
    <>
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
              <Button size="sm" variant="outline" onClick={onRaw}>
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
          Written by hand. Saving rewrites it in the dashboard&rsquo;s layout and keeps the original
          as <code className="font-mono">{stream.name}.conf.bak</code>.
        </FormNote>
      )}
    </>
  )
}

/**
 * The fields, grouped: `section` wraps each group — the Settings tab's
 * `SettingSection`s, or nothing in the sheet, where the groups run on as one
 * form under the sheet's own title.
 */
function StreamFields({
  draft,
  section,
}: {
  draft: StreamDraft
  section: (title: string, state: React.ReactNode, children: React.ReactNode) => React.ReactNode
}) {
  const { stream, status, spec, access, refused, conflict, edit, targets } = draft
  const renaming = stream !== null && spec.name !== stream.name
  const service = DANGEROUS_PORTS[spec.listen]
  const presetPort = STREAM_PRESETS.find((p) => p.id === draft.preset)?.port
  const target = targets.find((t) => t.upstream === spec.upstream)
  // A range moves as a whole: the suggestion is the first port of one as wide.
  const takePort = (port: number) =>
    edit(
      { listen: port, listenEnd: spec.listenEnd && port + (spec.listenEnd - spec.listen) },
      "spec.listen",
    )

  const listen = (
    <>
      {!stream && (
        <Field
          label="Start from"
          info="Sets the port, protocol, UDP mode and a starting allow list for a common service."
          hint={STREAM_PRESETS.find((p) => p.id === draft.preset)?.hint}
        >
          <Select value={draft.preset} onValueChange={draft.pickPreset}>
            <SelectTrigger className="w-full" aria-label="Service preset">
              <SelectValue placeholder="A common service" />
            </SelectTrigger>
            <SelectContent>
              {STREAM_PRESETS.map((p) => (
                <SelectItem
                  key={p.id}
                  value={p.id}
                  hint={`${protocolLabel(p.protocol)} ${p.listen ?? p.port}${p.restricted ? " · private networks" : ""}`}
                >
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
      <Field
        label="Name"
        htmlFor="stream-name"
        info="Names the file. Lowercase letters, digits, dots, dashes and underscores."
        hint={
          renaming
            ? `Saving renames ${stream?.name}.conf to ${spec.name || "…"}.conf and keeps the old file as ${stream?.name}.conf.bak.`
            : undefined
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
          info={
            spec.listenEnd !== undefined ? "The first port of the range." : "The port on this host."
          }
          hint={service ? `${service}'s usual port — restrict who may connect.` : undefined}
          error={
            refused?.field === "spec.listen" ? (
              <PortTaken message={refused.message} conflict={conflict} onUse={takePort} />
            ) : (
              conflict && (
                <PortTaken message={conflict.message} conflict={conflict} onUse={takePort} />
              )
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
          <Segments
            label="Protocol"
            fill
            value={spec.protocol}
            onChange={(v) => edit({ protocol: v }, "spec.listen")}
            options={[
              { value: "tcp", label: "TCP" },
              { value: "udp", label: "UDP" },
              { value: "both", label: "TCP+UDP" },
            ]}
          />
        </Field>
      </FieldRow>

      <StreamListen
        spec={spec}
        addresses={status.addresses ?? []}
        module={status.module}
        error={refused?.field === "spec.address" ? refused.message : undefined}
        onChange={edit}
      />

      {spec.protocol !== "tcp" && (
        <Field
          label="UDP sessions"
          info={
            (spec.udpMode === "request"
              ? "Each reply ends the session: one question, one answer, as DNS works."
              : "One session per client until it goes quiet, so a game server, WireGuard or VoIP sees one peer.") +
            (spec.protocol === "both" ? " TCP connections are not affected." : "")
          }
        >
          <Segments
            label="UDP sessions"
            fill
            value={spec.udpMode ?? "session"}
            onChange={(v) => edit({ udpMode: v })}
            options={[
              { value: "session", label: "Long-lived" },
              { value: "request", label: "One reply" },
            ]}
          />
        </Field>
      )}
    </>
  )

  const forward = (
    <>
      <StreamServers
        pool={spec}
        onChange={(pool) => edit(pool)}
        hint={
          spec.routes
            ? "The default: host:port or unix:/path for any TLS name no route below matches."
            : spec.samePort
              ? "The backend's IP address alone: each port of the range goes to the same port there."
              : "host:port of the service behind it, or unix:/path for a local socket."
        }
        warning={
          !spec.routes && !spec.samePort && target?.exposed
            ? `${target.container} publishes this port on every address, so it is reachable around the stream, where these rules do not apply. Publish it on 127.0.0.1 instead.`
            : undefined
        }
        placeholder={spec.samePort ? "10.0.0.5" : `10.0.0.5:${presetPort ?? 5432}`}
        picker={
          targets.length > 0 && (
            <Select
              value={target?.key ?? ""}
              onValueChange={(k) => {
                const chosen = targets.find((t) => t.key === k)
                if (chosen)
                  edit({
                    upstream: chosen.upstream,
                    servers: spec.servers && [
                      { ...spec.servers[0], address: chosen.upstream },
                      ...spec.servers.slice(1),
                    ],
                  })
              }}
            >
              <SelectTrigger className="w-full sm:w-44" aria-label="Forward to a container">
                <SelectValue placeholder="A container" />
              </SelectTrigger>
              <SelectContent>
                {targets.map((t) => (
                  <SelectItem
                    key={t.key}
                    value={t.key}
                    hint={`${t.upstream}/${t.protocol}${t.exposed ? " · published on every address" : ""}`}
                  >
                    {t.container}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )
        }
      />
      <StreamRoutes spec={spec} module={status.module} onChange={edit} />
      <StreamTLS spec={spec} module={status.module} open onChange={edit} />
    </>
  )

  const who = <AccessRules access={access} error={draft.accessProblem} onChange={draft.setAccess} />

  const connection = (
    <>
      <OptionList>
        <OptionRow
          title="Send the PROXY header"
          hint="Lets the backend see the real client address. It has to be expecting the header, or it reads it as the first bytes of the connection and fails in a way that looks like a protocol mismatch."
          checked={spec.proxyProtocol}
          onCheckedChange={(v) => edit({ proxyProtocol: v })}
        />
        <OptionRow
          title="Log connections"
          hint={`nginx writes one line per session — client, outcome, bytes, length — to /var/log/nginx/stream-${spec.name || "<name>"}.log, which the stream's page reads.`}
          checked={Boolean(spec.logConnections)}
          onCheckedChange={(v) => edit({ logConnections: v })}
        />
      </OptionList>

      <FieldRow>
        <Field
          label="Idle timeout"
          htmlFor="stream-timeout"
          info="Silence before nginx closes the connection, as 90s, 10m or 1h. Empty is 10m."
          error={draft.idleSeconds === null && durationError(draft.idle, "10m")}
        >
          <Input
            id="stream-timeout"
            value={draft.idle}
            onChange={(e) => draft.setIdle(e.target.value)}
            placeholder="10m"
            aria-invalid={draft.idleSeconds === null || undefined}
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Connect timeout"
          htmlFor="stream-connect-timeout"
          info="How long to wait for the backend to accept. Empty is 60s."
          error={draft.connectSeconds === null && durationError(draft.connect, "60s")}
        >
          <Input
            id="stream-connect-timeout"
            value={draft.connect}
            onChange={(e) => draft.setConnect(e.target.value)}
            placeholder="60s"
            aria-invalid={draft.connectSeconds === null || undefined}
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>

      <FieldRow>
        <Field
          label="Connections per client"
          htmlFor="stream-conn-ip"
          info="Open at once from one address; a UDP session counts as one. Empty is no cap."
        >
          <Input
            id="stream-conn-ip"
            value={spec.maxConnPerIp ?? ""}
            inputMode="numeric"
            onChange={(e) => edit({ maxConnPerIp: positive(e.target.value) })}
            placeholder="No cap"
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Connections in total"
          htmlFor="stream-conn-total"
          info="nginx closes one over either cap as soon as it is accepted."
          hint={
            spec.listenEnd !== undefined
              ? "Not with a port range: nginx would count each port on its own."
              : undefined
          }
        >
          <Input
            id="stream-conn-total"
            disabled={spec.listenEnd !== undefined}
            value={spec.maxConnTotal ?? ""}
            inputMode="numeric"
            onChange={(e) => edit({ maxConnTotal: positive(e.target.value) })}
            placeholder="No cap"
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>

      <FieldRow>
        <Field
          label="Upload rate"
          htmlFor="stream-upload-rate"
          info="KiB/s from the client, per connection: four connections get four times it."
        >
          <Input
            id="stream-upload-rate"
            value={spec.uploadRate ?? ""}
            inputMode="numeric"
            onChange={(e) => edit({ uploadRate: positive(e.target.value) })}
            placeholder="No limit"
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Download rate"
          htmlFor="stream-download-rate"
          info="KiB/s to the client, per connection. Empty is no limit."
        >
          <Input
            id="stream-download-rate"
            value={spec.downloadRate ?? ""}
            inputMode="numeric"
            onChange={(e) => edit({ downloadRate: positive(e.target.value) })}
            placeholder="No limit"
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>
    </>
  )

  const listenState = `${protocolLabel(spec.protocol)} ${spec.listen || "—"}${spec.listenEnd ? `-${spec.listenEnd}` : ""}`
  const forwardState = (
    <span className="font-mono">
      {spec.servers ? `${spec.servers.length} servers` : spec.upstream || "nowhere yet"}
    </span>
  )
  const whoState =
    access.rules.length === 0
      ? access.defaultAllow
        ? "anyone"
        : "no one"
      : `${access.rules.length} ${access.rules.length === 1 ? "rule" : "rules"}`
  return (
    <>
      {[
        section("Listen", listenState, listen),
        section("Destination", forwardState, forward),
        section("Access", whoState, who),
        section("Connection", undefined, connection),
      ].map((part, i) => (
        <Fragment key={i}>{part}</Fragment>
      ))}
    </>
  )
}

/** The nginx a save would write, or why there is none to show yet. */
function StreamPreviewPane({ draft, className }: { draft: StreamDraft; className?: string }) {
  return (
    <Pane className={className}>
      {draft.previewError ? (
        <EmptyNote className="my-auto text-destructive">{draft.previewError}</EmptyNote>
      ) : draft.preview ? (
        <CodeEditor className="h-full" language="ini" value={draft.preview.content} readOnly />
      ) : (
        <EmptyNote className="my-auto">
          {draft.timed
            ? "Fill in a name, a port and an upstream, and the nginx appears here."
            : "Correct the timeout, and the nginx appears here."}
        </EmptyNote>
      )}
    </Pane>
  )
}

function PreviewWarnings({ draft }: { draft: StreamDraft }) {
  if (!draft.preview || draft.preview.warnings.length === 0) return null
  return (
    <div className="space-y-1" aria-live="polite">
      {draft.preview.warnings.map((warning) => (
        <FormNote key={warning} tone="warning">
          {warning}
        </FormNote>
      ))}
    </div>
  )
}

/** What a save will do, or why it cannot: the sheet's footer and the Settings column's foot. */
function saveWords(draft: StreamDraft) {
  if (draft.blocked === "module") return "nginx’s test fails until it has the stream module."
  if (draft.blocked === "misplaced")
    return `nginx reads this directory ${includedPlace(draft.status.includedIn ?? "")}, where its test refuses a stream.`
  return draft.live
    ? "Tested with nginx’s own parser before it takes effect."
    : "nginx does not read this directory yet, so its test cannot check this file."
}

/**
 * A new stream, in a sheet: the one add form the section keeps as one. A
 * stream's own fields are its page's Settings tab (`StreamSettings`).
 */
export function StreamForm({
  open,
  draft: start,
  status,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  /** A new stream's starting fields: a duplicate's, or a link's. */
  draft: StreamSpec | null
  status: StreamStatus
  onOpenChange: (open: boolean) => void
  /** Reads the listing again; with the saved stream's name, once it was saved. */
  onSaved: (name?: string) => void
}) {
  const draft = useStreamDraft({ stream: null, draft: start, status, open })
  const save = async () => {
    const res = await draft.save()
    if (!res) return
    announce(res, draft.live, () => onSaved())
    onSaved(res.name)
    onOpenChange(false)
  }
  return (
    <SidePanel
      open={open}
      onOpenChange={(o) => !draft.busy && onOpenChange(o)}
      width="lg"
      title={
        <>
          <ProductLogo id={portProduct(draft.spec.listen)} size="sm" fallback={Connection} />
          New stream
        </>
      }
      description="A port on this host, forwarded somewhere else"
      bodyClassName="flex min-h-0 flex-1 flex-col gap-4 p-4"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">{saveWords(draft)}</span>
          <Button
            size="sm"
            onClick={() => void save()}
            disabled={draft.readOnly || draft.busy || draft.incomplete}
            pending={draft.busy}
          >
            {draft.live ? "Save and reload" : "Save for later"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <StreamNotices draft={draft} onRaw={() => {}} />
        <fieldset disabled={draft.readOnly} className="min-w-0 space-y-4">
          <StreamFields draft={draft} section={(_, __, children) => children} />
        </fieldset>
        <PreviewWarnings draft={draft} />
      </div>
      {!draft.readOnly && <StreamPreviewPane draft={draft} className="min-h-48 flex-1" />}
    </SidePanel>
  )
}

/**
 * A stream's own Settings: the same fields as one centred column of
 * sections, the nginx a save would write at its foot, and the page's one
 * floating save bar, which counts the edits and names what Save does — a
 * reload while nginx reads the directory, a file kept for later while it
 * does not. A file the form cannot say everything about is shown read-only
 * with the file itself to hand.
 */
export function StreamSettings({
  stream,
  status,
  onSaved,
  onRaw,
}: {
  stream: StreamEntry
  status: StreamStatus
  /** The save went through: the listing is read again, and a rename follows the stream. */
  onSaved: (res: StreamResult) => void
  onRaw: () => void
}) {
  const draft = useStreamDraft({ stream, draft: null, status, open: true })
  const dirty = draft.changes > 0
  const onSave = async () => {
    const res = await draft.save()
    if (!res) return false
    announce(res, draft.live, () => onSaved(res))
    onSaved(res)
    return true
  }
  return (
    <SaveBarProvider>
      <div data-slot="stream-settings" className="mx-auto w-full max-w-3xl min-w-0 space-y-8 pb-20">
        <div className="space-y-4 empty:hidden">
          <StreamNotices draft={draft} onRaw={onRaw} />
        </div>
        <SettingForm
          name="Stream settings"
          canEdit={!draft.readOnly}
          dirty={dirty}
          changes={draft.changes}
          saving={draft.busy}
          invalid={draft.incomplete}
          applies="immediately"
          onSave={onSave}
          onDiscard={draft.reset}
          // A file the form cannot keep says so above it, with the file to hand.
          note={draft.locked ? undefined : saveWords(draft)}
        >
          <fieldset disabled={draft.readOnly} className="contents">
            <StreamFields
              draft={draft}
              section={(title, state, children) => (
                <SettingSection title={title} state={state}>
                  <div className="space-y-4">{children}</div>
                </SettingSection>
              )}
            />
          </fieldset>
          {!draft.readOnly && (
            <SettingSection
              title="The file"
              state={<span className="font-mono">{stream.path}</span>}
            >
              <div className="space-y-3">
                <PreviewWarnings draft={draft} />
                <StreamPreviewPane draft={draft} className="h-72" />
              </div>
            </SettingSection>
          )}
        </SettingForm>
      </div>
    </SaveBarProvider>
  )
}
