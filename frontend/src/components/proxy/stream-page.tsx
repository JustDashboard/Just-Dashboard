"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, Connection, Warning } from "@/components/icons"
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
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Segments } from "@/components/deploy/settings/segments"
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

const BACK = (
  <Link
    href="/proxy/streams"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Streams
  </Link>
)

const TAB_LABEL: Record<StreamTab, string> = {
  overview: "Overview",
  clients: "Clients",
  connections: "Connected now",
  settings: "Settings",
}

/** The route's segment as a stream name; a name cannot hold `%`, so decoding twice is harmless. */
function decodeName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
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
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeName(params.name)
  const { can } = useAuth()
  const admin = can("system.admin")
  const asked = streamTab(search.get("tab"))
  // Settings is the form, which only an administrator can use.
  const tab: StreamTab = asked === "settings" && !admin ? "overview" : asked
  const setTab = useCallback(
    // Replaced rather than pushed: the view is not a place Back should step through.
    (next: string) => router.replace(streamHref(name, streamTab(next)), { scroll: false }),
    [router, name],
  )

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
  const at = railed.findIndex((stream) => stream.name === name)
  const step = (direction: number) => {
    const next = at >= 0 ? railed[at + direction] : railed[direction > 0 ? 0 : railed.length - 1]
    if (next) router.push(streamHref(next.name, tab))
  }
  const previous = { disabled: at <= 0, run: () => step(-1) }
  const next = { disabled: railed.length === 0 || at >= railed.length - 1, run: () => step(1) }
  const filterLabel = STREAM_FILTERS.find((f) => f.key === chip)?.label
  const caption = streams.data
    ? `${chip !== "all" || query ? `${railed.length} of ${listed.length}` : `${listed.length} ${listed.length === 1 ? "stream" : "streams"}`}${chip !== "all" ? ` · ${filterLabel}` : " · worst first"}`
    : "Reading the streams…"

  return (
    <Workspace
      name="Stream"
      stateKey="proxy.stream.page"
      refresh={streams.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous stream",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next stream",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <StreamRail
            className="hidden w-72 shrink-0 xl:flex"
            streams={railed}
            current={name}
            tab={tab}
            loading={streams.loading}
            query={query}
            onQuery={setQuery}
            filter={chip !== "all" ? chip : undefined}
            onClearFilter={() => setChip("all")}
            caption={caption}
          />
          <StreamMain
            key={name}
            name={name}
            admin={admin}
            status={streams.data}
            error={streams.error}
            refresh={streams.refresh}
            tab={tab}
            setTab={setTab}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function StreamMain({
  name,
  admin,
  status,
  error,
  refresh,
  tab,
  setTab,
  previous,
  next,
}: {
  name: string
  admin: boolean
  status: StreamStatus | undefined
  error: Error | undefined
  refresh: () => void
  tab: StreamTab
  setTab: (tab: string) => void
  previous: Step
  next: Step
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

  if (!stream) {
    if (!status) {
      return (
        <div className="flex min-w-0 flex-1 flex-col gap-5">
          <PageContext eyebrow={BACK} title={name} />
          {error ? (
            <ErrorState error={error} onRetry={refresh} />
          ) : (
            <>
              <HostIdentity
                className="border-b-0 pb-0"
                fallback={Connection}
                title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
                facts={<span>&nbsp;</span>}
              />
              <LoadingRows />
            </>
          )}
        </div>
      )
    }
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title={name} />
        <EmptyState
          icon={Connection}
          title={`No stream called ${name}`}
          description="It may have been deleted or renamed. The Streams list has every forward on this host."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href="/proxy/streams">All streams</Link>
            </Button>
          }
        />
      </div>
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

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={stream.name}
          actions={
            <>
              {Array.isArray(tested) && <TestResults name={stream.name} results={tested} />}
              {primary && (
                <Button
                  size="sm"
                  onClick={primary.run}
                  disabled={primary.disabled}
                  pending={primary.disabled && primary.key !== "recheck"}
                >
                  <primary.icon className="size-4" />
                  {primary.label}
                </Button>
              )}
              {rest.length > 0 && (
                <VerbMenu verbs={rest} label={`More actions for ${stream.name}`} />
              )}
              <span className="flex items-center">
                <IconAction
                  label="Previous stream"
                  disabled={previous.disabled}
                  onClick={previous.run}
                >
                  <ChevronUp />
                </IconAction>
                <IconAction label="Next stream" disabled={next.disabled} onClick={next.run}>
                  <ChevronDown />
                </IconAction>
                <span className="max-sm:hidden">
                  <WorkspaceHelp compact />
                </span>
              </span>
            </>
          }
        />
        <StreamIdentity stream={stream} />

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
      </div>

      <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <div className="flex shrink-0 flex-wrap items-end justify-between gap-x-4 gap-y-2 border-b border-hairline">
          <TabsPrimitive.List
            aria-label={`Views of ${stream.name}`}
            className="flex min-w-0 [scrollbar-width:none] gap-1 overflow-x-auto [&::-webkit-scrollbar]:hidden"
          >
            {views.map((key) => (
              <TabsPrimitive.Trigger
                key={key}
                value={key}
                className={tabClasses(tab === key, "h-10")}
              >
                {TAB_LABEL[key]}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>
          {(tab === "overview" || tab === "clients") && readable && (
            <Segments
              label="Window"
              value={span}
              onChange={setSpan}
              options={WINDOWS.map((w) => ({ value: w.value, label: w.label }))}
              className="mb-1.5"
            />
          )}
        </div>

        <TabsContent value="overview" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
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
        <TabsContent value="clients" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
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
        <TabsContent value="connections" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "connections" && readable && <StreamConnections name={name} state={sessions} />}
        </TabsContent>
        {admin && (
          <TabsContent value="settings" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
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
    </div>
  )
}

/**
 * The line the page opens on: the port as the service its number is with its
 * state in the tile's corner, the stream's name and where it listens, then
 * what kind of forward it is, where it goes and who may reach it; its state
 * at the line's end.
 */
function StreamIdentity({ stream }: { stream: StreamEntry }) {
  const who = restrictionWords(stream)
  const family = listenFamily(stream.address)
  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={portProduct(stream.listen)}
            fallback={Connection}
            className="size-12 rounded-xl [&_img]:size-7"
          />
          <span
            aria-hidden
            className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={dotTone(stream)} live={stream.state === "live"} />
          </span>
        </span>
      }
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <span className="truncate">{stream.name}</span>
          {!stream.error && (
            <span
              className="numeric shrink-0 font-mono text-body font-normal text-muted-foreground"
              title={family ? `Every ${family} address` : undefined}
            >
              {listenLabel(stream)}
            </span>
          )}
        </span>
      }
      facts={
        stream.error ? (
          <span className="font-mono">{stream.path}</span>
        ) : (
          <>
            {streamTraits(stream).map((fact) => (
              <span key={fact} className="contents">
                <span>{fact}</span>
                <FactDot />
              </span>
            ))}
            <span className="font-mono">→ {upstreamsOf(stream).join(", ")}</span>
            <FactDot />
            <span
              className={cn(
                who.mono && "font-mono",
                who.tone === "danger" && "font-medium text-destructive",
                who.tone === "warning" && "text-warning",
              )}
            >
              {stream.open ? who.label : `from ${who.label}`}
            </span>
          </>
        )
      }
      aside={<Status {...stateStatus(stream)} />}
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
