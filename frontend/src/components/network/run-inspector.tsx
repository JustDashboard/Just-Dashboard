"use client"

import { PathReport } from "./path-report"
import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { usePoll } from "@/hooks/use-poll"
import { del, get, getText, patch, post } from "@/lib/api"
import {
  diagnosticDuration,
  diagnosticFinished,
  diagnosticNameProblem,
  diagnosticReading,
  hasEvidence,
  resultReading,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { notify } from "@/lib/toast"
import { NetworkReadWarning } from "./read-warning"
import { RunComparison } from "./run-comparison"
import { RunHistory } from "./run-history"
import { ProbeEvidence } from "./tools/probe-evidence"

export function RunInspector({
  id,
  runs,
  onChanged,
  onSelected,
}: {
  id: string
  runs: DiagnosticRun[]
  onChanged: () => void
  onSelected: (id: string) => void
}) {
  const poll = usePoll(
    (signal) =>
      get<DiagnosticRun>(`/network/diagnostics/${encodeURIComponent(id)}`, undefined, signal),
    2500,
    [id],
  )
  if (!poll.data)
    return poll.error ? (
      <ErrorState error={poll.error} onRetry={poll.refresh} />
    ) : (
      <LoadingPanel plain />
    )
  return (
    <div className="min-w-0 space-y-4">
      <NetworkReadWarning
        error={poll.error}
        refresh={poll.refresh}
        lastSuccess={poll.lastSuccess}
        reading="diagnostic result"
      />
      <RunDetails
        key={id}
        run={poll.data}
        runs={runs}
        onChanged={() => {
          poll.refresh()
          onChanged()
        }}
        onSelected={onSelected}
      />
    </div>
  )
}

function RunDetails({
  run,
  runs,
  onChanged,
  onSelected,
}: {
  run: DiagnosticRun
  runs: DiagnosticRun[]
  onChanged: () => void
  onSelected: (id: string) => void
}) {
  const [name, setName] = useState(run.name)
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<Error>()
  const { confirm, dialog } = useConfirm()
  const console_ = useJobConsole({ onSuccess: onChanged })
  const reading = diagnosticReading(run)
  const nameProblem = diagnosticNameProblem(name)
  const finished = diagnosticFinished(run)
  const act = async (verb: string, action: () => Promise<void>) => {
    if (busy) return
    setBusy(verb)
    setError(undefined)
    try {
      await action()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(undefined)
    }
  }
  const exportRun = async () => {
    const text = await getText(`/network/diagnostics/${encodeURIComponent(run.id)}/export`)
    const url = URL.createObjectURL(new Blob([text], { type: "application/json" }))
    const link = document.createElement("a")
    link.href = url
    link.download = `network-diagnostic-${run.id}.json`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <>
      <Panel plain aria-label="Saved diagnostic">
        <PanelHeader
          title={run.name}
          actions={<Status label={reading.label} tone={reading.tone} />}
        />
        <PanelBody className="space-y-4">
          <div className="flex flex-wrap gap-2">
            {!finished && (
              <Button
                variant="outline"
                disabled={Boolean(busy) || run.status === "cancelling"}
                pending={busy === "cancel"}
                onClick={() =>
                  void act("cancel", async () => {
                    await post(`/network/diagnostics/${run.id}/cancel`, {})
                  })
                }
              >
                {run.status === "cancelling" ? "Stopping…" : "Cancel run"}
              </Button>
            )}
            {finished && (
              <Button
                variant="outline"
                disabled={Boolean(busy)}
                pending={busy === "rerun"}
                onClick={() =>
                  void act("rerun", async () => {
                    const next = await post<DiagnosticRun>(
                      `/network/diagnostics/${run.id}/rerun`,
                      {},
                    )
                    onSelected(next.id)
                  })
                }
              >
                Rerun
              </Button>
            )}
            <Button
              variant="outline"
              disabled={Boolean(busy)}
              pending={busy === "export"}
              onClick={() => void act("export", exportRun)}
            >
              Export JSON
            </Button>
            {run.jobId && (
              <Button variant="ghost" onClick={() => void console_.open(run.jobId!)}>
                Watch job
              </Button>
            )}
            {finished && (
              <Button
                variant="ghost"
                disabled={Boolean(busy)}
                onClick={() =>
                  confirm({
                    title: "Delete saved run",
                    description: `Delete the retained request and result for ${run.name}.`,
                    confirmLabel: "Delete run",
                    action: async () => {
                      await del(`/network/diagnostics/${run.id}`)
                    },
                    onDone: () => {
                      onSelected("")
                      onChanged()
                    },
                  })
                }
              >
                Delete
              </Button>
            )}
          </div>
          {error && <ErrorState error={error} />}
          {run.status === "interrupted" && (
            <Notice tone="warning" title="Interrupted at backend restart or shutdown">
              This run was not rerun automatically. An abrupt restart does not independently verify
              final host-process cleanup.
            </Notice>
          )}
          {run.status === "cancelling" && (
            <Notice tone="warning" title="Waiting for the diagnostic to stop">
              Cancellation has been requested. The run becomes cancelled after its runner returns
              from cleanup.
            </Notice>
          )}
          {run.resultTruncated && (
            <Notice tone="warning" title="Retained result is truncated">
              Output and structured records are bounded. Export and comparison contain only this
              retained evidence.
            </Notice>
          )}
          <DetailList>
            <Detail label="Diagnostic">
              {run.kind === "investigation" ? "Connection path" : run.request.tool}
            </Detail>
            <Detail label="Target">
              <span className="font-mono break-all">
                {run.investigationRequest?.target || run.request.target || "this host"}
              </span>
            </Detail>
            <Detail label="Vantage">
              {run.scope.vantage === "dashboard_host" ? "This server" : run.scope.vantage}
            </Detail>
            <Detail label="Family">
              {run.scope.family === "resolved_at_execution"
                ? "Resolved when the tool runs"
                : run.scope.family === "not_applicable"
                  ? "Not applicable"
                  : run.scope.family === "inet6"
                    ? "IPv6"
                    : run.scope.family === "inet"
                      ? "IPv4"
                      : run.scope.family}
            </Detail>
            {run.scope.source && <Detail label="Source">{run.scope.source}</Detail>}
            {run.scope.sourceAddress && (
              <Detail label="Source address">{run.scope.sourceAddress}</Detail>
            )}
            {run.scope.address && <Detail label="Selected destination">{run.scope.address}</Detail>}
            {run.scope.mark && <Detail label="Mark">{run.scope.mark}</Detail>}
            {run.scope.protocol && (
              <Detail label="Protocol">{run.scope.protocol.replaceAll("_", " ")}</Detail>
            )}
            {run.scope.port && <Detail label="Port">{run.scope.port}</Detail>}
            {run.scope.interface && <Detail label="Interface">{run.scope.interface}</Detail>}
            {run.request.record && <Detail label="Record type">{run.request.record}</Detail>}
            {run.request.option && <Detail label="Option">{run.request.option}</Detail>}
            <Detail label="Created">
              <RunTime value={run.createdAt} />
            </Detail>
            <Detail label="Started">
              <RunTime value={run.startedAt} />
            </Detail>
            <Detail label="Ended">
              <RunTime value={run.endedAt} />
            </Detail>
            <Detail label="Requested by">{run.createdBy}</Detail>
            <Detail label="Duration">{diagnosticDuration(run)}</Detail>
            <Detail label="Outcome source">
              {run.outcomeSource?.replaceAll("_", " ") || "Awaiting outcome"}
            </Detail>
          </DetailList>
          {run.outcomeSource === "error_text" && (
            <p className="text-hint text-muted-foreground">
              The outcome category is inferred from the tool&rsquo;s error text. Inspect the
              evidence to verify the cause.
            </p>
          )}
          <ol aria-label="Diagnostic stages" className="divide-y divide-hairline">
            {run.stages.map((stage) => (
              <li
                key={stage.id}
                className="flex min-w-0 flex-wrap items-baseline justify-between gap-2 py-2"
              >
                <span className="text-body capitalize">{stage.id}</span>
                <span className="text-hint text-muted-foreground">
                  {stage.status} · <RunTime value={stage.endedAt ?? stage.startedAt} />
                </span>
              </li>
            ))}
          </ol>
          {run.investigation && <PathReport result={run.investigation} />}
          {run.result && hasEvidence(run.result) && (
            <div aria-label="Structured evidence" className="space-y-2">
              <Status
                label={resultReading(run.result).label}
                tone={resultReading(run.result).tone}
              />
              <ProbeEvidence result={run.result} />
            </div>
          )}
          {Boolean(run.result?.records?.length) && (
            <div aria-label="Structured result">
              <p className="mb-2 text-body font-medium">Structured records</p>
              <Well className="max-h-72 break-all whitespace-pre-wrap">
                {run.result!.records!.join("\n")}
              </Well>
            </div>
          )}
          {!run.hasResult && (
            <p className="text-body text-muted-foreground">
              {finished
                ? "No answer was retained. The lifecycle and error evidence are available below."
                : "Awaiting the tool’s result."}
            </p>
          )}
          <details className="text-body">
            <summary className="cursor-pointer focus-ring">Bounded tool evidence</summary>
            <div className="mt-3 space-y-3">
              {run.error && (
                <Well className="max-h-48 break-all whitespace-pre-wrap" aria-label="Probe error">
                  {run.error}
                </Well>
              )}
              {run.result?.output ? (
                <Well className="max-h-80 break-all whitespace-pre-wrap">{run.result.output}</Well>
              ) : (
                <p className="text-hint text-muted-foreground">No tool output recorded.</p>
              )}
              <p className="font-mono text-hint break-all">
                Run: {run.id}
                {run.rerunOf ? ` · rerun of ${run.rerunOf}` : ""}
              </p>
            </div>
          </details>
          {run.scope.limitations.map((line) => (
            <p key={line} className="text-hint text-muted-foreground">
              {line}
            </p>
          ))}
          <div className="flex flex-wrap items-end gap-3">
            <Field
              label="Saved run name"
              htmlFor="saved-run-name"
              error={nameProblem}
              className="min-w-0 flex-1"
            >
              <Input
                id="saved-run-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                aria-invalid={Boolean(nameProblem)}
              />
            </Field>
            <Button
              variant="outline"
              disabled={Boolean(busy) || Boolean(nameProblem) || name.trim() === run.name}
              pending={busy === "save"}
              onClick={() =>
                void act("save", async () => {
                  await patch(`/network/diagnostics/${run.id}`, { name: name.trim() })
                  notify.success("Run name saved")
                })
              }
            >
              Save name
            </Button>
          </div>
        </PanelBody>
      </Panel>
      {finished && run.hasResult && <RunComparison run={run} runs={runs} />}
      {finished && run.hasResult && run.kind !== "investigation" && <RunHistory run={run} />}
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
      {dialog}
    </>
  )
}

function RunTime({ value }: { value?: string }) {
  return value ? (
    <time dateTime={value}>{new Date(value).toLocaleString()}</time>
  ) : (
    <>Not recorded</>
  )
}
