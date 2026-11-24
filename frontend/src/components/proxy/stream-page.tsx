"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { Connection, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import type {
  StreamEntry,
  StreamResult,
  StreamSessions,
  StreamSpec,
  StreamStatus,
  StreamTraffic,
  StreamTrafficSummary,
  StreamWindow,
} from "@/lib/types"
import {
  STREAM_FILTERS,
  byUrgency,
  duplicateSpec,
  listenFamily,
  listenLabel,
  saveBlocked,
  stateLabel,
  stateStatus,
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
import { DetailPage, siblingSteps, useDetailTab, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { ViewTabList } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Segments } from "@/components/segments"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useProxyRead } from "@/components/proxy/proxy-context"
import { sitePath } from "@/components/proxy/site-verbs"
import { StreamForm, StreamSettings } from "@/components/proxy/stream-form"
import { StreamPicture } from "@/components/proxy/stream-picture"
import { StreamRail } from "@/components/proxy/stream-rail"
import {
  STREAM_TABS,
  restrictionWords,
  streamHref,
  streamTab,
  streamTraits,
  upstreamsOf,
  type StreamTab,
} from "@/components/proxy/stream-facts"
import {
  LogState,
  StreamClients,
  StreamConnections,
  StreamReadings,
  StreamTimeline,
  WINDOWS,
  windowRead,
} from "@/components/proxy/stream-traffic"
import { TestResults, useStreamVerbs } from "@/components/proxy/stream-verbs"
import { decodeSegment } from "@/lib/route-params"

const BACK = { href: "/proxy/streams", label: "Streams" }

const TAB_LABEL: Record<StreamTab, string> = {
  overview: "Overview",
  clients: "Clients",
  connections: "Connected now",
  settings: "Settings",
}

function dotTone(stream: StreamEntry): DotTone {
  const status = stateStatus(stream)
  if (status.tone) return status.tone
  return status.verdict === "ok" ? "running" : status.verdict === "critical" ? "danger" : "warning"
}

/**
 * One stream, as a place of its own.
 *
 * It was two sheets over the Streams list until 0.8.7: the edit form, and a
 * traffic sheet with two rows of tiles. A stream is now a destination with a
 * way back, the way a site, a process and a unit are: the identity line —
 * the port as the service its number is, its name, what kind of forward it
 * is, where it goes and who may reach it, its state at the end — and its
 * verbs above it; then its views as the underlined strip, with the view in
 * the address (`?tab=`). The Overview draws the forward (port, nginx, the
 * servers behind it) and what it carried as reading lines; Clients ranks who
 * connected, with the rule that turns one away a press from it; Connected
 * now lists the TCP sockets nginx holds; Settings is the form, as a settings
 * column with one floating save bar.
 *
 * The list stays beside it from `xl` (`StreamRail`), with the list's own
 * search and chip, so the next stream is one press away; Alt+↑ and Alt+↓ walk
 * it, and the view on screen goes with you. On a phone the page is the one
 * pane, and scrolls as a whole.
 */
