"use client"

import { cn } from "@/lib/utils"
import type { ProbeResult } from "@/lib/types"
import { Well } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"

/**
 * One probe's answer, rendered the same on every card: verdict, target,
 * duration, structured records as marks, then the tool's own text verbatim in
 * the recessed well the rest of the product uses for command output.
 */
export function ToolResult({
  result,
  successLabel = "answered",
}: {
  result: ProbeResult
  successLabel?: string
}) {
  return (
    <div className="space-y-2 border-t border-hairline pt-2.5">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <Status
          verdict={result.ok ? "ok" : "critical"}
          label={result.ok ? successLabel : "no answer"}
        />
        <span className="truncate font-mono text-xs">{result.target}</span>
        <span className="numeric text-hint text-muted-foreground">{result.duration}</span>
      </div>
      {result.records && result.records.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {result.records.map((r, index) => (
            <Tag key={`${index}:${r}`} mono>
              {r}
            </Tag>
          ))}
        </div>
      )}
      <Well
        className={cn(
          "max-h-72 text-hint whitespace-pre-wrap",
          !result.ok && "text-muted-foreground",
        )}
      >
        {result.output || result.error || "No output."}
      </Well>
      {result.output && result.error && result.error !== result.output && (
        <p role="alert" className="text-body text-destructive">
          {result.error}
        </p>
      )}
    </div>
  )
}
