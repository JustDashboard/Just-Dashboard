"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import {
  Code,
  Connection,
  Copy,
  Lightning,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  Slash,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { ApiError, del, errorMessage, get, post } from "@/lib/api"
import type {
  Container,
  PortConflict,
  StreamDeleteResult,
  StreamEntry,
  StreamIncludeResult,
  StreamAccess,
  StreamPreview,
  StreamResult,
  StreamSpec,
  StreamStatus,
  StreamTestResult,
} from "@/lib/types"
import {
  STREAM_FILTERS,
  accessError,
  accessOf,
  blocksReloads,
  byUrgency,
  carries,
  containerTargets,
  disconnectChange,
  duplicateSpec,
  durationError,
  formatDuration,
  includedPlace,
  listenFamily,
  listenLabel,
  moduleMissing,
  moduleRemedy,
  parseDuration,
  protocolLabel,
  saveBlocked,
  saveOutcome,
  stateCounts,
  stateStatus,
  streamBody,
  streamDraftFromQuery,
  streamFilterCounts,
  streamMatchesFilter,
  streamMatchesQuery,
  streamOutage,
  streamSpecOf,
  streamTests,
  streamsLive,
  testPlace,
  testStatus,
  testSummary,
  type StreamFilter,
} from "@/lib/streams"
import { STREAM_PRESETS, applyPreset } from "@/lib/stream-presets"
import { duration } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChoiceRow } from "@/components/flow"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Pane, Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { ProductLogo, ProductLogos, portProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, VerbMenu, type Verb } from "@/components/verbs"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { StreamSetup } from "@/components/proxy/stream-setup"
import { AccessRules } from "@/components/proxy/access-rules"
import { StreamServers } from "@/components/proxy/stream-servers"
import { useProxy } from "@/components/proxy/proxy-context"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"
import { ProxyGrid, RoutePath } from "@/components/proxy/route-path"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
 * top-level stream block, or the files are written and silently ignored. The
 * page installs the one and connects the other (stream-setup.tsx), and takes
 * its own include out again from the header's menu.
 */
