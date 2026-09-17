"use client"

import { Question, RotateClockwise, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import type { LogRetention } from "@/lib/types"

/**
 * Whether anything is actually trimming this file.
 *
 * logrotate's rules are on disk already and nobody reads them, so this answers
 * the question they would have been consulted for. The file with no rule
 * governing it is the one that fills the disk at 3am, and it is precisely the
 * entry a rule list cannot show — it is the one that is not there.
 *
 * One line in the pane's footer, tinted only when it is a warning: a reading of
 * state, next to the stream's state and the line count.
 */
export function RetentionNote({ retention }: { retention: LogRetention }) {
  const Icon =
    retention.level === "warn"
      ? Warning
      : retention.level === "unknown"
        ? Question
        : RotateClockwise
  const detail = [
    retention.pattern &&
      `Rule matches ${retention.pattern}${retention.rule?.configFile ? ` in ${retention.rule.configFile}` : ""}.`,
    retention.lastRun && retention.level !== "warn"
      ? `logrotate last ran ${relativeTime(retention.lastRun)}.`
      : null,
  ]
    .filter(Boolean)
    .join(" ")

  return (
    <span
      className={cn(
        "flex min-w-0 items-center gap-1.5",
        retention.level === "warn" ? "text-warning" : "text-muted-foreground",
      )}
      title={detail || undefined}
    >
      <Icon className="size-3 shrink-0" />
      <span className="truncate">{retention.summary}</span>
    </span>
  )
}
