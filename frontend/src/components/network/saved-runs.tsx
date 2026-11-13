"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Connection, Notes, RefreshClockwise, SettingsSliders, Wrench } from "@/components/icons"
import { WireMark } from "@/components/deploy/wire"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import {
  diagnosticFinished,
  diagnosticReading,
  type DiagnosticRetention,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import {
  RUN_SCOPE_WORDS,
  runChips,
  runCounts,
  runMatches,
  runRequestLine,
  runTool,
  type RunScope,
} from "@/lib/network-runs-view"
import { useSessionState } from "@/lib/view-state"
import { StateTile, toolGlyph } from "./diagnostic-marks"
import { NetworkReadWarning } from "./read-warning"
import { useRunActions } from "./run-actions"
import { RetentionDialog, retentionWords } from "./run-retention"

/** The list's search and chip, shared with the rail on a run's page. */
export const RUN_QUERY_KEY = "network.runs.query"
export const RUN_SCOPE_KEY = "network.runs.scope"

export const runHref = (id: string, tab?: string) =>
  `/network/runs/${encodeURIComponent(id)}${tab && tab !== "result" ? `?tab=${tab}` : ""}`

/** The list as the page and the rail both read it: polled, narrowed, counted. */
export function useRunList(admin: boolean, interval = 2500) {
  const [query, setQuery] = useSessionState(RUN_QUERY_KEY, "")
  const [scope, setScope] = useSessionState<RunScope>(RUN_SCOPE_KEY, "all")
  const poll = usePoll(
    (signal) => get<DiagnosticRun[]>("/network/diagnostics/", undefined, signal),
    interval,
    [admin],
    { enabled: admin },
  )
  const runs = poll.data ?? []
  const counts = runCounts(runs)
  const chips = runChips(counts, scope)
  return {
    poll,
    runs,
    shown: runs.filter((run) => runMatches(run, scope, query)),
    counts,
    chips,
    query,
    setQuery,
    scope,
    setScope,
  }
}

/** The list's chips: what the four tiles counted, now what narrows the list. */
export function RunChips({
  list,
  className,
}: {
  list: ReturnType<typeof useRunList>
  className?: string
}) {
  return (
    <div role="group" aria-label="Run states" className={className ?? "flex flex-wrap gap-1.5"}>
      {list.chips.map((scope) => (
        <FilterChip
          key={scope}
          selected={list.scope === scope}
          onClick={() => list.setScope(scope)}
        >
          {RUN_SCOPE_WORDS[scope]}
          <ChipCount>{list.counts[scope]}</ChipCount>
        </FilterChip>
      ))}
    </div>
  )
}

/**
 * Reading register: the diagnostics a tool kept, what each found, and the
 * probes the watch list checks on its schedule.
 *
 * It opened on four grey tiles counting run states over a framed list beside
 * an inspector. The counts are the list's chips now, the running count the
 * identity line's state, and a run is a page of its own (`run-page.tsx`).
 */
export function SavedRuns({ watched }: { watched: React.ReactNode }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  // The address that selected a run beside the list — Tools' Run and save,
  // a block's incident, a capture's related run — opens its page.
  const legacy = params.get("run")
  useEffect(() => {
    if (legacy) router.replace(runHref(legacy))
  }, [legacy, router])
  const list = useRunList(admin && !legacy)
  const [retention, setRetention] = useState(false)
  const policy = usePoll(
    (signal) => get<DiagnosticRetention>("/network/diagnostics/policy", undefined, signal),
    60_000,
    [admin],
    { enabled: admin },
  )

  if (!admin)
    return (
      <>
        <PageContext title="Saved runs" />
        <EmptyState
          icon={Notes}
          title="Saved diagnostics need the admin capability"
          description="Retained tests can include private network targets and decoded packet fields."
          action={
            <Button asChild size="sm" variant="outline">
              <Link href="/network/tools">Run a tool</Link>
            </Button>
          }
        />
        {watched}
      </>
    )

  const { poll, runs, shown } = list
  const running = list.counts.running
  return (
    <>
      <PageContext title="Saved runs" />
      <HostIdentity
        label="About saved runs"
        fallback={Notes}
        title="Saved runs"
        state={
          poll.data ? (
            running > 0 ? (
              <Status tone="running" label={`${running} running`} />
            ) : (
              <Status tone="stopped" label="Nothing running" />
            )
          ) : (
            <TextShimmer className="text-xs">Reading…</TextShimmer>
          )
        }
        facts={
          poll.data ? (
            <>
              <span className="numeric">{plural(runs.length, "run")} kept</span>
              {policy.data && (
                <>
                  <FactDot />
                  <button
                    type="button"
                    onClick={() => setRetention(true)}
                    className="rounded-sm focus-ring hover:text-foreground hover:underline"
                  >
                    {retentionWords(policy.data)}
                  </button>
                </>
              )}
              {list.counts.failed > 0 && (
                <>
                  <FactDot />
                  <span className="text-warning">{list.counts.failed} failed</span>
                </>
              )}
            </>
          ) : (
            <span>&nbsp;</span>
          )
        }
        command={
          <Button asChild size="sm">
            <Link href="/network/tools">
              <Wrench className="size-3.5" />
              Run a tool
            </Link>
          </Button>
        }
        menu={
          <VerbMenu
            label="Saved run actions"
            verbs={[
              { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: poll.refresh },
              {
                key: "retention",
                label: "Retention policy",
                icon: SettingsSliders,
                run: () => setRetention(true),
              },
            ]}
          />
        }
      />
      {poll.data && (
        <NetworkReadWarning
          error={poll.error}
          refresh={poll.refresh}
          lastSuccess={poll.lastSuccess}
          reading="saved diagnostic runs"
        />
      )}
      {!poll.data && poll.error ? (
        <ErrorState error={poll.error} onRetry={poll.refresh} />
      ) : poll.data && runs.length === 0 ? (
        <EmptyState
          icon={Notes}
          title="No saved runs"
          description="Run a tool and save it to keep its result, rerun it and compare."
          action={
            <Button asChild size="sm">
              <Link href="/network/tools">
                <Wrench className="size-3.5" />
                Run a tool
              </Link>
            </Button>
          }
        />
      ) : (
        <Panel plain aria-label="Runs">
          <PanelHeader
            title="Runs"
            actions={
              <SearchInput
                value={list.query}
                onChange={(event) => list.setQuery(event.target.value)}
                placeholder="Name, tool or target"
                aria-label="Search the saved runs"
              />
            }
          />
          <PanelBody className="space-y-3">
            {/* The chips' row is held while the list loads, so the cards land where
                their grey places stood. */}
            {poll.data ? <RunChips list={list} /> : <div aria-hidden className="h-7" />}
            {!poll.data ? (
              <LoadingRows rows={4} className="[&>*]:h-14 [&>*]:rounded-xl" />
            ) : shown.length === 0 ? (
              <EmptyNote>
                No run matches.{" "}
                <button
                  type="button"
                  className="rounded-sm font-medium text-foreground focus-ring hover:underline"
                  onClick={() => {
                    list.setQuery("")
                    list.setScope("all")
                  }}
                >
                  Show every run
                </button>
              </EmptyNote>
            ) : (
              <ChoiceList aria-label="Saved runs">
                {shown.map((run) => (
                  <RunRow key={run.id} run={run} onChanged={poll.refresh} />
                ))}
              </ChoiceList>
            )}
          </PanelBody>
        </Panel>
      )}
      {watched}
      <RetentionDialog
        open={retention}
        onOpenChange={setRetention}
        policy={policy.data}
        error={policy.error}
        onRetry={policy.refresh}
        onChanged={() => {
          policy.refresh()
          poll.refresh()
        }}
      />
    </>
  )
}

function RunRow({ run, onChanged }: { run: DiagnosticRun; onChanged: () => void }) {
  const router = useRouter()
  const reading = diagnosticReading(run)
  const actions = useRunActions(run, {
    onChanged,
    onRerun: (id) => router.push(runHref(id)),
    onDeleted: onChanged,
    page: false,
  })
  return (
    <ChoiceRow
      workspaceItem={{ id: run.id, name: run.name }}
      leading={<StateTile glyph={toolGlyph(run)} tone={reading.tone} />}
      title={run.name}
      verb={`Open ${run.name}`}
      href={runHref(run.id)}
      busy={!diagnosticFinished(run)}
      description={
        <>
          {`${runTool(run)} · `}
          <span className="font-mono">{runRequestLine(run)}</span>
        </>
      }
      trailing={
        <span className="flex items-center gap-4">
          <time
            dateTime={run.createdAt}
            className="numeric w-36 truncate text-right text-hint text-muted-foreground max-lg:hidden"
          >
            {timestamp(run.createdAt)}
          </time>
          <span className="flex w-44 justify-end max-sm:hidden">
            <Status label={reading.label} tone={reading.tone} />
          </span>
        </span>
      }
      actions={
        <>
          <VerbMenu label={`Actions for ${run.name}`} verbs={actions.verbs} />
          {actions.dialogs}
        </>
      }
    />
  )
}

/**
 * Nothing watched yet: the probe's mark, one sentence, and the step that
 * makes one — a port check on Tools, watched from its result.
 */
export function WatchedProbesEmpty() {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-3">
      <WireMark tone="logo" size="sm">
        <Connection aria-hidden />
      </WireMark>
      <p className="min-w-0 flex-1 text-body text-muted-foreground">
        No probe is watched. Watch a port check&rsquo;s result to have it checked on the watch
        list&rsquo;s schedule.
      </p>
      <Button asChild size="sm" variant="outline">
        <Link href="/network/tools?tool=port">Port check</Link>
      </Button>
    </div>
  )
}
