"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Connection, Plus, Slash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { errorMessage, get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  StreamEntry,
  StreamIncludeResult,
  StreamSpec,
  StreamTrafficSummary,
} from "@/lib/types"
import {
  STREAM_FILTERS,
  byUrgency,
  disconnectChange,
  duplicateSpec,
  moduleLabel,
  moduleMissing,
  protocolLabel,
  saveBlocked,
  stateStatus,
  streamDraftFromQuery,
  streamFilterCounts,
  streamMatchesFilter,
  streamMatchesQuery,
  streamOutage,
  streamsLive,
  type StreamFilter,
} from "@/lib/streams"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { ProductLogo, ProductLogos, hasProductLogo, portProduct } from "@/components/product-logo"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { EngineSetup } from "@/components/proxy/engine-setup"
import { StreamSetup } from "@/components/proxy/stream-setup"
import { BLANK, StreamForm } from "@/components/proxy/stream-form"
import { StreamPicture } from "@/components/proxy/stream-picture"
import { TestResults, useStreamVerbs } from "@/components/proxy/stream-verbs"
import {
  hourWords,
  legacyStreamHref,
  restrictionWords,
  routeLine,
  streamHref,
  streamsVerdict,
} from "@/components/proxy/stream-facts"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"

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
 * its own include out again from the identity line's menu.
 *
 * It opens on what it is about — the module, the forwards and the verdict on
 * them, with New stream at the line's end — then a picture of the host's
 * ports wired through nginx to where they go, then the forwards as one-line
 * rows, each a way to that stream's own page. Four tiles stood on top until
 * 0.8.7 (Live, TCP, UDP, Open to anyone): Live is the verdict at the line's
 * end, a press of which narrows the list to what it counts; TCP and UDP were
 * already the chips; Open to anyone is a fact in the line and a warning
 * colour in the picture.
 */