export function StreamPage() {
  const params = useParams<{ name: string }>()
  const router = useRouter()
  const name = decodeSegment(params.name)
  const { can } = useAuth()
  const admin = can("system.admin")
  // The view is kept in the address and remembered for the visit, as a
  // site's, a port's and a certificate's are.
  const [asked, choose] = useDetailTab(STREAM_TABS, { remember: "proxy.stream.tab" })
  // Settings is the form, which only an administrator can use.
  const tab: StreamTab = asked === "settings" && !admin ? "overview" : asked
  const setTab = useCallback((next: string) => choose(streamTab(next)), [choose])

  const streams = useProxyRead("streams")
  const listed = useMemo(
    () => [...(streams.data?.streams ?? []), ...(streams.data?.paused ?? [])],
    [streams.data],
  )
  const [chip, setChip] = useSessionState<StreamFilter>("proxy.streams.filter", "all")
  const [query, setQuery] = useSessionState("proxy.streams.query", "")
  const railed = useMemo(
    () =>
      listed
        .filter((stream) => streamMatchesFilter(stream, chip) && streamMatchesQuery(stream, query))
        .sort(byUrgency),
    [listed, chip, query],
  )
  const steps = siblingSteps(
    railed.map((stream) => stream.name),
    name,
    (key) => router.push(streamHref(key, tab)),
  )
  const filterLabel = STREAM_FILTERS.find((f) => f.key === chip)?.label
  const caption = streams.data
    ? `${chip !== "all" || query ? `${railed.length} of ${listed.length}` : `${listed.length} ${listed.length === 1 ? "stream" : "streams"}`}${chip !== "all" ? ` · ${filterLabel}` : " · worst first"}`
    : "Reading the streams…"

  return (
    <StreamMain
      key={name}
      name={name}
      admin={admin}
      status={streams.data}
      error={streams.error}
      refresh={streams.refresh}
      tab={tab}
      setTab={setTab}
      steps={steps}
      rail={
        <StreamRail
          streams={railed}
          current={name}
          tab={tab}
          loading={streams.loading}
          error={streams.data ? undefined : streams.error}
          onRetry={streams.refresh}
          query={query}
          onQuery={setQuery}
          filter={chip !== "all" ? chip : undefined}
          onClearFilter={() => setChip("all")}
          caption={caption}
        />
      }
    />
  )
}

