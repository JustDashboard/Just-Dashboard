"use client"

import { plural } from "@/lib/format"
import type { DeploymentStep, ReplayReport } from "@/lib/types"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Tag } from "@/components/tag"
import { StepMark } from "@/components/deploy/vocabulary"
import { driftAnswer, driftRequest, latencyHint, replayMs } from "@/components/deploy/replay-model"

/**
 * The replay_traffic step's report (`deploy/replay.go`), read as what an
 * operator asks of it: how many of the requests people actually sent were
 * replayed against the candidate, how many it answered in another status
 * class than the live release had, whether it was slower, and which requests
 * those were — the route a single health check never asked for. A replay
 * that did not run says why in one sentence.
 */
export function ReplayView({ step, report }: { step: DeploymentStep; report: ReplayReport }) {
  if (!report.replayed) {
    if (!report.reason) return null
    return (
      <p className="text-body text-muted-foreground">
        Not replayed: {report.reason}.
        {step.state === "warning"
          ? " The release continued without the replay."
          : " The release went on to the cutover on its health checks alone."}
      </p>
    )
  }
  const tripped = report.verdict.tripped
  const statusDrift = report.verdict.code === "status_drift" || report.verdict.code === "unanswered"
  const slow = report.verdict.code === "latency"
  return (
    <section aria-label="Replay report" className="min-w-0 space-y-5">
      <StatGrid columns={4} dense>
        <StatTile
          label="Replayed"
          value={<span className="numeric">{report.requests}</span>}
          hint={`of ${plural(report.eligible, "recorded request")}`}
        />
        <StatTile
          label="Drifted"
          value={<span className="numeric">{report.drifted}</span>}
          tone={statusDrift ? "danger" : report.drifted > 0 ? "warning" : "default"}
          hint={`${report.driftPercent}% · ${report.maxStatusDrift}% allowed`}
        />
        <StatTile
          label="Candidate p95"
          value={report.latencyJudged ? replayMs(report.candidateP95) : "Not judged"}
          tone={slow ? "danger" : "default"}
          hint={latencyHint(report)}
        />
        <StatTile
          label="Took"
          value={replayMs(report.durationMs)}
          tone={report.timedOut ? "warning" : "default"}
          hint={
            report.timedOut
              ? `out of its ${report.timeoutSeconds} s`
              : `${report.concurrency} at a time`
          }
        />
      </StatGrid>

      {/* A stopped release's verdict is its error, already above the figures. */}
      {!step.errorMessage && (
        <p className="flex min-w-0 items-start gap-2.5 text-body">
          <StepMark state={tripped ? "failed" : "passed"} />
          <span className="min-w-0">{report.verdict.reason}</span>
        </p>
      )}

      {report.drifts.length > 0 && (
        <ol aria-label="Requests answered differently" className="space-y-1.5">
          {report.drifts.map((drift, index) => (
            <li
              key={`${driftRequest(drift)}:${index}`}
              className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-body"
            >
              <Tag mono>{drift.method}</Tag>
              <span className="min-w-0 font-mono text-xs wrap-anywhere">
                {drift.path}
                {drift.query && <span className="text-muted-foreground">?{drift.query}</span>}
              </span>
              <span className="numeric text-muted-foreground">
                recorded <span className="text-foreground">{drift.recordedStatus}</span>
              </span>
              <Tag mono tone="danger">
                {driftAnswer(drift)}
              </Tag>
              {drift.candidateMs !== undefined && (
                <span className="numeric text-hint text-muted-foreground">
                  {replayMs(drift.candidateMs)}
                </span>
              )}
            </li>
          ))}
        </ol>
      )}
      {report.drifted > report.drifts.length && (
        <p className="text-hint text-muted-foreground">
          {plural(report.drifted - report.drifts.length, "more drifted request")} not listed.
        </p>
      )}

      <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
        The last {plural(report.requests, "distinct GET and HEAD request")} the access log recorded
        for {report.domains?.join(", ") || "the environment's domains"}, sent to the candidate at{" "}
        <span className="font-mono">{report.target}</span> with the recorded Host and no cookies,
        credentials or bodies. Nothing went through the public route, and no answer was kept.
        {tripped &&
          (report.recovery?.candidateStopped
            ? " The candidate was stopped and the live release was left serving."
            : " The live release was left serving.")}
      </p>
    </section>
  )
}
