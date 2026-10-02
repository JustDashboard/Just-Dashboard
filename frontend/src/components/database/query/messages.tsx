"use client"

import { cn } from "@/lib/utils"
import { EmptyNote } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { statementVerb } from "@/components/database/home/kinds"
import { messagesOf, spanText, type Message, type Run } from "@/components/database/query/run-model"
import { firstLine } from "@/components/database/query/sql-text"

const DOT: Record<Message["tone"], DotTone> = {
  default: "stopped",
  success: "running",
  warning: "warning",
  danger: "danger",
}

/** A statement's first line with its first word in the hue of what that word does. */
export function StatementLine({ sql, className }: { sql: string; className?: string }) {
  const line = firstLine(sql, 200)
  const verb = statementVerb(line)
  return (
    <span className={cn("min-w-0 truncate font-mono text-xs", className)} title={sql}>
      {verb ? (
        <>
          <span style={{ color: verb.color }}>{verb.word}</span>
          {verb.rest}
        </>
      ) : (
        line
      )}
    </span>
  )
}

/**
 * The run told in order: each statement with what it did and how long it
 * took, the engine's own words where one failed, and how the whole ended —
 * which a grid of the last statement's rows cannot say.
 */
export function Messages({
  run,
  onShowStep,
}: {
  run: Run | undefined
  /** Opens a statement's result. */
  onShowStep: (index: number) => void
}) {
  const lines = messagesOf(run)
  if (lines.length === 0) {
    return (
      <EmptyNote className="p-6">
        {run?.phase === "running"
          ? "The statement is still running."
          : "What each statement did is listed here once one has run."}
      </EmptyNote>
    )
  }
  return (
    <ol data-slot="query-messages" className="min-h-0 flex-1 overflow-auto py-1">
      {lines.map((line) => (
        <li key={line.key} className="px-2.5 py-1">
          <div className="flex min-h-6 min-w-0 items-center gap-2">
            <StatusDot tone={DOT[line.tone]} className="shrink-0" />
            {line.step ? (
              <>
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  Line {line.step.line}
                </span>
                <button
                  type="button"
                  className="flex min-w-0 flex-1 rounded-sm text-left focus-ring"
                  aria-label={`Show the result of statement ${line.step.index + 1}`}
                  onClick={() => onShowStep(line.step!.index)}
                >
                  <StatementLine sql={line.step.sql} />
                </button>
                <span
                  className={cn(
                    "shrink-0 text-xs",
                    line.tone === "danger" ? "text-destructive" : "text-muted-foreground",
                  )}
                >
                  {line.text}
                </span>
                <span className="numeric w-16 shrink-0 text-right text-hint text-muted-foreground">
                  {line.step.status !== "skipped" && spanText(line.step.durationMs)}
                </span>
              </>
            ) : (
              <span className="min-w-0 flex-1 text-xs">{line.text}</span>
            )}
          </div>
          {line.detail && (
            <p
              className={cn(
                "mt-0.5 ml-3.5 font-mono text-hint leading-relaxed break-words whitespace-pre-wrap",
                line.tone === "danger" ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {line.detail}
            </p>
          )}
        </li>
      ))}
    </ol>
  )
}