function StreamMain({
  name,
  admin,
  status,
  error,
  refresh,
  tab,
  setTab,
  steps,
  rail,
}: {
  name: string
  admin: boolean
  status: StreamStatus | undefined
  error: Error | undefined
  refresh: () => void
  tab: StreamTab
  setTab: (tab: string) => void
  steps: { previous: Step; next: Step }
  rail: React.ReactNode
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const stream = useMemo(
    () => [...(status?.streams ?? []), ...(status?.paused ?? [])].find((s) => s.name === name),
    [status, name],
  )
  const [raw, setRaw] = useState(false)
  const [form, setForm] = useState<{ open: boolean; session: number; draft: StreamSpec | null }>({
    open: false,
    session: 0,
    draft: null,
  })
  const [span, setSpan] = useSessionState<StreamWindow>("proxy.stream.window", "1h")
  const reading: unknown = error ?? status
  const [checkingFrom, setCheckingFrom] = useState<unknown>(undefined)
  const checking = checkingFrom !== undefined && checkingFrom === reading
  const live = status ? streamsLive(status) : false

  const readable = Boolean(stream && !stream.error)
  const traffic = usePoll<StreamTraffic>(
    (signal) => get(`/proxy/streams/${encodeURIComponent(name)}/traffic`, { window: span }, signal),
    60_000,
    [name, span],
    { enabled: readable && (tab === "overview" || tab === "clients") },
  )
  // Sockets change by the second, so the list is asked for often, and only
  // while it is on screen.
  const sessions = usePoll<StreamSessions>(
    (signal) => get(`/proxy/streams/${encodeURIComponent(name)}/sessions`, undefined, signal),
    10_000,
    [name],
    { enabled: readable && tab === "connections" },
  )
  // The hour of every stream, for whether the picture's line moves.
  const summaries = usePoll<Record<string, StreamTrafficSummary>>(
    (signal) => get("/proxy/streams/traffic", undefined, signal),
    60_000,
    [],
    { enabled: readable && tab === "overview" },
  )

  const canCreate = admin && Boolean(status) && !saveBlocked(status!)
  const verbs = useStreamVerbs({
    status,
    admin,
    confirm,
    refresh,
    checking,
    recheck: () => {
      setCheckingFrom(reading)
      refresh()
    },
    onDuplicate: canCreate
      ? (s) => {
          forgetSessionState("proxy.stream.form.new.")
          setForm((f) => ({ open: true, session: f.session + 1, draft: duplicateSpec(s) }))
        }
      : undefined,
    onRaw: () => setRaw(true),
    // The page was the stream's own; once it is gone the list is where to be.
    onDeleted: () => router.push("/proxy/streams"),
  })

  // The frame every detail page has (D9): the way back with the arrows, the
  // rail from `xl`, and the identity line held still over the views.
  const frame = (
    identity: React.ReactNode,
    body: React.ReactNode,
    title: React.ReactNode = name,
  ) => (
    <DetailPage
      name="Stream"
      what="stream"
      stateKey="proxy.stream.page"
      refresh={refresh}
      steps={steps}
      rail={rail}
      back={BACK}
      title={title}
      identity={identity}
    >
      {body}
    </DetailPage>
  )

  if (!stream) {
    if (!status) {
      if (error) return frame(undefined, <ErrorState error={error} onRetry={refresh} />)
      // The line keeps a place for its verbs and its state, as the loaded
      // line has them, so it is as tall on a phone as the line it becomes.
      return frame(
        <IdentityHeader
          loading
          className="border-b-0 pb-0"
          fallback={Connection}
          title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
          state
          command
          menu={false}
        />,
        <LoadingRows />,
      )
    }
    return frame(
      undefined,
      <EmptyState
        icon={Connection}
        title={`No stream called ${name}`}
        description="It may have been deleted or renamed. The Streams list has every forward on this host."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href="/proxy/streams">All streams</Link>
          </Button>
        }
      />,
    )
  }

  const { primary, rest } = verbs.pageVerbs(stream)
  const tested = verbs.tests[stream.name]
  const window = windowRead(traffic, name, span)
  const views = STREAM_TABS.filter((key) => key !== "settings" || admin)
  const engineTone: DotTone = streamOutage(status!) ? "danger" : !live ? "warning" : "running"
  const settingsHref = admin && !stream.paused ? streamHref(name, "settings") : undefined

  const onSaved = (res: StreamResult) => {
    // What was saved is the stream now: the draft goes, and the form is
    // drawn again from the stream once the listing has it.
    forgetSessionState(`proxy.stream.form.${name}.`)
    refresh()
    if (res.name !== name) router.replace(streamHref(res.name, "settings"))
  }

  // The identity line's verbs by the one rule (`IdentityVerbs`): the command
  // first, the rest behind `⋯`; a test's outcome stands before them.
  const lineVerbs = primary
    ? IdentityVerbs({ verbs: [{ ...primary, inline: true }, ...rest], name: stream.name })
    : {
        command: undefined,
        menu: rest.length > 0 && (
          <VerbMenu verbs={rest} label={`More actions for ${stream.name}`} />
        ),
      }

  const windowed = (tab === "overview" || tab === "clients") && readable
  const windowChoice = (
    <Segments
      label="Window"
      value={span}
      onChange={setSpan}
      options={WINDOWS.map((w) => ({ value: w.value, label: w.label }))}
    />
  )

  return frame(
    <>
      <StreamIdentity
        stream={stream}
        command={
          <>
            {Array.isArray(tested) && <TestResults name={stream.name} results={tested} />}
            {lineVerbs.command}
          </>
        }
        menu={lineVerbs.menu}
      />
      {error && (
        <Notice tone="warning" icon={Warning} title="This is the stream as it was last read">
          <div className="space-y-2">
            <p className="break-words">{errorMessage(error)}</p>
            <Button size="sm" variant="outline" onClick={refresh}>
              Try again
            </Button>
          </div>
        </Notice>
      )}
      <StateNotice stream={stream} />
    </>,
    <>
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <ViewTabList
          label={`Views of ${stream.name}`}
          value={tab}
          views={views.map((key) => ({ key, label: TAB_LABEL[key] }))}
          sticky
          // Beside the views where there is room; on a phone a row of its
          // own under them, or it squeezed the strip and cut a view's name.
          trailing={windowed && <span className="max-sm:hidden">{windowChoice}</span>}
        />
        {windowed && <div className="pt-3 sm:hidden">{windowChoice}</div>}

        <TabsContent value="overview" className="pt-6">
          {tab === "overview" && readable && (
            <div className="flex min-w-0 flex-col gap-8">
              <StreamPicture
                streams={[stream]}
                summaries={summaries.data}
                engineTone={engineTone}
                engineHint={<span className="font-mono">{status!.dir}</span>}
                current={stream.name}
              />
              {window && <LogState traffic={window} settingsHref={settingsHref} />}
              {traffic.error && !window && (
                <ErrorState error={traffic.error} onRetry={traffic.refresh} />
              )}
              <StreamReadings name={name} traffic={window} span={span} error={traffic.error} />
              <StreamTimeline traffic={window} />
            </div>
          )}
        </TabsContent>
        <TabsContent value="clients" className="pt-6">
          {tab === "clients" && readable && (
            <div className="flex min-w-0 flex-col gap-6">
              {window && <LogState traffic={window} settingsHref={settingsHref} />}
              {traffic.error && !window ? (
                <ErrorState error={traffic.error} onRetry={traffic.refresh} />
              ) : (
                <StreamClients
                  traffic={window}
                  stream={stream}
                  admin={admin}
                  live={live}
                  onSaved={() => {
                    refresh()
                    traffic.refresh()
                  }}
                />
              )}
            </div>
          )}
        </TabsContent>
        <TabsContent value="connections" className="pt-6">
          {tab === "connections" && readable && <StreamConnections name={name} state={sessions} />}
        </TabsContent>
        {admin && (
          <TabsContent value="settings" className="pt-6">
            {tab === "settings" &&
              (stream.paused ? (
                <PausedSettings
                  name={stream.name}
                  resuming={verbs.resuming === stream.name}
                  onResume={() => void verbs.resume(stream)}
                />
              ) : (
                <StreamSettings
                  // Drawn again from the stream whenever the listing changes
                  // it; an edit in progress is kept for the tab, so it
                  // survives that.
                  key={JSON.stringify(stream)}
                  stream={stream}
                  status={status!}
                  onSaved={onSaved}
                  onRaw={() => setRaw(true)}
                />
              ))}
          </TabsContent>
        )}
      </Tabs>

      {admin && status && (
        <StreamForm
          key={`dup:${form.session}`}
          open={form.open}
          draft={form.draft}
          status={status}
          onOpenChange={(open) => {
            setForm((f) => ({ ...f, open }))
            if (!open) forgetSessionState("proxy.stream.form.new.")
          }}
          onSaved={(saved) => {
            refresh()
            if (saved) router.push(streamHref(saved))
          }}
        />
      )}
      <ConfigEditor
        open={raw}
        onOpenChange={(open) => !open && setRaw(false)}
        path={stream.path}
        kind="nginx"
        title={`${stream.name}.conf`}
        readOnly={!admin}
        onSaved={refresh}
      />
      {dialog}
    </>,
    stream.name,
  )
}

