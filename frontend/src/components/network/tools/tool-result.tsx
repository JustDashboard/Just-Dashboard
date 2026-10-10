"use client"

import { cn } from "@/lib/utils"
import { hasEvidence, resultReading, type DiagnosticResult } from "@/lib/network-diagnostics"
import { Well } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ProbeEvidence } from "./probe-evidence"

/**
 * One probe's answer, rendered the same on every card: verdict, target,
 * duration, the structured evidence, structured records as marks, then the
 * tool's own text verbatim in the recessed well the rest of the product uses
 * for command output. Where structured evidence exists the raw text is one
 * press away rather than first.
 */
export function ToolResult({
  result,
  successLabel = "answered",
}: {
  result: DiagnosticResult
  successLabel?: string
}) {
  const reading = resultReading(result, successLabel)
  const structured = hasEvidence(result)
  const raw = (
    <Well
      className={cn(
        "max-h-72 text-hint whitespace-pre-wrap",
        !result.ok && "text-muted-foreground",
      )}
    >
      {result.output || result.error || "No output."}
    </Well>
  )
  return (
    <div className="space-y-4 border-t border-hairline pt-2.5">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <Status tone={reading.tone} label={reading.label} />
        <span className="truncate font-mono text-xs">{result.target}</span>
        <span className="numeric text-hint text-muted-foreground">{result.duration}</span>
      </div>
      {structured && <ProbeEvidence result={result} />}
      {result.records && result.records.length > 0 && (
        <div className="flex flex-wrap gap-1" aria-label="Records">
          {result.records.map((r, index) => (
            <Tag key={`${index}:${r}`} mono>
              {r}
            </Tag>
          ))}
        </div>
      )}
      {structured ? (
        <details className="text-body">
          <summary className="cursor-pointer focus-ring">Tool output</summary>
          <div className="mt-2">{raw}</div>
        </details>
      ) : (
        raw
      )}
      {result.output && result.error && result.error !== result.output && (
        <p role="alert" className="text-body text-destructive">
          {result.error}
        </p>
      )}
    </div>
  )
}
