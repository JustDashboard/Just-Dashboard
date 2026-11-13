"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, Notes } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, get } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import {
  diagnosticCompatible,
  diagnosticDuration,
  diagnosticFinished,
  diagnosticReading,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { neighbours, runRequestLine, runTool } from "@/lib/network-runs-view"
import { useViewState } from "@/lib/view-state"
import { StateTile, toolGlyph } from "./diagnostic-marks"
import { ItemRail } from "./item-rail"
import { NetworkReadWarning } from "./read-warning"
import { useRunActions } from "./run-actions"
import { RunComparison } from "./run-comparison"
import { RunHistory } from "./run-history"
import { RunDetails, RunResult } from "./run-inspector"
import { RunChips, runHref, useRunList } from "./saved-runs"

const TABS = ["result", "compare", "history", "details"] as const
type Tab = (typeof TABS)[number]

const BACK = (
  <Link
    href="/network/runs"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Saved runs
  </Link>
)

/**
 * One saved run, as a place of its own (the process page's shape): a way
 * back, the run's line with Rerun as its command, and its views — the result
 * the tool drew, the comparison with an earlier run of the same request, the
 * history of that request, and the request's own record. From `xl` the list
 * stands beside it with its chips and search; Alt+↑ and Alt+↓ walk it.
 */
export function RunPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const params = useParams<{ id: string }>()
  const id = params.id ? decodeURIComponent(params.id) : ""
  const search = useSearchParams()
  const router = useRouter()
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("network.run.tab", "result")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "result"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // Replaced, not pushed: a view is not a place Back should step through.
      const url = new URL(window.location.href)
      if (next === "result") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )
  const list = useRunList(admin, 5000)
  const walk = neighbours(list.shown, id)
  const open = (next?: DiagnosticRun) => next && router.push(runHref(next.id, tab))

  if (!admin)
    return (
      <Page>
        <PageContext eyebrow={BACK} title="Saved run" />
        <EmptyState
          icon={Notes}
          title="Saved diagnostics need the admin capability"
          description="Retained tests can include private network targets and decoded packet fields."
        />
      </Page>
    )

  return (
    <Workspace
      name="Saved run"
      stateKey="network.run.page"
      refresh={list.poll.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous run",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: !walk.previous,
          run: () => open(walk.previous),
        },
        {
          id: "next",
          label: "Next run",
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
            className="hidden w-80 shrink-0 xl:flex"
            label="Saved runs"
            current={id}
            loading={!list.poll.data && !list.poll.error}
            query={list.query}
            onQuery={list.setQuery}
            placeholder="Name, tool or target"
            filters={<RunChips list={list} className="flex flex-wrap gap-1" />}
            caption={
              list.poll.data
                ? list.shown.length === list.runs.length
                  ? plural(list.runs.length, "run")
                  : `${list.shown.length} of ${plural(list.runs.length, "run")}`
                : "Reading the saved runs…"
            }
            items={list.shown.map((run) => ({
              id: run.id,
              href: runHref(run.id, tab),
              label: run.name,
              mark: (
                <StateTile glyph={toolGlyph(run)} tone={diagnosticReading(run).tone} size="sm" />
              ),
              title: run.name,
              line: `${runTool(run)} · ${runRequestLine(run)}`,
            }))}
          />
          <RunMain
            key={id}
            id={id}
            runs={list.runs}
            tab={tab}
            setTab={setTab}
            onListChanged={list.poll.refresh}
            previous={{ disabled: !walk.previous, run: () => open(walk.previous) }}
            next={{ disabled: !walk.next, run: () => open(walk.next) }}
          />
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function RunMain({
  id,
  runs,
  tab,
  setTab,
  onListChanged,
  previous,
  next,
}: {
  id: string
  runs: DiagnosticRun[]
  tab: Tab
  setTab: (tab: string) => void
  onListChanged: () => void
  previous: Step
  next: Step
}) {
  const [done, setDone] = useState(false)
  const poll = usePoll(
    (signal) =>
      get<DiagnosticRun>(`/network/diagnostics/${encodeURIComponent(id)}`, undefined, signal),
    // A run that has ended changes only by its name; it is read again slowly
    // in case it is renamed elsewhere, deleted or expires.
    done ? 15_000 : 2500,
    [id],
  )
  const run = poll.data
  const finished = run ? diagnosticFinished(run) : false
  if (finished !== done) setDone(finished)
  const missing = poll.error instanceof ApiError && poll.error.status === 404

  const header = (
    <PageContext
      eyebrow={BACK}
      title={run?.name ?? "Saved run"}
      actions={
        <span className="flex items-center">
          <IconAction label="Previous run" disabled={previous.disabled} onClick={previous.run}>
            <ChevronUp />
          </IconAction>
          <IconAction label="Next run" disabled={next.disabled} onClick={next.run}>
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
            icon={Notes}
            title="No saved run with that ID is kept"
            description="Runs past the retention policy are erased, and a deleted one is gone."
            action={
              <Button asChild size="sm" variant="outline">
                <Link href="/network/runs">All saved runs</Link>
              </Button>
            }
          />
        ) : poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} />
        ) : (
          <>
            <HostIdentity
              fallback={Notes}
              title={<TextShimmer>Reading the run…</TextShimmer>}
              state={<span>&nbsp;</span>}
              facts={<span>&nbsp;</span>}
            />
            <LoadingRows rows={4} />
          </>
        )}
      </div>
    )

  return (
    <RunView
      run={run}
      runs={runs}
      tab={tab}
      setTab={setTab}
      header={header}
      warning={
        <NetworkReadWarning
          error={poll.error}
          refresh={poll.refresh}
          lastSuccess={poll.lastSuccess}
          reading="diagnostic result"
        />
      }
      onChanged={() => {
        poll.refresh()
        onListChanged()
      }}
    />
  )
}

