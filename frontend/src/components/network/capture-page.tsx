"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Crosshair,
  Download,
  StopCircle,
} from "@/components/icons"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import {
  captureFinished,
  captureReading,
  type CaptureList,
  type CaptureRun,
} from "@/lib/network-captures"
import {
  captureFilter,
  captureLine,
  captureMatches,
  familyWord,
  neighbours,
  startedApart,
} from "@/lib/network-runs-view"
import { useSessionState, useViewState } from "@/lib/view-state"
import type { NetworkLink } from "@/lib/types"
import {
  CAPTURE_QUERY_KEY,
  CaptureMenu,
  captureHref,
  useCaptureActions,
  useCaptureLinks,
} from "./captures"
import { CapturePicture } from "./capture-picture"
import { StateTile, interfaceMark, toolGlyph } from "./diagnostic-marks"
import { ItemRail } from "./item-rail"
import { NetworkReadWarning } from "./read-warning"
import { runHref } from "./saved-runs"

const TABS = ["overview", "facts"] as const
type Tab = (typeof TABS)[number]

const BACK = (
  <Link
    href="/network/captures"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Packet captures
  </Link>
)

/**
 * One packet capture, as a place of its own (the process page's shape):
 * a way back, the capture's line with its one command, the picture of what
 * it listened to and where the packets went, and the seventeen facts the
 * old pane listed behind a second view. From `xl` the list stands beside it,
 * and Alt+↑ and Alt+↓ walk it with the view kept.
 */