export function StreamsPage() {
  const { can } = useAuth()
  const proxy = useProxy()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useSessionState<StreamEntry | null>("proxy.streams.editing", null)
  const [form, setForm] = useSessionState("proxy.streams.form", { open: false, session: 0 })
  // A new stream's starting fields — a duplicate, or a link's ?new=1 — kept
  // for the tab like the form's own fields, so a reload opens the same form.
  const [draft, setDraft] = useSessionState<StreamSpec | null>("proxy.streams.draft", null)
  const [raw, setRaw] = useState<StreamEntry | null>(null)
  const [chip, setChip] = useSessionState<StreamFilter>("proxy.streams.filter", "all")
  const [query, setQuery] = useSessionState("proxy.streams.query", "")
  const searchRef = useRef<HTMLInputElement>(null)
  const params = useSearchParams()
  const { data, error, loading, refresh } = usePoll<StreamStatus>(
    (signal) => get("/proxy/streams/", undefined, signal),
    60_000,
  )
  // A re-check reads the listing again; it is over when the reading it was
  // asked from is replaced — by the next one, or by a failure.
  const reading: unknown = error ?? data
  const [checkingFrom, setCheckingFrom] = useState<unknown>(undefined)
  const checking = checkingFrom !== undefined && checkingFrom === reading
  const recheck = () => {
    setCheckingFrom(reading)
    refresh()
  }
  const admin = can("system.admin")
  const noNginx = !proxy.loading && !proxy.hasNginx

  const open = (stream: StreamEntry | null, start: StreamSpec | null = null) => {
    // A starting point replaces whatever a closed-by-reload new form left
    // behind, which would otherwise win over it.
    if (!stream) forgetSessionState("proxy.stream.form.new.")
    setEditing(stream)
    setDraft(stream ? null : start)
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
      stopping: streams.filter(blocksReloads).length,
    }
  }, [data])
  // Paused streams are listed with the rest, apart only from the counts of
  // what nginx reads.
  const listed = useMemo(() => [...(data?.streams ?? []), ...(data?.paused ?? [])], [data])
  const states = useMemo(() => stateCounts(listed), [listed])
  const chips = useMemo(() => streamFilterCounts(listed), [listed])
  const shown = useMemo(
    () =>
      listed
        .filter((stream) => streamMatchesFilter(stream, chip) && streamMatchesQuery(stream, query))
        .sort(byUrgency),
    [listed, chip, query],
  )

  const live = data ? streamsLive(data) : false
  const blocked = data ? saveBlocked(data) : null
  const outage = data ? streamOutage(data) : null
  const canCreate = admin && !noNginx && !blocked

  // ?stream=<name> opens that stream's form, and ?new=1&listen=&upstream=&protocol=
  // a new one filled in, once the first listing is here to check them
  // against. The parameters are then taken out of the address, so a reload
  // or Back does not open the form again over one closed by hand.
  const linked = useRef(false)
  useEffect(() => {
    if (!data || linked.current) return
    linked.current = true
    const name = params.get("stream")
    const start = streamDraftFromQuery(params)
    if (!name && !start) return
    if (name) {
      const stream = listed.find((s) => s.name === name)
      if (stream && admin && !stream.error && !stream.paused) {
        open(stream)
      } else {
        // Not editable here — paused, unreadable, or a read-only account:
        // the card is still what the link was about.
        setChip("all")
        setQuery(name)
      }
    } else if (start && canCreate) {
      open(null, { ...BLANK, ...start })
    }
    const url = new URL(window.location.href)
    for (const key of ["stream", "new", "listen", "upstream", "protocol", "name"]) {
      url.searchParams.delete(key)
    }
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data])

  // "/" searches and "n" starts a new stream, as on the other lists, while
  // no form or editor is open and nothing is being typed.
  const formOpen = form.open || raw !== null
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (formOpen || e.metaKey || e.ctrlKey || e.altKey) return
      const target = e.target as HTMLElement | null
      if (
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA" ||
        target?.tagName === "SELECT" ||
        target?.isContentEditable
      ) {
        return
      }
      if (e.key === "/") {
        e.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      } else if (e.key === "n" && canCreate) {
        e.preventDefault()
        open(null)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formOpen, canCreate])

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
      confirmLabel: live && !stream.paused ? "Delete and reload" : "Delete",
      description: (
        <p>
          {stream.paused
            ? "It is paused, so nginx is not reading it and nothing stops forwarding."
            : port === null
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

  // Only the include the dashboard added can be taken out from here; one
  // written by hand stays for the hand that wrote it.
  const connection = data?.connection
  const disconnect = () => {
    if (!connection || !data) return
    let result: StreamIncludeResult | undefined
    const count = data.streams.length
    confirm({
      title: "Disconnect the stream directory",
      confirmLabel: "Disconnect and reload",
      description: (
        <p>
          nginx stops reading <code className="font-mono">{data.dir}</code>
          {count > 0
            ? `, and ${count === 1 ? "its stream stops" : `its ${count} streams stop`} forwarding as soon as it reloads`
            : ""}
          . {disconnectChange(connection.mode, connection.path)} The stream files stay, and Connect
          puts it back.
        </p>
      ),
      action: async () => {
        result = await post<StreamIncludeResult>("/proxy/streams/include/remove", { reload: true })
        refresh()
      },
      onDone: () => {
        if (result?.reloadError) {
          notify.warning("nginx did not reload", {
            description: `The include is out and nginx's test passed, but the streams forward until nginx reloads: ${result.reloadError}`,
          })
        }
      },
    })
  }
  // Pausing moves the file into paused/, out of the include's reach; the
  // form cannot edit it there, and Resume puts it back under a save's checks.
  const pause = (stream: StreamEntry) => {
    let result: StreamResult | undefined
    const reads = live && stream.state !== "not-read"
    confirm({
      title: `Pause ${stream.name}`,
      confirmLabel: reads ? "Pause and reload" : "Pause",
      description: (
        <p>
          {reads
            ? `Port ${stream.listen} stops being forwarded as soon as nginx reloads.`
            : "nginx is not reading this stream, so nothing stops forwarding."}{" "}
          The file moves to <code className="font-mono">paused/</code> unchanged, and Resume puts it
          back.
        </p>
      ),
      action: async () => {
        result = await post<StreamResult>(
          `/proxy/streams/${encodeURIComponent(stream.name)}/enabled`,
          { enabled: false },
        )
        refresh()
      },
      onDone: () => {
        if (result?.reloadError) {
          notify.warning("nginx did not reload", {
            description: `Port ${stream.listen} is still forwarded until nginx reloads. ${result.reloadError}`,
          })
        }
      },
    })
  }
  const [resuming, setResuming] = useState("")
  const resume = async (stream: StreamEntry) => {
    setResuming(stream.name)
    try {
      const res = await post<StreamResult>(
        `/proxy/streams/${encodeURIComponent(stream.name)}/enabled`,
        { enabled: true },
      )
      if (res.reloadError) {
        notify.warning(`${stream.name} resumed, reload failed`, {
          description: `The file passed nginx's test and is back, but nginx did not reload, so it is not forwarding yet: ${res.reloadError}`,
        })
      } else if (res.listening === false) {
        notify.warning(`${stream.name} resumed, not listening yet`, {
          description: res.listenNote,
          action: { label: "Re-check", onClick: refresh },
        })
      } else if (!res.validation) {
        notify.warning(`${stream.name} resumed, not yet live`, { description: res.listenNote })
      } else {
        notify.success(
          res.listening ? `${stream.name} resumed and listening` : `${stream.name} resumed`,
          { description: res.listenNote },
        )
      }
    } catch (err) {
      notify.error(`${stream.name} not resumed`, err)
    } finally {
      setResuming("")
      refresh()
    }
  }

  // A test's results stay on the card until the next test or a reload of
  // the page: they are a reading of that moment, not of the stream.
  const [tests, setTests] = useState<Record<string, StreamTestResult[] | "running">>({})
  const test = async (stream: StreamEntry) => {
    setTests((t) => ({ ...t, [stream.name]: "running" }))
    try {
      const results = await Promise.all(
        streamTests(stream).map((req) => post<StreamTestResult>("/proxy/streams/test", req)),
      )
      setTests((t) => ({ ...t, [stream.name]: results }))
    } catch (err) {
      setTests((t) => {
        const rest = { ...t }
        delete rest[stream.name]
        return rest
      })
      notify.error(`${stream.name} not tested`, err)
    }
  }
  // Dialling an address is the scanner boundary, so only an admin tests.
  const testVerb = (stream: StreamEntry): Verb[] =>
    stream.error || streamTests(stream).length === 0
      ? []
      : [
          {
            key: "test",
            label: tests[stream.name] === "running" ? "Testing…" : "Test",
            icon: Lightning,
            disabled: tests[stream.name] === "running",
            run: () => void test(stream),
          },
        ]

  const pageVerbs: Verb[] = connection
    ? [{ key: "disconnect", label: "Disconnect", icon: Slash, danger: true, run: disconnect }]
    : []

  // A stream that is not live can be asked about again at once, rather than
  // at the next minute's poll — after freeing its port, say. Reading is no
  // change, so every account has it.
  const recheckVerb = (stream: StreamEntry): Verb[] =>
    stream.state === "live" || stream.state === "not-read" || stream.paused
      ? []
      : [
          {
            key: "recheck",
            label: checking ? "Checking…" : "Re-check",
            icon: RefreshClockwise,
            inline: true,
            disabled: checking,
            run: recheck,
          },
        ]
  const rawVerb = (stream: StreamEntry): Verb => ({
    key: "raw",
    label: "Raw file",
    icon: Code,
    disabled: Boolean(stream.error),
    run: () => setRaw(stream),
  })
  // Reading a stream's route is no change, so every account can copy it.
  const copyVerbs = (stream: StreamEntry): Verb[] =>
    stream.error
      ? []
      : [
          {
            key: "copy-listen",
            label: stream.address && !listenFamily(stream.address) ? "Copy address" : "Copy port",
            icon: Copy,
            run: () => void copyText(listenLabel(stream), `${listenLabel(stream)} copied`),
          },
          {
            key: "copy-upstream",
            label: "Copy upstream",
            icon: Copy,
            run: () => void copyText(stream.upstream, `${stream.upstream} copied`),
          },
        ]
  // Only a file the form can say everything about is duplicated: from one
  // with a deny rule or a second server, the copy would silently lack them.
  const duplicateVerb = (stream: StreamEntry): Verb[] =>
    stream.error || stream.unsupported.length > 0 || !canCreate
      ? []
      : [
          {
            key: "duplicate",
            label: "Duplicate",
            icon: Copy,
            run: () => open(null, duplicateSpec(stream)),
          },
        ]
  const deleteVerb = (stream: StreamEntry): Verb => ({
    key: "delete",
    label: "Delete",
    icon: Trash,
    danger: true,
    run: () => remove(stream),
  })
  const verbsFor = (stream: StreamEntry): Verb[] =>
    !admin
      ? [...recheckVerb(stream), ...copyVerbs(stream)]
      : stream.paused
        ? [
            {
              key: "resume",
              label: resuming === stream.name ? "Resuming…" : "Resume",
              icon: Play,
              inline: true,
              disabled: resuming !== "" || Boolean(stream.error),
              run: () => void resume(stream),
            },
            ...testVerb(stream),
            rawVerb(stream),
            ...duplicateVerb(stream),
            ...copyVerbs(stream),
            deleteVerb(stream),
          ]
        : [
            {
              key: "edit",
              label: "Edit",
              icon: Pencil,
              inline: true,
              disabled: Boolean(stream.error),
              run: () => open(stream),
            },
            ...recheckVerb(stream),
            ...testVerb(stream),
            rawVerb(stream),
            ...duplicateVerb(stream),
            ...copyVerbs(stream),
            // A link is stopped by deleting it: moved into paused/, a relative
            // one would point somewhere else.
            ...(stream.link || stream.error
              ? []
              : [{ key: "pause", label: "Pause", icon: Pause, run: () => pause(stream) }]),
            deleteVerb(stream),
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
          label="Live"
          value={states.live}
          hint={
            counts.all === 0
              ? "nothing forwarded"
              : outage
                ? "every reload refused"
                : !live
                  ? "not read by nginx"
                  : counts.stopping > 0
                    ? `${counts.stopping} stop${counts.stopping === 1 ? "s" : ""} every reload`
                    : states.live === counts.all
                      ? `all ${counts.all} listening`
                      : `of ${counts.all} streams`
          }
          tone={
            counts.all === 0
              ? "default"
              : outage || counts.stopping > 0
                ? "danger"
                : states.live < counts.all
                  ? "warning"
                  : "default"
          }
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
        <StreamSetup status={data} admin={admin} onChanged={refresh} />
      )}

      <Panel plain>
        <PanelHeader
          title="Port forwarding"
          actions={
            admin &&
            !noNginx && (
              <div className="flex items-center gap-1.5">
                {
                  // Staging a forward is still useful before nginx reads the
                  // directory, but not while its test refuses every file
                  // there: the notice above says why, and a form could only
                  // end in "Not saved".
                  !blocked && (
                    <Button
                      size="sm"
                      variant={live ? "default" : "outline"}
                      onClick={() => open(null)}
                      aria-keyshortcuts="n"
                      title="New stream (N)"
                    >
                      <Plus className="size-4" />
                      {live ? "New stream" : "Prepare a stream"}
                    </Button>
                  )
                }
                {pageVerbs.length > 0 && (
                  <VerbMenu verbs={pageVerbs} label="More stream directory actions" />
                )}
              </div>
            )
          }
        />
        {(listed.length > 1 || chip !== "all" || query !== "") && (
          // A chip no stream is under is hidden, except the one chosen: it
          // stays to say why the list is empty, and All is beside it.
          <PanelToolbar className="justify-between gap-x-4">
            <SearchInput
              ref={searchRef}
              dense
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Name, port, upstream or source"
              aria-label="Search streams"
              aria-keyshortcuts="/"
            />
            <ChipStrip aria-label="Filter streams">
              {STREAM_FILTERS.filter(
                ({ key }) => key === "all" || chips[key] > 0 || chip === key,
              ).map(({ key, label }) => (
                <FilterChip key={key} selected={chip === key} onClick={() => setChip(key)}>
                  {label} <ChipCount>{chips[key]}</ChipCount>
                </FilterChip>
              ))}
            </ChipStrip>
          </PanelToolbar>
        )}
        <PanelBody flush>
          {listed.length === 0 ? (
            <EmptyState
              mark={<ProductLogos ids={["postgresql", "redis", "minecraft-java"]} size="md" />}
              title="Nothing forwarded"
              description="Point a port on this host at a service somewhere else — a database replica, a bastion, a game server. Anything TCP or UDP."
              className="mt-2"
            />
          ) : shown.length === 0 ? (
            <EmptyNote className="py-6">
              {query.trim()
                ? `No stream matches “${query.trim()}”`
                : "No stream is under this chip"}
              {query.trim() && chip !== "all" ? " under this chip." : "."}
            </EmptyNote>
          ) : (
            // Every row opens the stream's form, so it is a choice and carries
            // the edge (§16). Each is drawn as the service its port is — a
            // forward on 5432 as Postgres — where the port says so, and as a
            // bare connection where it does not.
            <ProxyGrid aria-label="Streams">
              {shown.map((stream, index) => (
                <ChoiceRow
                  key={stream.name}
                  verb={admin && !stream.paused ? `Edit ${stream.name}` : stream.name}
                  onSelect={
                    admin && !stream.error && !stream.paused ? () => open(stream) : undefined
                  }
                  disabled={!admin || Boolean(stream.error) || Boolean(stream.paused)}
                  index={index}
                  className="h-full gap-4 p-4"
                  leading={
                    <ProductLogo id={portProduct(stream.listen)} size="md" fallback={Connection} />
                  }
                  title={<span className="text-title">{stream.name}</span>}
                  description={describe(stream)}
                  trailing={<Status {...stateStatus(stream)} />}
                >
                  {stream.error ? (
                    <p className="text-hint break-all text-muted-foreground">{stream.error}</p>
                  ) : (
                    <>
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
                      {
                        // While nginx reads none of the directory, every card
                        // would repeat what the steps above already say.
                        (live || stream.state !== "not-read") && <StateReason stream={stream} />
                      }
                    </>
                  )}
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <span className="block text-hint text-muted-foreground">Allowed sources</span>
                      <Restriction stream={stream} />
                    </div>
                    {Array.isArray(tests[stream.name]) && (
                      <TestResults
                        name={stream.name}
                        results={tests[stream.name] as StreamTestResult[]}
                      />
                    )}
                    {verbsFor(stream).length > 0 && (
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
        draft={draft}
        status={data}
        onOpenChange={(open) => {
          setForm((f) => ({ ...f, open }))
          if (!open) {
            forgetSessionState("proxy.stream.form.")
            setDraft(null)
          }
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
const BALANCE_LABELS: Record<NonNullable<StreamSpec["balance"]>, string> = {
  "least-conn": "least busy first",
  "client-ip": "one server per client",
  random: "picked at random",
}

function describe(stream: StreamEntry): string {
  if (stream.error) return "could not be read"
  return [
    `${protocolLabel(stream.protocol)} forwarding`,
    stream.udpMode === "request" &&
      (stream.protocol === "both" ? "one reply per UDP session" : "one reply per session"),
    stream.servers &&
      `${stream.servers.length} servers${stream.balance ? `, ${BALANCE_LABELS[stream.balance]}` : ""}`,
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
 * Why a stream is not simply live, as the line under its route: the
 * backend's sentence, nginx's own words for a bind it logged as failed, and
 * where to go about what holds the port — a site's page, or the list of
 * listening ports. The sentence already names a stream that shadows it,
 * whose card is on this page.
 */
function StateReason({ stream }: { stream: StreamEntry }) {
  if (!stream.stateReason) return null
  const { blocker } = stream
  return (
    <div className="min-w-0 space-y-1">
      <p className="text-hint leading-relaxed break-words text-muted-foreground">
        {stream.stateReason}{" "}
        {blocker?.kind === "site" && blocker.site ? (
          <Link
            href={`/proxy/sites?site=${encodeURIComponent(blocker.site)}`}
            className="underline underline-offset-2 hover:text-foreground"
          >
            Open the site
          </Link>
        ) : blocker?.kind === "program" ? (
          <Link href="/proxy/ports" className="underline underline-offset-2 hover:text-foreground">
            See who holds it
          </Link>
        ) : null}
      </p>
      {stream.bindError && (
        <p className="font-mono text-hint break-all text-muted-foreground">{stream.bindError}</p>
      )}
    </div>
  )
}

/**
 * A port a save is refused for, on the Listen field: the refusal, where to
 * see what holds it — the site, or the list of listening ports; a stream's
 * card is on this page — and the next port free as one press.
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
          href={`/proxy/sites?site=${encodeURIComponent(conflict.site)}`}
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
        label={`${service ? `${service} to anyone` : "anyone"}${stream.rules?.some((rule) => rule.action === "deny") ? " not denied" : ""}`}
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
  if (stream.rules && stream.rules.length > 0) {
    return (
      <span className="block font-mono text-hint break-all text-muted-foreground">
        {stream.rules.map((rule) => `${rule.action} ${rule.source}`).join(", ")}, then{" "}
        {stream.defaultAllow ? "allow" : "deny"} the rest
      </span>
    )
  }
  return <span className="block text-hint text-muted-foreground">set in the file</span>
}

/** A test's outcome on its card, and every dial's answer in a popover under it. */
function TestResults({ name, results }: { name: string; results: StreamTestResult[] }) {
  const summary = testSummary(results)
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button size="sm" variant="ghost" aria-label={`Test of ${name}: ${summary.label}`}>
          <Status tone={summary.tone} label={summary.label} />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 space-y-3">
        {results.map((result, i) => (
          <div key={i} className="min-w-0 space-y-1">
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3">
              <span className="text-hint font-medium">{testPlace(result)}</span>
              <Status {...testStatus(result)} />
            </div>
            {result.address && (
              <span className="block font-mono text-hint break-all text-muted-foreground">
                {result.address}
              </span>
            )}
            {result.banner && (
              <pre className="max-h-24 overflow-auto font-mono text-hint break-all whitespace-pre-wrap">
                {result.banner}
              </pre>
            )}
            {result.answer && <p className="text-hint">{result.answer}</p>}
            <p className="text-hint text-muted-foreground">{result.detail}</p>
            {result.warnings.map((warning) => (
              <p key={warning} className="text-hint text-warning">
                {warning}
              </p>
            ))}
          </div>
        ))}
      </PopoverContent>
    </Popover>
  )
}

/**
 * The form's reminder, at the point of commit, of what keeps a saved stream
 * from forwarding — and where the fix is: the page behind the form installs
 * the module and connects the directory.
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

/** A count or a rate as typed: a whole number above zero, or unset. */
function positive(text: string): number | undefined {
  const n = Number(text)
  return Number.isInteger(n) && n > 0 ? n : undefined
}

const BLANK: StreamSpec = {
  name: "",
  listen: 0,
  protocol: "tcp",
  upstream: "",
  proxyProtocol: false,
  allowFrom: [],
}

function StreamForm({
  open,
  stream,
  draft,
  status,
  onOpenChange,
  onSaved,
  onRaw,
}: {
  open: boolean
  /** The stream this form opened on; null for a new one. */
  stream: StreamEntry | null
  /** A new stream's starting fields: a duplicate's, or a link's. */
  draft: StreamSpec | null
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
    stream ? streamSpecOf(stream) : (draft ?? BLANK),
  )
  const [access, setAccess] = useSessionState<StreamAccess>(
    `${key}.access`,
    accessOf(stream ?? draft ?? BLANK),
  )
  // The timeouts are kept as typed — "10m" — and read on the way out.
  const [idle, setIdle] = useSessionState(`${key}.idle`, formatDuration((stream ?? draft)?.timeout))
  const [connect, setConnect] = useSessionState(
    `${key}.connect`,
    formatDuration((stream ?? draft)?.connectTimeout),
  )
  const [preset, setPreset] = useSessionState(`${key}.preset`, "")
  const presetPort = STREAM_PRESETS.find((p) => p.id === preset)?.port
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
  // form would drop what it cannot show — a max_conns, a TLS listener.
  const locked = Boolean(stream && (stream.error || stream.unsupported.length > 0))
  const readOnly = locked || blocked !== null
  const renaming = stream !== null && spec.name !== stream.name
  const service = DANGEROUS_PORTS[spec.listen]
  const family = listenFamily(spec.address)
  const edit = (change: Partial<StreamSpec>, field?: string) => {
    setSpec((s) => ({ ...s, ...change }))
    if (field && refused?.field === field) setRefused(null)
  }
  // The preview asks what the save would: a port something holds is said on
  // the field before the save is pressed, with the next one free. It is the
  // preview of this very spec only while nothing has been typed since.
  const conflict = preview?.conflict?.port === spec.listen ? preview.conflict : undefined
  const takePort = (port: number) => edit({ listen: port }, "spec.listen")
  const pickPreset = (id: string) => {
    const chosen = STREAM_PRESETS.find((p) => p.id === id)
    if (!chosen) return
    const next = applyPreset(chosen, spec, access)
    setPreset(id)
    setSpec(next.spec)
    setAccess(next.access)
    if (refused?.field === "spec.listen" || refused?.field === "spec.name") setRefused(null)
  }

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
  const targets = containerTargets(containers, spec.protocol)
  const target = targets.find((t) => t.upstream === spec.upstream)

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
      // The save watched nginx take the reload up: "saved and listening" only
      // when nginx holds the port, and a warning with a way to ask again
      // when it did not within the wait.
      const outcome = saveOutcome(res, live)
      notify[outcome.tone](outcome.title, {
        description: outcome.description,
        action: outcome.recheck ? { label: "Re-check", onClick: onSaved } : undefined,
      })
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
            disabled={
              readOnly ||
              busy ||
              !spec.name ||
              !spec.listen ||
              !spec.upstream ||
              spec.servers?.some((server) => !server.address.trim()) ||
              !timed ||
              accessProblem !== ""
            }
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
          {!stream && (
            <Field
              label="Start from"
              hint={
                STREAM_PRESETS.find((p) => p.id === preset)?.hint ??
                "Sets the port, protocol, UDP mode and a starting allow list for a common service."
              }
            >
              <Select value={preset} onValueChange={pickPreset}>
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

          <StreamServers
            pool={spec}
            onChange={(pool) => edit(pool)}
            hint={
              target?.exposed
                ? `${target.container} publishes this port on every address, so it is reachable around the stream, where these rules do not apply. Publish it on 127.0.0.1 instead.`
                : "host:port of the service behind it, or unix:/path for a local socket."
            }
            placeholder={`10.0.0.5:${presetPort ?? 5432}`}
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

          <AccessRules access={access} error={accessProblem} onChange={setAccess} />

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
              error={idleSeconds === null && durationError(idle, "10m")}
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
              error={connectSeconds === null && durationError(connect, "60s")}
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

          <FieldRow>
            <Field
              label="Connections per client"
              htmlFor="stream-conn-ip"
              hint="Open at once from one address; a UDP session counts as one. Empty is no cap."
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
              hint="nginx closes one over either cap as soon as it is accepted."
            >
              <Input
                id="stream-conn-total"
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
              hint="KiB/s from the client, per connection: four connections get four times it."
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
              hint="KiB/s to the client, per connection. Empty is no limit."
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