/**
 * The line the page opens on: the port as the service its number is with its
 * state in the tile's corner, the stream's name and where it listens, then
 * what kind of forward it is, where it goes and who may reach it; its state
 * at the line's end.
 */
function StreamIdentity({
  stream,
  command,
  menu,
}: {
  stream: StreamEntry
  command: React.ReactNode
  menu: React.ReactNode
}) {
  const who = restrictionWords(stream)
  const family = listenFamily(stream.address)
  const reach = (
    <Fact
      className={cn(
        who.mono && "font-mono",
        who.tone === "danger" && "font-medium text-destructive",
        who.tone === "warning" && "text-warning",
      )}
    >
      {stream.open ? who.label : `from ${who.label}`}
    </Fact>
  )
  // Who may reach it wants action when it is open to anyone, so it leads the
  // facts then: a phone's one line of facts never clips it.
  const urgent = who.tone === "danger" || who.tone === "warning"
  return (
    <IdentityHeader
      className="animate-rise border-b-0 pb-0"
      label={`About ${stream.name}`}
      slot="host-identity"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={portProduct(stream.listen)}
            fallback={Connection}
            className="size-10 rounded-lg [&_img]:size-6 [&>svg]:size-5"
          />
          <StatusDot corner="lg" tone={dotTone(stream)} live={stream.state === "live"} />
        </span>
      }
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <span className="truncate">{stream.name}</span>
          {!stream.error && (
            <span
              // Set solid: on the body's 20px line the mono face hung a pixel
              // under the name's 24, and the line the page opens on was that
              // much taller than the one it had waited on.
              className="numeric shrink-0 font-mono text-body leading-none font-normal text-muted-foreground"
              title={family ? `Every ${family} address` : undefined}
            >
              {listenLabel(stream)}
            </span>
          )}
        </span>
      }
      facts={
        stream.error ? (
          <Fact mono>{stream.path}</Fact>
        ) : (
          <>
            {urgent && reach}
            {/* On a phone the line is where it goes and who may reach it:
                the kind of forward is the page's to say, and a line of
                facts that wraps moved the views under it as it loaded. */}
            {streamTraits(stream).map((fact) => (
              <Fact key={fact} className="max-sm:hidden">
                {fact}
              </Fact>
            ))}
            <Fact mono>→ {upstreamsOf(stream).join(", ")}</Fact>
            {!urgent && reach}
          </>
        )
      }
      state={<Status {...stateStatus(stream)} />}
      command={command}
      menu={menu}
    />
  )
}

