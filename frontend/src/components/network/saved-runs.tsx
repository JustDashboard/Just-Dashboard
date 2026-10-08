"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { PageContext } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import {
  diagnosticFinished,
  diagnosticReading,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { NetworkReadWarning } from "./read-warning"
import { RunInspector } from "./run-inspector"
import { RunRetention } from "./run-retention"

/** Reading register: retained observations, their lifecycle and bounded evidence. */
export function SavedRuns() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  const poll = usePoll(
    (signal) => get<DiagnosticRun[]>("/network/diagnostics/", undefined, signal),
    2500,
    [admin],
    { enabled: admin },
  )
  const runs = poll.data ?? []
  const selected = params.get("run") ?? runs[0]?.id
  const select = (id: string) =>
    router.replace(id ? `/network/runs?run=${encodeURIComponent(id)}` : "/network/runs")
  return (
    <>
      <PageContext
        eyebrow="Network"
        title="Saved runs"
        actions={
          <Button asChild variant="outline" size="sm">
            <Link href="/network/tools">Quick tools</Link>
          </Button>
        }
      />
      {!admin ? (
        <Notice title="Saved diagnostics need the admin capability">
          Retained tests can include private network targets and decoded packet fields.
        </Notice>
      ) : !poll.data ? (
        poll.error ? (
          <ErrorState error={poll.error} onRetry={poll.refresh} />
        ) : (
          <LoadingPanel plain />
        )
      ) : (
        <>
          <NetworkReadWarning
            error={poll.error}
            refresh={poll.refresh}
            lastSuccess={poll.lastSuccess}
            reading="saved diagnostic runs"
          />
          <StatGrid columns={4}>
            <StatTile
              label="Running"
              value={runs.filter((run) => !diagnosticFinished(run)).length}
              hint="up to four at once"
            />
            <StatTile
              label="Completed"
              value={runs.filter((run) => run.status === "completed").length}
              hint="tool completed; path not guaranteed"
            />
            <StatTile
              label="Failed"
              value={runs.filter((run) => run.status === "failed").length}
              tone={runs.some((run) => run.status === "failed") ? "warning" : "default"}
            />
            <StatTile
              label="Interrupted"
              value={runs.filter((run) => run.status === "interrupted").length}
              tone={runs.some((run) => run.status === "interrupted") ? "warning" : "default"}
              hint="never rerun automatically"
            />
          </StatGrid>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span className="text-section font-semibold">Saved runs</span>
            <Button variant="outline" size="sm" onClick={poll.refresh}>
              Refresh runs
            </Button>
          </div>
          {/* The history and focused inspector scroll independently in one workbench. */}
          <div className="grid min-w-0 overflow-hidden rounded-xl border bg-card lg:grid-cols-[19rem_minmax(0,1fr)]">
            <Pane
              flush
              className="max-h-80 border-b border-hairline lg:max-h-[48rem] lg:border-r lg:border-b-0"
            >
              <PaneHeader>
                <span className="text-body font-medium">{runs.length} retained runs</span>
              </PaneHeader>
              <div className="min-h-0 overflow-y-auto p-3">
                {runs.length ? (
                  <ChoiceList>
                    {runs.map((run) => {
                      const reading = diagnosticReading(run)
                      return (
                        <ChoiceRow
                          key={run.id}
                          title={run.name}
                          verb={`Open ${run.name}`}
                          href={`/network/runs?run=${encodeURIComponent(run.id)}`}
                          description={
                            <span className="break-all">
                              {run.request.tool} · {run.request.target || "this host"} ·{" "}
                              {new Date(run.createdAt).toLocaleString()}
                            </span>
                          }
                          trailing={<Status label={reading.label} tone={reading.tone} />}
                          className={run.id === selected ? "bg-accent" : undefined}
                        />
                      )
                    })}
                  </ChoiceList>
                ) : (
                  <EmptyNote>No saved runs. Use Run and save on a tool to start one.</EmptyNote>
                )}
              </div>
            </Pane>
            <Pane flush className="min-h-72 lg:max-h-[48rem]">
              <div className="min-h-0 overflow-y-auto p-4 sm:p-6">
                {selected ? (
                  <RunInspector
                    key={selected}
                    id={selected}
                    runs={runs}
                    onChanged={poll.refresh}
                    onSelected={select}
                  />
                ) : (
                  <EmptyNote>Choose a retained run to read its stages, scope and result.</EmptyNote>
                )}
              </div>
            </Pane>
          </div>
          <RunRetention onChanged={poll.refresh} />
        </>
      )}
    </>
  )
}