function RunView({
  run,
  runs,
  tab,
  setTab,
  header,
  warning,
  onChanged,
}: {
  run: DiagnosticRun
  runs: DiagnosticRun[]
  tab: Tab
  setTab: (tab: string) => void
  header: React.ReactNode
  warning: React.ReactNode
  onChanged: () => void
}) {
  const router = useRouter()
  const actions = useRunActions(run, {
    onChanged,
    onRerun: (id) => router.push(runHref(id, tab)),
    onDeleted: () => {
      onChanged()
      router.push("/network/runs")
    },
    page: true,
  })
  const reading = diagnosticReading(run)
  const finished = diagnosticFinished(run)
  const comparable = finished && run.hasResult
  const candidates = comparable ? runs.filter((other) => diagnosticCompatible(other, run)) : []
  const views = [
    { key: "result", label: "Result" },
    ...(comparable ? [{ key: "compare", label: "Compare", count: candidates.length }] : []),
    ...(comparable && run.kind !== "investigation" ? [{ key: "history", label: "History" }] : []),
    { key: "details", label: "Details" },
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "result"
  const command = actions.verbs.find((verb) => verb.key === "rerun" || verb.key === "cancel")
  const menu = actions.verbs.filter((verb) => verb !== command)
  return (
    <section aria-label="Saved diagnostic" className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {header}
        <HostIdentity
          label={`About ${run.name}`}
          logo={<StateTile glyph={toolGlyph(run)} tone={reading.tone} size="lg" />}
          title={<span className="truncate">{run.name}</span>}
          state={<Status label={reading.label} tone={reading.tone} className="text-body" />}
          facts={
            <>
              <span className="text-foreground">{runTool(run)}</span>
              <FactDot />
              <span className="min-w-0 truncate font-mono">{runRequestLine(run)}</span>
              <FactDot />
              <span>
                {run.scope.vantage === "dashboard_host"
                  ? "from this server"
                  : run.scope.source
                    ? `from ${run.scope.source}`
                    : run.scope.vantage.replaceAll("_", " ")}
              </span>
              {finished && diagnosticDuration(run) !== "Not recorded" && (
                <>
                  <FactDot />
                  <span className="numeric">{diagnosticDuration(run)}</span>
                </>
              )}
              <FactDot />
              <span className="numeric">
                <time dateTime={run.createdAt}>{timestamp(run.createdAt)}</time> by {run.createdBy}
              </span>
            </>
          }
          command={
            command && (
              <Button
                size="sm"
                variant={command.key === "cancel" ? "outline" : "default"}
                disabled={command.disabled}
                pending={actions.busy === command.key}
                onClick={command.run}
              >
                <command.icon className="size-3.5" />
                {command.label}
              </Button>
            )
          }
          menu={<VerbMenu verbs={menu} label="Run actions" />}
        />
        {warning}
        {actions.error && <ErrorState error={actions.error} />}
        {actions.console}
      </div>
      <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${run.name}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(shown === view.key, "h-10")}
            >
              {view.label}
              {"count" in view && view.count !== undefined && view.count > 0 && (
                <ChipCount>{view.count}</ChipCount>
              )}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>
        <TabsContent value="result" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "result" && <RunResult run={run} />}
        </TabsContent>
        <TabsContent value="compare" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "compare" && <RunComparison run={run} runs={runs} />}
        </TabsContent>
        <TabsContent value="history" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "history" && <RunHistory run={run} />}
        </TabsContent>
        <TabsContent value="details" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "details" && <RunDetails run={run} />}
        </TabsContent>
      </Tabs>
      {actions.dialogs}
    </section>
  )
}
