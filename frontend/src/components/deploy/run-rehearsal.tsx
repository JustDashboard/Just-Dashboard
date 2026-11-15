"use client"

import { clockMinute, plural, timestamp } from "@/lib/format"
import type { DeploymentStep, RehearsalReport } from "@/lib/types"
import { Database } from "@/components/icons"
import { Tag } from "@/components/tag"
import { StepMark } from "@/components/deploy/vocabulary"
import { engineName, heldLocks, lockMode, millis } from "@/components/deploy/rehearsal-model"
import { RunVerdict } from "@/components/deploy/run-verdict"
import { rehearsalVerdict } from "@/components/deploy/run-verdicts"

/**
 * The rehearse_migration step's report (`deploy/rehearsal.go`), read as what
 * an operator asks of it: where the copy came from and how long it took,
 * whether the release tasks passed against it, which tables they held locked
 * and for how long — the time production's reads or writes of those tables
 * will wait for the same migration — and whether the copy is gone. It opens
 * on the verdict as one sentence on the step's mark, with the copy's story as
 * a line of facts (`RunVerdict`). A report that never rehearsed has only its
 * reason, which the step's own error or warning line above already carries,
 * so it adds nothing here.
 */
export function RehearsalView({ step, report }: { step: DeploymentStep; report: RehearsalReport }) {
  if (!report.rehearsed) {
    return step.state === "warning" && report.reason ? (
      <p className="text-body text-muted-foreground">
        Not rehearsed: {report.reason}. This environment lets a release continue without its
        rehearsal, so the release tasks ran against production unrehearsed.
      </p>
    ) : null
  }
  const locks = heldLocks(report)
  return (
    <section aria-label="Rehearsal report" className="min-w-0 space-y-5">
      <RunVerdict
        verdict={rehearsalVerdict(
          report,
          (iso) => clockMinute(iso),
          (iso) => timestamp(iso),
        )}
        glyph={<Database aria-hidden />}
      />

      <ol aria-label="Release tasks against the copy" className="space-y-2">
        {report.tasks.map((task) => {
          const bad = task.exitCode !== 0 || Boolean(task.error)
          return (
            <li key={task.name} className="min-w-0 space-y-1">
              <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-body">
                <StepMark state={bad ? "failed" : "passed"} />
                <span className="font-mono">{task.name}</span>
                <span className="numeric text-muted-foreground">{millis(task.durationMs)}</span>
                <Tag mono tone={bad ? "danger" : "default"}>
                  exit {task.exitCode}
                </Tag>
              </p>
              {task.error && (
                <p className="pl-6 font-mono text-xs leading-relaxed wrap-anywhere text-destructive">
                  {task.error}
                </p>
              )}
            </li>
          )
        })}
      </ol>

      {locks.length > 0 && (
        <ul aria-label="Locks held against the copy" className="space-y-1.5 text-body">
          {locks.map((held) => (
            <li key={`${held.relation}:${held.mode}`} className="min-w-0">
              <span className="font-mono">{lockMode(held.mode)}</span> on{" "}
              <span className="font-mono">{held.relation}</span> for about{" "}
              <span className="numeric">{millis(held.heldMs)}</span>
              <span className="text-muted-foreground">
                {" "}
                — production&apos;s{" "}
                {held.mode === "AccessExclusiveLock" ? "reads and writes" : "writes"} of{" "}
                {held.relation} will wait about as long
              </span>
            </li>
          ))}
        </ul>
      )}
      {report.locks.maxWaiting > 0 && (
        <p className="text-body text-muted-foreground">
          {plural(report.locks.maxWaiting, "session")} waited for a lock at once, about{" "}
          <span className="numeric text-foreground">{millis(report.locks.waitedMs)}</span> in all.
        </p>
      )}

      {report.isolation && (
        <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
          {engineName(report.database?.engine)} copy of {report.database?.name} on{" "}
          <span className="font-mono">{report.isolation.network}</span>, an internal network with no
          way out that only the task joined
          {report.isolation.published ? "" : "; nothing published"}
          {report.isolation.volumesMounted ? "" : ", no volumes mounted"}. The rehearsal did not
          touch production&apos;s database or network.
        </p>
      )}
    </section>
  )
}
