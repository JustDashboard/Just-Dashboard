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
 */
export function RetentionNote({ retention }: { retention: LogRetention }) {
  const Icon =
    retention.level === "warn"
      ? Warning
      : retention.level === "unknown"
        ? Question
        : RotateClockwise
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-x-2 gap-y-0.5 border-t border-hairline px-3 py-1.5 text-hint",
        retention.level === "warn"
          ? "bg-wash-warning text-warning"
          : "bg-surface-header text-muted-foreground",
      )}
    >
      <Icon className="size-3 shrink-0" />
      <span>{retention.summary}</span>
      {retention.pattern && (
        <span className="text-muted-foreground/70">
          Rule matches <code className="font-mono">{retention.pattern}</code>
          {retention.rule?.configFile && ` in ${retention.rule.configFile}`}.
        </span>
      )}
      {retention.lastRun && retention.level !== "warn" && (
        <span className="text-muted-foreground/70">
          logrotate last ran {relativeTime(retention.lastRun)}.
        </span>
      )}
    </div>
  )
}
