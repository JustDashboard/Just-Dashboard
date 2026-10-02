"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, errorMessage, post } from "@/lib/api"
import { useMemoryState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, Statement } from "@/components/form"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { gate, reasonsText, type Asker } from "@/components/database/query/gate"
import {
  stepsOfQuery,
  stepsOfScript,
  type Explained,
  type Run,
  type TabRun,
} from "@/components/database/query/run-model"
import { firstLine, type RunTarget } from "@/components/database/query/sql-text"
import type {
  ClassifyResponse,
  ExplainResponse,
  QueryResponse,
  ScriptResponse,
} from "@/components/database/query/types"

const NO_RUNS: Record<string, TabRun> = {}

/** How many destructive statements a confirmation prints before it counts the rest. */
const SHOWN = 4

export interface RunOptions {
  limit: number
  /** Several statements as one transaction. */
  transaction: boolean
}

/**
 * Runs, stops and explains statements for the editor's tabs.
 *
 * A statement is classified by the server before it is sent, every time, from
 * the exact text about to run. That one answer decides everything that used
 * to be decided from whatever was classified last: whether the role may send
 * it, whether a protected connection takes it, whether the reader is asked
 * first, and whether it is one statement or a script. A classification that
 * does not come back is a run that does not happen.
 *
 * What a tab ran is kept in memory for the browser tab — rows of somebody's
 * data never reach Web Storage — and outlives the page: a statement left
 * running comes back with its clock still ticking and its Cancel still
 * working, and lands in its tab whether or not anybody is looking.
 */
