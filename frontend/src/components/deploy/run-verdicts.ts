import type { RehearsalReport, ReplayReport } from "@/lib/types"
import { plural } from "@/lib/format"
import { latencyWords } from "@/lib/latency-words"
import { latencyHint, replayMs } from "@/components/deploy/replay-model"
import {
  heldLocks,
  lockMode,
  millis,
  sourceShort,
  sourceWords,
} from "@/components/deploy/rehearsal-model"

/**
 * A step's report as one sentence and a line of facts: what a traffic replay
 * and a migration rehearsal concluded, in the words a person would say it in.
 * Each was four figures over four captions, which the reader put together
 * into the sentence. Pure, so `bun test` holds the wording.
 */
export type VerdictFact = {
  key: string
  text: string
  /** What the fact was worked out from, for a `title`. */
  basis?: string
  tone?: "danger" | "warning"
}

export type Verdict = {
  tone: "success" | "danger" | "warning"
  title: string
  facts: VerdictFact[]
}

/** How a replay against the candidate went: how many matched, then what was measured. */
export function replayVerdict(report: ReplayReport): Verdict {
  const matched = Math.max(0, report.requests - report.drifted)
  const statusDrift = report.verdict.code === "status_drift" || report.verdict.code === "unanswered"
  const slow = report.verdict.code === "latency"
  const title =
    report.drifted === 0
      ? report.requests === 1
        ? "The 1 replayed request matched the record"
        : `All ${report.requests} replayed requests matched the record`
      : `${matched} of ${plural(report.requests, "replayed request")} matched the record`
  const facts: VerdictFact[] = [
    {
      key: "drift",
      text:
        report.drifted === 0
          ? `None drifted, ${report.maxStatusDrift}% allowed`
          : `${report.drifted} drifted (${report.driftPercent}%), ${report.maxStatusDrift}% allowed`,
      tone: statusDrift ? "danger" : report.drifted > 0 ? "warning" : undefined,
    },
    report.latencyJudged
      ? {
          key: "latency",
          text: `${latencyWords("p95", replayMs(report.candidateP95))}, ${latencyHint(report)}`,
          tone: slow ? "danger" : undefined,
        }
      : { key: "latency", text: `Speed not judged: ${latencyHint(report)}` },
    {
      key: "took",
      text: report.timedOut
        ? `Took ${replayMs(report.durationMs)}, out of its ${report.timeoutSeconds} s`
        : `Took ${replayMs(report.durationMs)}, ${report.concurrency} at a time`,
      tone: report.timedOut ? "warning" : undefined,
    },
    { key: "from", text: `From ${plural(report.eligible, "recorded request")}` },
  ]
  return {
    tone: report.verdict.tripped ? "danger" : report.drifted > 0 ? "warning" : "success",
    title,
    facts,
  }
}

/**
 * How a rehearsal of the release tasks against a copy went: whether they
 * passed, then where the copy came from, the longest lock they held and
 * whether the copy is gone. `at` formats the backup's time for the line,
 * `full` for its title.
 */
export function rehearsalVerdict(
  report: RehearsalReport,
  at: (iso: string) => string,
  full: (iso: string) => string,
): Verdict {
  const failed = report.tasks.find((task) => task.exitCode !== 0 || task.error)
  const copy = `a copy of ${report.database?.name ?? "the database"}`
  const title = failed
    ? `${failed.name} failed against ${copy}`
    : report.tasks.length === 0
      ? `No release task ran against ${copy}`
      : report.tasks.length === 1
        ? `${report.tasks[0].name} passed against ${copy}`
        : `${plural(report.tasks.length, "release task")} passed against ${copy}`
  const lock = heldLocks(report)[0]
  const facts: VerdictFact[] = []
  if (failed) facts.push({ key: "exit", text: `Exit ${failed.exitCode}`, tone: "danger" })
  facts.push({
    key: "restored",
    text: `Restored in ${millis(report.restoreMs)} from ${theSource(sourceShort(report.source, at))}`,
    basis: `From ${sourceWords(report.source, full)}`,
  })
  facts.push({
    key: "lock",
    text: lock
      ? `Longest lock ${millis(lock.heldMs)}, ${lockMode(lock.mode)}`
      : report.locks.read
        ? "No table locked"
        : "Locks not read",
  })
  facts.push(
    report.cleanedUp
      ? { key: "copy", text: "Copy removed with its network" }
      : { key: "copy", text: "Copy left until the next start", tone: "warning" },
  )
  return { tone: failed ? "danger" : report.cleanedUp ? "success" : "warning", title, facts }
}

/** "dump of 02:15" with its article, where the words do not bring their own. */
function theSource(words: string) {
  return /^(a|the) /.test(words) ? words : `the ${words}`
}