export function CapturePage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const params = useParams<{ id: string }>()
  const id = params.id ? decodeURIComponent(params.id) : ""
  const search = useSearchParams()
  const router = useRouter()
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("network.capture.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "overview"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // Replaced, not pushed: a view is not a place Back should step through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )

  const [query, setQuery] = useSessionState(CAPTURE_QUERY_KEY, "")
  const list = usePoll(
    (signal) => get<CaptureList>("/network/captures/", undefined, signal),
    5000,
    [admin],
    { enabled: admin },
  )
  const links = useCaptureLinks(admin)
  const rows = (list.data?.runs ?? []).filter((run) => captureMatches(run, query))
  const walk = neighbours(rows, id)
  const open = (next?: CaptureRun) => next && router.push(captureHref(next.id, tab))

  // The page's refresh key reads the item again as well as the list beside
  // it; the item's read lives in the pane, which hands its refresh up here.
  const rereadRef = useRef<() => void>(() => {})

  if (!admin)
    return (
      <Page>
        <PageContext eyebrow={BACK} title="Packet capture" />
        <EmptyState
          icon={Crosshair}
          title="Packet captures require the admin capability"
          description="Original packet bytes and private filter tuples are available only to server administrators."
        />
      </Page>
    )

  return (
    <Workspace
      name="Packet capture"
      stateKey="network.capture.page"
      refresh={() => {
        list.refresh()
        rereadRef.current()
      }}
      commands={[
        {
          id: "previous",
          label: "Previous capture",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: !walk.previous,
          run: () => open(walk.previous),
        },
        {
          id: "next",
          label: "Next capture",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: !walk.next,
          run: () => open(walk.next),
        },
      ]}
    >
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <ItemRail
            className="hidden w-72 shrink-0 xl:flex"
            label="Packet captures"
            current={id}
            loading={!list.data && !list.error}
            query={query}
            onQuery={setQuery}
            placeholder="Name, interface or address"
            caption={list.data ? plural(list.data.runs.length, "capture") : "Reading the captures…"}
            items={rows.map((run) => ({
              id: run.id,
              href: captureHref(run.id, tab),
              label: run.name,
              mark: (
                <StateTile
                  {...interfaceMark(run.request.interface, links)}
                  tone={captureReading(run).tone}
                  size="sm"
                />
              ),
              title: run.name,
              line: captureLine(run),
            }))}
          />
          <CaptureMain
            key={id}
            id={id}
            rereadRef={rereadRef}
            links={links}
            tab={tab}
            setTab={setTab}
            onListChanged={list.refresh}
            previous={{ disabled: !walk.previous, run: () => open(walk.previous) }}
            next={{ disabled: !walk.next, run: () => open(walk.next) }}
          />
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function CaptureMain({
  id,
  rereadRef,
  links,
  tab,
  setTab,
  onListChanged,
  previous,
  next,
}: {
  id: string
  rereadRef: React.RefObject<() => void>
  links?: NetworkLink[]
  tab: Tab
  setTab: (tab: string) => void
  onListChanged: () => void
  previous: Step
  next: Step
}) {
  const router = useRouter()
  const [done, setDone] = useState(false)
  const poll = usePoll(
    (signal) => get<CaptureRun>(`/network/captures/${encodeURIComponent(id)}`, undefined, signal),
    // A capture that has ended changes no more; it is read again only in
    // case it is deleted or expires under the open page.
    done ? 30_000 : 1500,
    [id],
  )
  const refresh = poll.refresh
  useEffect(() => {
    rereadRef.current = refresh
  }, [rereadRef, refresh])
  const run = poll.data
  const finished = run ? captureFinished(run) : false
  if (finished !== done) setDone(finished)
  const missing = poll.error instanceof ApiError && poll.error.status === 404

  const header = (
    <PageContext
      eyebrow={BACK}
      title={run?.name ?? "Packet capture"}
      actions={
        <span className="flex items-center">
          <IconAction label="Previous capture" disabled={previous.disabled} onClick={previous.run}>
            <ChevronUp />
          </IconAction>
          <IconAction label="Next capture" disabled={next.disabled} onClick={next.run}>
            <ChevronDown />
          </IconAction>
          <span className="max-sm:hidden">
            <WorkspaceHelp compact />
          </span>
        </span>
      }
    />
  )

  if (!run)
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {header}
        {missing ? (
          <EmptyState
            icon={Crosshair}
            title="No capture with that ID is kept"
            description="Captures expire after 24 hours, and a deleted one is gone with its packets."
            action={
              <Button asChild size="sm" variant="outline">
                <Link href="/network/captures">All captures</Link>
              </Button>
            }
          />
        ) : poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} retryable />
        ) : (
          <>
            <HostIdentity
              fallback={Crosshair}
              title={<TextShimmer>Reading the capture…</TextShimmer>}
              state={<span>&nbsp;</span>}
              facts={<span>&nbsp;</span>}
            />
            <LoadingRows rows={4} />
          </>
        )}
      </div>
    )

  return (
    <section aria-label="Packet capture" className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {header}
        <CaptureIdentity
          run={run}
          links={links}
          onChanged={() => {
            poll.refresh()
            onListChanged()
          }}
          onDeleted={() => {
            onListChanged()
            router.push("/network/captures")
          }}
        />
        <NetworkReadWarning
          error={poll.error}
          refresh={poll.refresh}
          lastSuccess={poll.lastSuccess}
          reading="capture evidence"
        />
      </div>
      <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${run.name}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {[
            { key: "overview", label: "Overview" },
            { key: "facts", label: "Facts" },
          ].map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(tab === view.key, "h-10")}
            >
              {view.label}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>
        <TabsContent value="overview" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "overview" && <CaptureOverview run={run} links={links} />}
        </TabsContent>
        <TabsContent value="facts" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "facts" && <CaptureFacts run={run} />}
        </TabsContent>
      </Tabs>
    </section>
  )
}

/**
 * The capture's line: its state, the interface and filter, what it kept and
 * who asked; Download as the command once there is a file, Stop while it
 * runs, the rest behind the menu.
 */