export function useRunner({
  confirm,
  onSettled,
}: {
  confirm: (request: ConfirmRequest) => void
  /** A statement reached the server and came back: the history has a new entry. */
  onSettled: () => void
}) {
  const { id, conn, engine, readOnly } = useDatabase()
  const { can } = useAuth()
  const [runs, setRuns] = useMemoryState<Record<string, TabRun>>(
    `databases.${id}.query.runs`,
    NO_RUNS,
  )
  // Which tab is being checked before its run is sent: nothing is in flight yet.
  const [checking, setChecking] = useState<string | null>(null)
  const settled = useRef(onSettled)
  useEffect(() => {
    settled.current = onSettled
  })

  const asker: Asker = {
    canRun: can("service.control"),
    canDestroy: can("destructive"),
    readOnly,
  }
  const askerRef = useRef(asker)
  useEffect(() => {
    askerRef.current = asker
  })

  const patch = useCallback(
    (tab: string, change: (held: TabRun) => TabRun) =>
      setRuns((all) => ({ ...all, [tab]: change(all[tab] ?? {}) })),
    [setRuns],
  )
  const patchRun = useCallback(
    (tab: string, queryId: string, change: (run: Run) => Run) =>
      patch(tab, (held) =>
        held.run && held.run.queryId === queryId ? { ...held, run: change(held.run) } : held,
      ),
    [patch],
  )

  /** The server's reading of the text, or why there is none. */
  const classify = useCallback(
    async (sql: string): Promise<ClassifyResponse | string> => {
      try {
        const answer = await post<ClassifyResponse>(`/databases/${id}/classify`, { query: sql })
        if (!Array.isArray(answer.statements) || typeof answer.level !== "string") {
          return "The server did not say what this statement would do, so it was not run."
        }
        if (answer.statements.length === 0) {
          return `The statement could not be read${answer.reasons?.length ? `: ${answer.reasons.join("; ")}` : ""}.`
        }
        return answer
      } catch (err) {
        return `What this statement would do could not be checked, so it was not run: ${errorMessage(err)}`
      }
    },
    [id],
  )

  /** Asks before a statement that destroys, with what it does and the statement itself. */
  const ask = useCallback(
    (verdict: ClassifyResponse, title: string, note: string, go: () => void) => {
      const destroying = verdict.statements.filter((statement) => statement.risk.destructive)
      const shown = destroying.slice(0, SHOWN)
      confirm({
        title,
        confirmLabel: title,
        subject: {
          mark: <EngineMark engine={engine} size="sm" />,
          name: conn.name,
          facts: (
            <>
              <FormFact label="Engine">{engine.label}</FormFact>
              {conn.database && (
                <FormFact label={engine.databaseField} mono>
                  {conn.database}
                </FormFact>
              )}
              {conn.environment && <FormFact label="Environment">{conn.environment}</FormFact>}
            </>
          ),
        },
        description: (
          <div className="space-y-3">
            <p>
              The server reads {destroying.length === 1 ? "this statement" : "these statements"} as{" "}
              <span className="font-medium">{verdict.level}</span>
              {verdict.reasons.length > 0 && <>: it {reasonsText(verdict)}</>}. {note}
            </p>
            {shown.map((statement, index) => (
              <div key={index} className="space-y-1">
                <Statement
                  label={verdict.statements.length > 1 ? `Line ${statement.line}` : "Statement"}
                  sql={statement.sql}
                  placeholder=""
                />
                {destroying.length > 1 && reasonsText(statement.risk) && (
                  <p className="text-hint text-muted-foreground">
                    It {reasonsText(statement.risk)}.
                  </p>
                )}
              </div>
            ))}
            {destroying.length > shown.length && (
              <p className="text-hint text-muted-foreground">
                And {destroying.length - shown.length} more that destroy, of{" "}
                {verdict.statements.length} statements in all.
              </p>
            )}
          </div>
        ),
        // The dialog only starts the run: it is the editor that waits for it,
        // with a clock and a way to stop it.
        action: async () => {
          go()
          return "reported"
        },
      })
    },
    [confirm, conn, engine],
  )

  const send = useCallback(
    (tab: string, target: RunTarget, verdict: ClassifyResponse, options: RunOptions) => {
      const queryId = crypto.randomUUID()
      const script = verdict.statements.length > 1
      const run: Run = {
        queryId,
        phase: "running",
        startedAt: Date.now(),
        sql: target.sql,
        scope: target.scope,
        offset: target.offset,
        line: target.line,
        limit: options.limit,
        script,
        steps: [],
        failed: -1,
        risk: verdict,
      }
      patch(tab, (held) => ({ ...held, run }))
      const done = (change: Partial<Run>) => {
        patchRun(tab, queryId, (held) => ({
          ...held,
          ...change,
          phase: "done",
          finishedAt: Date.now(),
        }))
        settled.current()
      }
      const cancelled = (err: unknown) => err instanceof ApiError && err.code === "query_cancelled"
      if (script) {
        post<ScriptResponse>(`/databases/${id}/script`, {
          script: target.sql,
          maxRows: options.limit,
          queryId,
          ...(options.transaction ? { transaction: true } : {}),
        })
          .then((answer) => {
            const steps = stepsOfScript(answer, target.line)
            done({
              steps,
              failed: answer.failed,
              transaction: answer.transaction,
              cancelled: steps.some((step) => step.error === "the statement was cancelled"),
            })
          })
          .catch((err: unknown) => done({ error: errorMessage(err), cancelled: cancelled(err) }))
      } else {
        const sql = verdict.statements[0].sql
        post<QueryResponse>(`/databases/${id}/query`, {
          query: target.sql,
          maxRows: options.limit,
          queryId,
        })
          .then((answer) => done({ steps: stepsOfQuery(answer, sql, target.line) }))
          .catch((err: unknown) =>
            done(
              cancelled(err)
                ? { cancelled: true }
                : {
                    // The engine's refusal is the statement's own outcome, shown where its rows would be.
                    steps: [
                      {
                        index: 0,
                        sql,
                        line: target.line,
                        risk: verdict.statements[0].risk,
                        status: "error",
                        error: errorMessage(err),
                        durationMs: 0,
                      },
                    ],
                    failed: 0,
                  },
            ),
          )
      }
    },
    [id, patch, patchRun],
  )

  /** Classify, gate, ask, send. Resolves once the run has been sent or turned down. */
  const run = useCallback(
    async (tab: string, target: RunTarget, options: RunOptions) => {
      setChecking(tab)
      const verdict = await classify(target.sql)
      setChecking((held) => (held === tab ? null : held))
      const refuse = (why: string) =>
        patch(tab, (held) => ({
          ...held,
          run: {
            queryId: crypto.randomUUID(),
            phase: "done",
            startedAt: Date.now(),
            finishedAt: Date.now(),
            sql: target.sql,
            scope: target.scope,
            offset: target.offset,
            line: target.line,
            limit: options.limit,
            script: false,
            steps: [],
            failed: -1,
            risk: typeof verdict === "string" ? undefined : verdict,
            refused: why,
          },
        }))
      if (typeof verdict === "string") return refuse(verdict)
      const allowed = gate(verdict, askerRef.current)
      if (!allowed.run) return refuse(allowed.why)
      const go = () => send(tab, target, verdict, options)
      if (!allowed.confirm) return go()
      const several = verdict.statements.length > 1
      ask(
        verdict,
        several ? `Run ${verdict.statements.length} statements` : "Run statement",
        several && options.transaction
          ? "They run in one transaction: all of them take effect, or none."
          : "It cannot be taken back once it has run.",
        go,
      )
    },
    [classify, patch, send, ask],
  )

  /** Stops a tab's run on the server. The run's own request then comes back as cancelled. */
  const cancel = useCallback(
    (tab: string) => {
      const held = runs[tab]?.run
      if (!held || held.phase !== "running") return
      patchRun(tab, held.queryId, (run) => ({ ...run, cancelling: true }))
      post<{ cancelled: boolean }>(`/databases/${id}/query/cancel`, { queryId: held.queryId })
        .then((answer) => {
          // Nothing by that name is running: it finished as the reader pressed.
          if (!answer.cancelled) {
            patchRun(tab, held.queryId, (run) => ({ ...run, cancelling: false }))
          }
        })
        .catch(() => patchRun(tab, held.queryId, (run) => ({ ...run, cancelling: false })))
    },
    [id, runs, patchRun],
  )

  const explain = useCallback(
    async (tab: string, sql: string, analyze: boolean) => {
      const startedAt = Date.now()
      const settle = (change: Partial<Explained>) =>
        patch(tab, (held) =>
          held.explain && held.explain.startedAt === startedAt
            ? {
                ...held,
                explain: { ...held.explain, ...change, phase: "done", finishedAt: Date.now() },
              }
            : held,
        )
      const begin = () =>
        patch(tab, (held) => ({ ...held, explain: { phase: "running", startedAt, sql, analyze } }))
      const ask_ = async () => {
        begin()
        const json = engine.can("explainJSON")
        const request = (format: "json" | "text") =>
          post<ExplainResponse>(`/databases/${id}/explain`, {
            query: sql,
            format,
            ...(analyze ? { analyze: true } : {}),
          })
        try {
          settle({ answer: await request(json ? "json" : "text") })
        } catch (err) {
          // An engine that measures a plan only as text refuses the measured
          // tree; its text is asked for instead of showing a refusal of a
          // form the reader never chose.
          if (json && analyze && err instanceof ApiError && err.status === 400) {
            try {
              settle({ answer: await request("text") })
              return
            } catch (again) {
              settle({ error: errorMessage(again) })
              return
            }
          }
          settle({ error: errorMessage(err) })
        } finally {
          if (analyze) settled.current()
        }
      }
      // A plan alone runs nothing. Measuring one runs the statement, so it
      // passes the same gate a run does.
      if (!analyze) return ask_()
      setChecking(tab)
      const verdict = await classify(sql)
      setChecking((held) => (held === tab ? null : held))
      const refuse = (why: string) =>
        patch(tab, (held) => ({
          ...held,
          explain: { phase: "done", startedAt, finishedAt: Date.now(), sql, analyze, refused: why },
        }))
      if (typeof verdict === "string") return refuse(verdict)
      const allowed = gate(verdict, askerRef.current)
      if (!allowed.run) return refuse(allowed.why)
      if (!allowed.confirm) return ask_()
      ask(
        verdict,
        "Run and measure statement",
        "Measuring a plan runs the statement. Where the engine can, what it changes is undone afterwards.",
        () => void ask_(),
      )
    },
    [id, engine, classify, patch, ask],
  )

  const forget = useCallback(
    (tab: string) =>
      setRuns((all) => {
        if (!(tab in all)) return all
        const next = { ...all }
        delete next[tab]
        return next
      }),
    [setRuns],
  )

  return { runs, checking, asker, run, cancel, explain, forget }
}

/** A statement's first line as a run's title: "select * from orders…". */
export const runTitle = (sql: string) => firstLine(sql, 60)