/**
 * Why a stream is not simply live, said where the reader can act on it: the
 * backend's sentence, nginx's own words for a bind it logged as failed, and
 * where to go about what holds the port — the site's page, or the list of
 * listening ports. A file that could not be read says why.
 */
function StateNotice({ stream }: { stream: StreamEntry }) {
  if (stream.error) {
    return (
      <Notice tone="danger" icon={Warning} title="This file could not be read">
        <p className="font-mono text-hint break-all">{stream.error}</p>
      </Notice>
    )
  }
  if (!stream.stateReason || stream.state === "live" || stream.paused) return null
  const { blocker } = stream
  return (
    <Notice
      tone={stream.state === "not-listening" ? "danger" : "warning"}
      icon={Warning}
      title={stateLabel(stream.state).replace(/^./, (c) => c.toUpperCase())}
    >
      <div className="min-w-0 space-y-1">
        <p className="leading-relaxed break-words">
          {stream.stateReason}{" "}
          {blocker?.kind === "site" && blocker.site ? (
            <Link href={sitePath(blocker.site)} className="underline underline-offset-2">
              Open the site
            </Link>
          ) : blocker?.kind === "program" ? (
            <Link href="/proxy/ports" className="underline underline-offset-2">
              See who holds it
            </Link>
          ) : blocker?.kind === "stream" && blocker.name ? (
            <Link href={streamHref(blocker.name)} className="underline underline-offset-2">
              Open {blocker.name}
            </Link>
          ) : null}
        </p>
        {stream.bindError && (
          <p className="font-mono text-hint break-all text-muted-foreground">{stream.bindError}</p>
        )}
      </div>
    </Notice>
  )
}

/** Settings for a paused stream: the file is out of nginx's reach, and the form edits it once it is back. */
function PausedSettings({
  name,
  resuming,
  onResume,
}: {
  name: string
  resuming: boolean
  onResume: () => void
}) {
  return (
    <EmptyState
      icon={Connection}
      title={`${name} is paused`}
      description="Its file is kept in paused/, unchanged. Resume it to edit it here."
      action={
        <Button size="sm" onClick={onResume} pending={resuming} disabled={resuming}>
          Resume
        </Button>
      }
    />
  )
}