function CaptureIdentity({
  run,
  links,
  onChanged,
  onDeleted,
}: {
  run: CaptureRun
  links?: NetworkLink[]
  onChanged: () => void
  onDeleted: () => void
}) {
  const actions = useCaptureActions(run, { onChanged, onDeleted })
  const reading = captureReading(run)
  const filter = captureFilter(run.request)
  return (
    <>
      <HostIdentity
        label={`About ${run.name}`}
        logo={
          <StateTile
            {...interfaceMark(run.request.interface, links)}
            tone={reading.tone}
            size="lg"
          />
        }
        title={<span className="truncate">{run.name}</span>}
        state={<Status {...reading} className="text-body" />}
        facts={
          <>
            <span className="font-mono text-foreground">{run.request.interface}</span>
            <FactDot />
            <span>{filter.title}</span>
            {run.result && (
              <>
                <FactDot />
                <span className="numeric">
                  {plural(run.result.packets, "packet")} · {bytes(run.result.bytes)}
                </span>
              </>
            )}
            <FactDot />
            <time dateTime={run.createdAt} title={timestamp(run.createdAt)} className="numeric">
              {relativeTime(run.createdAt)}
            </time>
          </>
        }
        command={
          actions.pcap ? (
            <Button asChild size="sm">
              <a href={actions.pcap} download>
                <Download className="size-3.5" />
                Download PCAP
              </a>
            </Button>
          ) : actions.stop ? (
            <Button
              size="sm"
              variant="outline"
              disabled={actions.stopping}
              pending={actions.stopping && run.status !== "cancelling"}
              onClick={actions.stop}
            >
              <StopCircle className="size-3.5" />
              {actions.stopping ? "Stopping…" : "Stop capture"}
            </Button>
          ) : undefined
        }
        menu={
          <CaptureMenu
            run={run}
            actions={actions}
            omit={["pcap", "stop"]}
            label="Capture actions"
          />
        }
      />
      {actions.error && <ErrorState error={actions.error} />}
      {actions.dialog}
    </>
  )
}

function CaptureOverview({ run, links }: { run: CaptureRun; links?: NetworkLink[] }) {
  return (
    <div className="min-w-0 space-y-8">
      {run.error && (
        <Notice tone="warning" title="Capture incomplete">
          {run.error}
        </Notice>
      )}
      {run.status === "interrupted" && (
        <Notice tone="warning" title="Final cleanup is unverified">
          This capture was not restarted. A predecessor process is bounded by its independent host
          timeout; this record does not prove when that process stopped.
        </Notice>
      )}
      {run.status === "cancelling" && (
        <Notice tone="warning" title="Waiting for native cleanup">
          The record becomes cancelled after its runner returns from process-group cleanup.
        </Notice>
      )}
      <CapturePicture run={run} links={links} />
      {run.request.incidentRunId && <RelatedRun id={run.request.incidentRunId} capture={run} />}
    </div>
  )
}

/**
 * The saved run this capture was started for. The reference and the two
 * start times are all that connect them, and the line under the card says so.
 */
function RelatedRun({ id, capture }: { id: string; capture: CaptureRun }) {
  const poll = usePoll(
    (signal) =>
      get<{
        id: string
        name: string
        kind?: "investigation"
        request?: { tool: string }
        startedAt?: string
      }>(`/network/diagnostics/${encodeURIComponent(id)}`, undefined, signal),
    30000,
    [id],
  )
  const diagnostic = poll.data
  return (
    <Panel plain aria-label="Related saved run">
      <PanelHeader title="Related saved run" />
      <PanelBody className="space-y-2">
        {diagnostic ? (
          <>
            <NetworkReadWarning
              error={poll.error}
              refresh={poll.refresh}
              lastSuccess={poll.lastSuccess}
              reading="related diagnostic"
            />
            <ChoiceList>
              <ChoiceRow
                leading={
                  <StateTile
                    glyph={toolGlyph({
                      kind: diagnostic.kind,
                      request: diagnostic.request ?? { tool: "" },
                    })}
                    tone="notice"
                  />
                }
                title={diagnostic.name}
                verb={`Open saved run ${diagnostic.name}`}
                href={runHref(id)}
                description={
                  startedApart(diagnostic.startedAt, capture.startedAt) ??
                  (diagnostic.startedAt
                    ? `started ${timestamp(diagnostic.startedAt)}`
                    : "start not recorded")
                }
              />
            </ChoiceList>
            <p className="text-hint text-muted-foreground">
              The reference and timestamps connect the evidence. They do not establish the same flow
              or the cause of an incident.
            </p>
          </>
        ) : poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} />
        ) : (
          <LoadingRows rows={1} className="[&>*]:h-14 [&>*]:rounded-xl" />
        )}
      </PanelBody>
    </Panel>
  )
}

