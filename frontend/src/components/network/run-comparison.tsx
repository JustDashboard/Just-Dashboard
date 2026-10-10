"use client"

import { useState } from "react"
import { Field } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { get } from "@/lib/api"
import {
  diagnosticCompatible,
  formatMetric,
  type DiagnosticComparison,
  type DiagnosticDifference,
  type DiagnosticRun,
} from "@/lib/network-diagnostics"

export function RunComparison({ run, runs }: { run: DiagnosticRun; runs: DiagnosticRun[] }) {
  const candidates = runs.filter((other) => diagnosticCompatible(other, run))
  const [before, setBefore] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<DiagnosticComparison>()
  const [error, setError] = useState<Error>()
  const selected = candidates.some((candidate) => candidate.id === before) ? before : ""
  const compare = async () => {
    if (!selected || busy) return
    setBusy(true)
    setResult(undefined)
    setError(undefined)
    try {
      setResult(
        await get<DiagnosticComparison>("/network/diagnostics/compare", {
          before: selected,
          after: run.id,
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Panel plain aria-label="Compare saved runs">
      <PanelHeader title="Compare runs" />
      <PanelBody className="space-y-3">
        {candidates.length ? (
          <div className="flex flex-wrap items-end gap-3">
            <Field label="Baseline run" className="min-w-0 flex-1">
              <Select
                value={selected}
                onValueChange={(value) => {
                  setBefore(value)
                  setResult(undefined)
                  setError(undefined)
                }}
                disabled={busy}
              >
                <SelectTrigger aria-label="Baseline run">
                  <SelectValue placeholder="Choose a compatible run" />
                </SelectTrigger>
                <SelectContent>
                  {candidates.map((candidate) => (
                    <SelectItem key={candidate.id} value={candidate.id}>
                      {candidate.name} · {new Date(candidate.createdAt).toLocaleString()}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Button variant="outline" onClick={compare} disabled={!selected || busy} pending={busy}>
              Compare
            </Button>
          </div>
        ) : (
          <p className="text-body text-muted-foreground">
            Another finished run with a retained result and the same normalized request is needed.
            Rerun this test to create one.
          </p>
        )}
        {error && <ErrorState error={error} />}
        {result && (
          <div className="space-y-3" aria-label="Run comparison result">
            <p className="text-body">
              {result.outcome.before} → {result.outcome.after} ·{" "}
              {result.duration.before || "duration unknown"} →{" "}
              {result.duration.after || "duration unknown"}
              {result.durationDeltaMs !== undefined
                ? ` · ${result.durationDeltaMs > 0 ? "+" : ""}${result.durationDeltaMs} ms`
                : ""}
            </p>
            {result.partial && (
              <Notice tone="warning" title="Partial comparison">
                At least one retained artifact or difference was truncated.
              </Notice>
            )}
            {Boolean(result.metrics?.length) && (
              <div aria-label="Measurement changes" className="space-y-1">
                <p className="text-body font-medium">Measurements</p>
                <DetailList>
                  {result.metrics!.map((metric) => (
                    <Detail key={metric.key} label={metric.label}>
                      <span className="numeric">
                        {metric.before === undefined
                          ? "—"
                          : formatMetric({ value: metric.before, unit: metric.unit })}{" "}
                        →{" "}
                        {metric.after === undefined
                          ? "—"
                          : formatMetric({ value: metric.after, unit: metric.unit })}
                      </span>
                    </Detail>
                  ))}
                </DetailList>
              </div>
            )}
            <Difference diff={result.records} label="Structured records" />
            <details className="text-body">
              <summary className="cursor-pointer focus-ring">Output line differences</summary>
              <div className="mt-3">
                <Difference diff={result.output} label="Tool output" />
              </div>
            </details>
            {result.limitations.map((line) => (
              <p key={line} className="text-hint text-muted-foreground">
                {line}
              </p>
            ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

function Difference({ diff, label }: { diff: DiagnosticDifference; label: string }) {
  return (
    <div className="space-y-2" aria-label={label}>
      <p className="text-body font-medium">
        {label}: {diff.added.length} added · {diff.removed.length} removed · {diff.unchanged}{" "}
        unchanged{diff.truncated ? " · truncated" : ""}
      </p>
      {(diff.added.length > 0 || diff.removed.length > 0) && (
        <Well className="max-h-64 break-all whitespace-pre-wrap">
          {diff.removed
            .map((line) => `− ${line}`)
            .concat(diff.added.map((line) => `+ ${line}`))
            .join("\n")}
        </Well>
      )}
    </div>
  )
}
