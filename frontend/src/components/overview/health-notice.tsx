"use client"

import { get } from "@/lib/api"
import { type DashboardHealth, healthProblems } from "@/lib/dashboard-health"
import { usePoll } from "@/hooks/use-poll"
import { Warning } from "@/components/icons"
import { Notice } from "@/components/state"

/** The store is asked every fifteen seconds; twice that is soon enough to say what it found. */
const POLL = 30_000

/**
 * What is wrong with the dashboard itself, said first on the Overview: a full disk under its
 * data, a database that refuses writes, a background task that keeps crashing. The reader has to
 * act on it, which is what a `Notice` is for, and nothing else in the product would tell them: the
 * container stays "healthy" on purpose, a failed save elsewhere reads as that page's problem, and a
 * task that is restarted after every panic fails without a sound. Nothing is drawn while all is
 * well.
 */
export function DashboardHealthNotice() {
  const health = usePoll(
    (signal) => get<DashboardHealth>("/dashboard/health", undefined, signal),
    POLL,
  )
  const problems = healthProblems(health.data)
  if (problems.length === 0) return null
  return (
    <>
      {problems.map((problem) => (
        <Notice key={problem.key} tone={problem.tone} icon={Warning} title={problem.title}>
          <p>{problem.body}</p>
          {problem.detail && (
            <p className="mt-1 font-mono text-hint break-all text-foreground">{problem.detail}</p>
          )}
        </Notice>
      ))}
    </>
  )
}