/** Everything the record holds, in the order a reader checks it. */
function CaptureFacts({ run }: { run: CaptureRun }) {
  const { request, result } = run
  const finished = captureFinished(run)
  const time = (value?: string, none = "Not recorded") =>
    value ? <time dateTime={value}>{timestamp(value)}</time> : none
  return (
    <div className="min-w-0 space-y-8">
      <DetailList aria-label="Capture facts" className="max-w-3xl">
        <Detail label="Interface">
          <span className="font-mono">{request.interface}</span>
          {result && (
            <span className="text-muted-foreground"> · index {result.interfaceIndex}</span>
          )}
        </Detail>
        <Detail label="Family">{familyWord(request.family)}</Detail>
        <Detail label="Protocol">{request.protocol}</Detail>
        <Detail label="Source filter">
          <span className="font-mono break-all">{request.source || "Any in this family"}</span>
        </Detail>
        <Detail label="Destination filter">
          <span className="font-mono break-all">{request.destination || "Any in this family"}</span>
        </Detail>
        <Detail label="Port filter">{request.port || "Any"}</Detail>
        <Detail label="Limits">
          {request.packets} packets · {request.seconds} s · {request.maxBytes} bytes
        </Detail>
        <Detail label="Snapshot">First {request.snapshotLength} bytes of each packet</Detail>
        <Detail label="Requested">
          {time(run.createdAt)} · {run.createdBy}
        </Detail>
        <Detail label="Started">{time(run.startedAt, "Not started")}</Detail>
        <Detail label="Ended">{time(run.endedAt, "Not ended")}</Detail>
        {result && (
          <>
            <Detail label="Observed packets">
              {result.packets}
              <span className="text-muted-foreground">
                {finished ? " · complete retained records" : " · provisional until capture ends"}
              </span>
            </Detail>
            <Detail label="PCAP bytes">
              {result.bytes}
              <span className="text-muted-foreground"> of {request.maxBytes}</span>
            </Detail>
            <Detail label="Kernel drops">
              <span
                className={
                  result.kernelDropped === undefined || result.kernelDropped > 0
                    ? "text-warning"
                    : undefined
                }
              >
                {result.kernelDropped ?? "Unknown"}
              </span>
              <span className="text-muted-foreground"> · only when the native tool reports it</span>
            </Detail>
            <Detail label="Last progress">{time(result.checkedAt)}</Detail>
            <Detail label="Stop reason">{result.stopReason.replaceAll("_", " ")}</Detail>
            <Detail label="Interface readback">
              {!finished
                ? "Awaiting final readback"
                : result.identityVerified
                  ? "Native index and attributes matched before and after"
                  : "Unknown or changed"}
            </Detail>
            {finished && (
              <Detail label="Native process cleanup">
                Exit {result.cleanup.exitCode} · TERM{" "}
                {result.cleanup.termSent ? "sent" : "not sent"} · KILL{" "}
                {result.cleanup.killSent ? "sent" : "not sent"}
              </Detail>
            )}
            <Detail label="Partial trailing packet">
              {result.partialPacket ? "Omitted from the artifact" : "None recorded"}
            </Detail>
            {result.sha256 && (
              <Detail label="Artifact SHA-256">
                <span className="font-mono break-all">{result.sha256}</span>
              </Detail>
            )}
          </>
        )}
      </DetailList>
      {run.limitations.length > 0 && (
        <Panel plain aria-label="Scope and limitations">
          <PanelHeader title="Scope and limitations" />
          <PanelBody>
            <ul className="space-y-1.5">
              {run.limitations.map((limit) => (
                <li key={limit} className="text-body text-muted-foreground">
                  {limit}
                </li>
              ))}
            </ul>
          </PanelBody>
        </Panel>
      )}
      {result?.nativeSummary && (
        <Panel plain aria-label="Native capture summary">
          <PanelHeader title="Native capture summary" />
          <PanelBody>
            <Well className="max-h-64 break-all whitespace-pre-wrap">{result.nativeSummary}</Well>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