export function StreamsPage() {
  const { can } = useAuth()
  const router = useRouter()
  const proxy = useProxy()
  const { confirm, dialog } = useConfirm()
  const [form, setForm] = useSessionState("proxy.streams.form", { open: false, session: 0 })
  // A new stream's starting fields — a duplicate, or a link's ?new=1 — kept
  // for the tab like the form's own fields, so a reload opens the same form.
  const [draft, setDraft] = useSessionState<StreamSpec | null>("proxy.streams.draft", null)
  const [raw, setRaw] = useState<StreamEntry | null>(null)
  const [chip, setChip] = useSessionState<StreamFilter>("proxy.streams.filter", "all")
  const [query, setQuery] = useSessionState("proxy.streams.query", "")
  const searchRef = useRef<HTMLInputElement>(null)
  const params = useSearchParams()
  const { data, error, refresh } = useProxyRead("streams")
  // A re-check reads the listing again; it is over when the reading it was
  // asked from is replaced — by the next one, or by a failure.
  const reading: unknown = error ?? data
  const [checkingFrom, setCheckingFrom] = useState<unknown>(undefined)
  const checking = checkingFrom !== undefined && checkingFrom === reading
  const recheck = () => {
    setCheckingFrom(reading)
    refresh()
  }
  // The last hour of every stream that logs; a stream that does not log has
  // no figure rather than a zero it never measured.
  const { data: summaries } = usePoll<Record<string, StreamTrafficSummary>>(
    (signal) => get("/proxy/streams/traffic", undefined, signal),
    60_000,
  )
  const admin = can("system.admin")
  const noNginx = !proxy.loading && !proxy.hasNginx

  const openNew = (start: StreamSpec | null = null) => {
    // A starting point replaces whatever a closed-by-reload new form left
    // behind, which would otherwise win over it.
    forgetSessionState("proxy.stream.form.new.")
    setDraft(start)
    setForm((f) => ({ open: true, session: f.session + 1 }))
  }

  // Paused streams are listed with the rest, apart only from the counts of
  // what nginx reads.
  const listed = useMemo(() => [...(data?.streams ?? []), ...(data?.paused ?? [])], [data])
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

  // ?stream=<name> was the form over this list; a stream has a page of its
  // own now, and the old address lands on it — on Settings where the form
  // would have opened. ?new=1&listen=&upstream=&protocol= still opens a new
  // one filled in, once the first listing is here to check against. The
  // parameters are then taken out of the address, so a reload or Back does
  // not open the form again over one closed by hand.
  const linked = useRef(false)
  useEffect(() => {
    if (!data || linked.current) return
    linked.current = true
    const name = params.get("stream")
    if (name) {
      router.replace(
        legacyStreamHref(
          name,
          listed.find((s) => s.name === name),
          admin,
        ),
      )
      return
    }
    const start = streamDraftFromQuery(params)
    if (!start) return
    if (canCreate) openNew({ ...BLANK, ...start })
    const url = new URL(window.location.href)
    for (const key of ["new", "listen", "upstream", "protocol", "name"]) {
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
        openNew()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formOpen, canCreate])

  const verbs = useStreamVerbs({
    status: data,
    admin,
    confirm,
    refresh,
    checking,
    recheck,
    onDuplicate: canCreate ? (stream) => openNew(duplicateSpec(stream)) : undefined,
    onRaw: setRaw,
  })

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
  const directoryVerbs: Verb[] =
    admin && connection
      ? [{ key: "disconnect", label: "Disconnect", icon: Slash, danger: true, run: disconnect }]
      : []

  const verdict = data ? streamsVerdict(data, listed) : null
  const newStream = admin && !noNginx && data && !blocked && (
    // Staging a forward is still useful before nginx reads the directory,
    // but not while its test refuses every file there: the notice under the
    // line says why, and a form could only end in "Not saved".
    <Button
      size="sm"
      variant={live ? "default" : "outline"}
      onClick={() => openNew()}
      aria-keyshortcuts="n"
      title="New stream (N)"
    >
      <Plus className="size-4" />
      {live ? "New stream" : "Prepare a stream"}
    </Button>
  )

  // The forwards as the services they reach, the commonest first; nginx
  // where none of their ports names one.
  const products = useMemo(() => {
    const seen = new Map<string, number>()
    for (const stream of listed) {
      const id = portProduct(stream.listen)
      if (hasProductLogo(id)) seen.set(id, (seen.get(id) ?? 0) + 1)
    }
    return [...seen.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id)
  }, [listed])
  const open = (data?.streams ?? []).filter((s) => s.open && !s.error).length
  const paused = data?.paused?.length ?? 0
  const hour = summaries
    ? Object.values(summaries).reduce((sum, summary) => sum + summary.sessions, 0)
    : undefined
  const engineTone: DotTone = outage ? "danger" : !live ? "warning" : "running"

  const identity = (
    <HostIdentity
      className="animate-rise"
      logo={
        products.length > 0 ? (
          <span className="flex h-10 shrink-0 items-center">
            <ProductLogos ids={products.slice(0, 4)} size="md" />
          </span>
        ) : undefined
      }
      mark="nginx"
      fallback={Connection}
      title="Port forwarding"
      // Laid out as a project's header, as the overview's line is: what needs
      // the reader beside the name, one command at the end, the rest in `⋯`.
      state={
        verdict ? (
          <Verdict
            verdict={verdict}
            pressed={verdict.chip !== undefined && chip === verdict.chip}
            onPress={() => verdict.chip && setChip(chip === verdict.chip ? "all" : verdict.chip)}
          />
        ) : noNginx ? (
          <Status tone="stopped" label="Not installed" />
        ) : !data ? (
          error ? (
            <Status tone="danger" label="Not read" />
          ) : (
            <Status tone="unknown" label="Reading" />
          )
        ) : (
          <Status tone="stopped" label="No forwards" />
        )
      }
      // An empty list carries the command in its own empty state.
      command={listed.length > 0 ? newStream : undefined}
      menu={
        directoryVerbs.length > 0 ? (
          <VerbMenu verbs={directoryVerbs} label="More stream directory actions" />
        ) : undefined
      }
      facts={
        noNginx ? (
          <span>nginx is not installed on this host</span>
        ) : !data ? (
          <span>{error ? "couldn't read the streams" : "Reading the streams…"}</span>
        ) : (
          <>
            <HostFact product="nginx">
              <span className={cn(moduleMissing(data.module) && "font-medium text-warning")}>
                stream module {moduleLabel(data.module)}
              </span>
            </HostFact>
            <FactDot />
            <span className="numeric">{plural(data.streams.length, "forward")}</span>
            {open > 0 && (
              <>
                <FactDot />
                <span className="numeric text-warning">{open} open to anyone</span>
              </>
            )}
            {paused > 0 && (
              <>
                <FactDot />
                <span className="numeric">{paused} paused</span>
              </>
            )}
            {hour !== undefined && hour > 0 && (
              <>
                <FactDot />
                <span className="numeric">{plural(hour, "session")} in the hour</span>
              </>
            )}
          </>
        )
      }
    />
  )

  const header = <PageContext eyebrow="Proxy" title="Streams" />

  if (noNginx) {
    return (
      <Page className="animate-rise">
        {header}
        {identity}
        {/* The section's own install screen: the module and the forwards
            come once nginx does, and the overview draws the same one. */}
        {proxy.status && (
          <EngineSetup
            status={proxy.status}
            admin={admin}
            onInstalled={() => {
              proxy.refresh()
              refresh()
            }}
          />
        )}
      </Page>
    )
  }

  if (!data) {
    return (
      <Page>
        {header}
        {identity}
        {error ? (
          <ErrorState error={error} onRetry={refresh} />
        ) : (
          // The list's own silhouette, so nothing moves when it lands.
          <div className="space-y-6">
            <Skeleton className="h-56 w-full max-lg:hidden" />
            <LoadingRows rows={4} />
          </div>
        )}
      </Page>
    )
  }

  const drawn = listed.filter((s) => !s.error)

  return (
    <Page className="animate-rise">
      {header}
      {identity}

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

      <StreamSetup status={data} admin={admin} onChanged={refresh} />

      {drawn.length > 0 && (
        <StreamPicture
          streams={drawn}
          summaries={summaries}
          engineTone={engineTone}
          engineHint={<span className="font-mono">{data.dir}</span>}
        />
      )}

      <Panel plain aria-label="Forwards">
        <PanelHeader
          title="Forwards"
          actions={
            listed.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {shown.length === listed.length
                  ? plural(listed.length, "stream")
                  : `${shown.length} of ${plural(listed.length, "stream")}`}
              </span>
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
        <PanelBody flush className="pt-3">
          {listed.length === 0 ? (
            <EmptyState
              mark={<ProductLogos ids={["postgresql", "redis", "minecraft-java"]} size="md" />}
              title="Nothing forwarded"
              description="Point a port on this host at a service somewhere else — a database replica, a bastion, a game server. Anything TCP or UDP."
              action={newStream}
            />
          ) : shown.length === 0 ? (
            <EmptyNote className="py-6">
              {query.trim()
                ? `No stream matches “${query.trim()}”`
                : "No stream is under this chip"}
              {query.trim() && chip !== "all" ? " under this chip." : "."}
            </EmptyNote>
          ) : (
            <ChoiceList aria-label="Streams">
              {shown.map((stream, index) => (
                <StreamRow
                  key={stream.name}
                  stream={stream}
                  index={index}
                  hour={hourWords(summaries?.[stream.name])}
                  tests={verbs.tests[stream.name]}
                  verbs={verbs.rowVerbs(stream)}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <StreamForm
        key={`new:${form.session}`}
        open={form.open}
        draft={draft}
        status={data}
        onOpenChange={(open) => {
          setForm((f) => ({ ...f, open }))
          if (!open) {
            forgetSessionState("proxy.stream.form.new.")
            setDraft(null)
          }
        }}
        onSaved={() => refresh()}
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

/**
 * A stream as one line, the same height whatever it holds: the port as the
 * service its number is, the name over where it listens and where that goes,
 * then in aligned columns the protocol, who may connect, its last hour (or a
 * test's outcome, which takes that place until the next one), and its state.
 * The row is a way to the stream's own page; everything else it can do is in
 * its menu. Why a stream is not live is the state's tooltip here and a
 * sentence on its page.
 */
function StreamRow({
  stream,
  index,
  hour,
  tests,
  verbs,
}: {
  stream: StreamEntry
  index: number
  hour?: string
  tests?: ReturnType<typeof useStreamVerbs>["tests"][string]
  verbs: Verb[]
}) {
  const who = restrictionWords(stream)
  const state = stateStatus(stream)
  return (
    <ChoiceRow
      href={streamHref(stream.name)}
      verb={`Open ${stream.name}`}
      index={index}
      busy={tests === "running"}
      className={cn(stream.paused && "opacity-80")}
      leading={<ProductLogo id={portProduct(stream.listen)} size="sm" fallback={Connection} />}
      title={stream.name}
      description={
        <span className={cn(!stream.error && "font-mono")} title={stream.error && stream.path}>
          {stream.error ?? routeLine(stream)}
        </span>
      }
      trailing={
        <>
          <span className="w-16 text-hint text-muted-foreground max-lg:hidden">
            {stream.error ? "" : protocolLabel(stream.protocol)}
          </span>
          <span
            title={who.label}
            className={cn(
              "w-40 truncate text-hint max-lg:hidden",
              who.mono && "font-mono",
              who.tone === "danger"
                ? "font-medium text-destructive"
                : who.tone === "warning"
                  ? "text-warning"
                  : "text-muted-foreground",
            )}
          >
            {who.label}
          </span>
          <span className="flex w-28 justify-end text-hint max-sm:hidden">
            {Array.isArray(tests) ? (
              <TestResults name={stream.name} results={tests} />
            ) : (
              <span className="numeric text-muted-foreground">{hour ?? ""}</span>
            )}
          </span>
          <span className="flex justify-end sm:w-28" title={stream.stateReason ?? stream.error}>
            <Status {...state} />
          </span>
        </>
      }
      actions={
        verbs.length > 0 ? (
          <VerbMenu verbs={verbs} label={`More actions for ${stream.name}`} />
        ) : (
          <span aria-hidden className="size-8" />
        )
      }
    />
  )
}

/**
 * The line's verdict. A count of streams is a question about which, so a
 * press narrows the list to exactly them; an all-clear has nothing to press.
 */
function Verdict({
  verdict,
  pressed,
  onPress,
}: {
  verdict: NonNullable<ReturnType<typeof streamsVerdict>>
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
      {status}
    </button>
  )
}
