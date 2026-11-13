"use client"

import Link from "next/link"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { timestamp } from "@/lib/format"
import {
  diagnosticDuration,
  diagnosticFinished,
  hasEvidence,
  resultReading,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"
import { runTarget, runTool } from "@/lib/network-runs-view"
import { cn } from "@/lib/utils"
import { PathReport } from "./path-report"
import { ProbeEvidence } from "./tools/probe-evidence"

/**
 * A saved run's views, for its page (`run-page.tsx`). It was one inspector
 * pane beside the list: buttons, notices, nineteen facts, the stages, the
 * result, a disclosure of the tool's output and a rename field, one under the
 * other. The result is the first view now, drawn by the tool's own renderer —
 * the connection path's report or the probe's evidence, the same components
 * Tools and the investigator draw — and the request's facts and the tool's
 * raw output are the Details view.
 */
export function RunResult({ run }: { run: DiagnosticRun }) {
  const finished = diagnosticFinished(run)
  return (
    <div className="min-w-0 space-y-6">
      {run.status === "interrupted" && (
        <Notice tone="warning" title="Interrupted at backend restart or shutdown">
          This run was not rerun automatically. An abrupt restart does not independently verify
          final host-process cleanup.
        </Notice>
      )}
      {run.status === "cancelling" && (
        <Notice tone="warning" title="Waiting for the diagnostic to stop">
          Cancellation has been requested. The run becomes cancelled after its runner returns from
          cleanup.
        </Notice>
      )}
      {run.resultTruncated && (
        <Notice tone="warning" title="Retained result is truncated">
          Output and structured records are bounded. Export and comparison contain only this
          retained evidence.
        </Notice>
      )}
      <RunStages run={run} />
      {run.outcomeSource === "error_text" && (
        <p className="text-hint text-muted-foreground">
          The outcome category is inferred from the tool&rsquo;s error text. Inspect the evidence to
          verify the cause.
        </p>
      )}
      {run.investigation && <PathReport result={run.investigation} />}
      {run.result && hasEvidence(run.result) && (
        <div aria-label="Structured evidence" className="space-y-3">
          <Status label={resultReading(run.result).label} tone={resultReading(run.result).tone} />
          <ProbeEvidence result={run.result} />
        </div>
      )}
      {Boolean(run.result?.records?.length) && (
        <Panel plain aria-label="Structured result">
          <PanelHeader title="Structured records" />
          <PanelBody>
            <Well className="max-h-72 break-all whitespace-pre-wrap">
              {run.result!.records!.join("\n")}
            </Well>
          </PanelBody>
        </Panel>
      )}
      {!run.hasResult && (
        <p className="text-body text-muted-foreground">
          {finished ? "No answer was retained." : "Awaiting the tool’s result."}
        </p>
      )}
      {/* With no answer, what the tool said when it failed is the result. */}
      {!run.hasResult && run.error && (
        <Well className="max-h-48 break-all whitespace-pre-wrap" aria-label="Probe error">
          {run.error}
        </Well>
      )}
      {run.scope.limitations.length > 0 && (
        <ul className="space-y-1" aria-label="Scope limits">
          {run.scope.limitations.map((line) => (
            <li key={line} className="text-hint text-muted-foreground">
              {line}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

const STAGE_TONES: Record<string, DotTone> = {
  completed: "running",
  running: "notice",
  queued: "unknown",
  failed: "danger",
  cancelled: "stopped",
  interrupted: "warning",
}

/**
 * The run's lifecycle as a strip: validated, probed, recorded — each step a
 * dot and its word, joined by a line that is solid once the step is done.
 */
export function RunStages({ run }: { run: DiagnosticRun }) {
  return (
    <ol
      aria-label="Diagnostic stages"
      className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-2 text-hint"
    >
      {run.stages.map((stage, index) => {
        const tone = STAGE_TONES[stage.status] ?? "unknown"
        const when = stage.endedAt ?? stage.startedAt
        return (
          <li key={stage.id} className="flex min-w-0 items-center gap-2">
            {index > 0 && (
              <span
                aria-hidden
                className={cn(
                  "h-px w-6 shrink-0",
                  stage.status === "queued" ? "border-t border-dashed" : "bg-border",
                )}
              />
            )}
            <span
              className="inline-flex items-center gap-1.5"
              title={when ? timestamp(when) : undefined}
            >
              <StatusDot tone={tone} />
              <span className="font-medium capitalize">{stage.id}</span>
              <span className="text-muted-foreground">{stage.status}</span>
            </span>
          </li>
        )
      })}
    </ol>
  )
}

const FAMILY: Record<string, string> = {
  resolved_at_execution: "Resolved when the tool runs",
  not_applicable: "Not applicable",
  inet6: "IPv6",
  inet: "IPv4",
}

/** The request and its record: what was asked, from where, by whom, and what the tool wrote. */
export function RunDetails({ run }: { run: DiagnosticRun }) {
  const time = (value?: string) =>
    value ? <time dateTime={value}>{timestamp(value)}</time> : "Not recorded"
  return (
    <div className="min-w-0 space-y-8">
      <DetailList aria-label="Run facts" className="max-w-3xl">
        <Detail label="Diagnostic">{runTool(run)}</Detail>
        <Detail label="Target">
          <span className="font-mono break-all">{runTarget(run)}</span>
        </Detail>
        <Detail label="Vantage">
          {run.scope.vantage === "dashboard_host" ? "This server" : run.scope.vantage}
        </Detail>
        <Detail label="Family">{FAMILY[run.scope.family] ?? run.scope.family}</Detail>
        {run.scope.source && <Detail label="Source">{run.scope.source}</Detail>}
        {run.scope.sourceAddress && (
          <Detail label="Source address">
            <span className="font-mono">{run.scope.sourceAddress}</span>
          </Detail>
        )}
        {run.scope.address && (
          <Detail label="Selected destination">
            <span className="font-mono">{run.scope.address}</span>
          </Detail>
        )}
        {run.scope.mark && <Detail label="Mark">{run.scope.mark}</Detail>}
        {run.scope.protocol && (
          <Detail label="Protocol">{run.scope.protocol.replaceAll("_", " ")}</Detail>
        )}
        {run.scope.port && <Detail label="Port">{run.scope.port}</Detail>}
        {run.scope.interface && <Detail label="Interface">{run.scope.interface}</Detail>}
        {run.request.record && <Detail label="Record type">{run.request.record}</Detail>}
        {run.request.option && <Detail label="Option">{run.request.option}</Detail>}
        <Detail label="Created">{time(run.createdAt)}</Detail>
        <Detail label="Started">{time(run.startedAt)}</Detail>
        <Detail label="Ended">{time(run.endedAt)}</Detail>
        <Detail label="Requested by">{run.createdBy}</Detail>
        <Detail label="Duration">{diagnosticDuration(run)}</Detail>
        <Detail label="Outcome source">
          {run.outcomeSource?.replaceAll("_", " ") || "Awaiting outcome"}
        </Detail>
        <Detail label="Run">
          <span className="font-mono break-all">{run.id}</span>
        </Detail>
        {run.rerunOf && (
          <Detail label="Rerun of">
            <Link
              href={`/network/runs/${encodeURIComponent(run.rerunOf)}`}
              className="font-mono break-all underline-offset-4 hover:underline"
            >
              {run.rerunOf}
            </Link>
          </Detail>
        )}
      </DetailList>
      <Panel plain aria-label="Bounded tool evidence">
        <PanelHeader title="Tool output" />
        <PanelBody className="space-y-3">
          {run.error && run.hasResult && (
            <Well className="max-h-48 break-all whitespace-pre-wrap" aria-label="Probe error">
              {run.error}
            </Well>
          )}
          {run.result?.output ? (
            <Well className="max-h-80 break-all whitespace-pre-wrap">{run.result.output}</Well>
          ) : (
            <p className="text-hint text-muted-foreground">No tool output recorded.</p>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
