"use client"

import { useEffect, useId, useMemo, useState } from "react"
import { Information, ListOrdered } from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { copyText } from "@/lib/clipboard"
import { plural, timestamp } from "@/lib/format"
import { Field, FormFact, FormFacts, FormNote, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ChangeDialog } from "@/components/database/schema/change-dialog"
import { BLANK_INDEX, indexRequest } from "@/components/database/schema/changes"
import { NotAvailable } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  candidateKey,
  columnsText,
  costChange,
  costText,
  hypopgFact,
  noCandidates,
  missingColumnsText,
  parseColumns,
  stepLines,
  usesText,
  type IndexAdvice as Advice,
  type IndexCandidate,
  type MissingIndexes,
  type StepLine,
  type WhatIf,
} from "@/components/database/query/index-advice-model"

/**
 * The Explain view's index tools, each a button that opens a dialog.
 *
 * Nothing here runs until the button is pressed, and nothing is kept once the
 * dialog is closed: version 0.7.5 took the Advisor and its findings out, and
 * this is the opposite of them — a tool the reader reaches for, on the
 * statement in front of them.
 *
 * PostgreSQL gets "What if?": indexes the plan's filters, joins and sorts could
 * use, one of which can be tried as a hypothetical index (HypoPG) and the
 * statement planned again beside the plan without it. SQL Server gets "Missing
 * indexes": what its optimizer has wished for, with the CREATE INDEX each wish
 * implies. Each is a piece of its own, so a step of the plan can host it later.
 */
export function IndexAdvice({
  sql,
  onOpenStatement,
}: {
  /** The statement whose plan is on screen; absent where there is no plan yet. */
  sql?: string
  /** Opens a statement in the editor. */
  onOpenStatement?: (sql: string, title?: string) => void
}) {
  const { engine } = useDatabase()
  const [open, setOpen] = useState<"whatIf" | "missing" | null>(null)
  const whatIf = sql !== undefined && engine.can("indexWhatIf")
  const missing = engine.can("missingIndexes")
  if (!whatIf && !missing) return null
  return (
    <>
      {whatIf && (
        <Button size="xs" variant="outline" onClick={() => setOpen("whatIf")}>
          <ListOrdered />
          What if?
        </Button>
      )}
      {missing && (
        <Button size="xs" variant="outline" onClick={() => setOpen("missing")}>
          <ListOrdered />
          Missing indexes
        </Button>
      )}
      {open === "whatIf" && sql !== undefined && (
        <WhatIfDialog sql={sql} onClose={() => setOpen(null)} />
      )}
      {open === "missing" && (
        <MissingIndexesDialog onClose={() => setOpen(null)} onOpenStatement={onOpenStatement} />
      )}
    </>
  )
}

/* ------------------------------------------------------------------ what if */

type Loaded<T> = { key: string; value?: T; error?: Error }

/** Candidates for a statement, asked once when the dialog opens. */
function useCandidates(id: number, sql: string, attempt: number) {
  const key = `${sql}\n${attempt}`
  const [loaded, setLoaded] = useState<Loaded<Advice>>()
  useEffect(() => {
    const controller = new AbortController()
    post<Advice>(
      `/databases/${id}/explain/index-candidates`,
      { query: sql },
      { signal: controller.signal },
    )
      .then((value) => setLoaded({ key, value }))
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setLoaded({ key, error: error instanceof Error ? error : new Error(String(error)) })
        }
      })
    return () => controller.abort()
  }, [id, sql, key])
  return loaded?.key === key ? loaded : undefined
}

function WhatIfDialog({ sql, onClose }: { sql: string; onClose: () => void }) {
  const { id, readOnly } = useDatabase()
  const { can } = useAuth()
  const [attempt, setAttempt] = useState(0)
  const candidates = useCandidates(id, sql, attempt)
  const advice = candidates?.value
  const [picked, setPicked] = useState<string>()
  const [edited, setEdited] = useState<{ key: string; text: string }>()
  const [tried, setTried] = useState<{ key: string; result?: WhatIf; error?: Error }>()
  const [trying, setTrying] = useState(false)
  const [creating, setCreating] = useState(false)
  const radios = useId()
  const fieldId = useId()

  const candidate = useMemo(
    () => advice?.candidates.find((c) => candidateKey(c) === picked) ?? advice?.candidates.at(0),
    [advice, picked],
  )
  const key = candidate ? candidateKey(candidate) : ""
  const text = edited?.key === key ? edited.text : candidate ? columnsText(candidate.columns) : ""
  const columns = parseColumns(text)
  // What was tried is only the answer to the index in the field now.
  const attemptKey = candidate ? `${key}|${columns.join("\u0000")}` : ""
  const answer = tried?.key === attemptKey ? tried : undefined
  const fact = advice ? hypopgFact(advice.hypopg) : undefined
  const mayCreate = can("service.control") && !readOnly

  const tryIt = async () => {
    if (!candidate || columns.length === 0 || trying) return
    setTrying(true)
    try {
      const result = await post<WhatIf>(`/databases/${id}/explain/what-if`, {
        query: sql,
        schema: candidate.schema,
        table: candidate.table,
        columns,
      })
      setTried({ key: attemptKey, result })
    } catch (error) {
      setTried({
        key: attemptKey,
        error: error instanceof Error ? error : new Error(String(error)),
      })
    } finally {
      setTrying(false)
    }
  }

  const real =
    candidate && columns.length > 0
      ? indexRequest(candidate.schema, candidate.table, {
          ...BLANK_INDEX,
          fields: columns,
          concurrently: true,
        })
      : null

  return (
    <>
      <Modal
        open
        onOpenChange={(next) => !next && !creating && onClose()}
        size="lg"
        title="What if there were an index?"
        description="Tries an index on this statement's plan without creating it"
        footer={
          <>
            <Button variant="outline" onClick={onClose}>
              Close
            </Button>
            {mayCreate && candidate && (answer?.result || advice?.hypopg.state !== "ready") && (
              <Button onClick={() => setCreating(true)} disabled={!real}>
                Create the real index…
              </Button>
            )}
          </>
        }
      >
        <div className="space-y-4">
          <p className="line-clamp-2 font-mono text-hint break-words text-muted-foreground">
            {sql}
          </p>

          {!candidates && <LoadingRows rows={3} />}
          {candidates?.error && (
            <ErrorState error={candidates.error} onRetry={() => setAttempt((n) => n + 1)} />
          )}
          {advice && (
            <>
              {fact && (
                <Notice icon={Information} title={fact}>
                  Without it no index can be tried on the plan. The candidates can still be created.
                </Notice>
              )}

              {advice.candidates.length === 0 ? (
                <EmptyState
                  className="py-8"
                  icon={ListOrdered}
                  title="No index to propose"
                  description={noCandidates(advice)}
                />
              ) : (
                <fieldset className="min-w-0 space-y-1.5">
                  <legend className="eyebrow mb-1.5">Candidate indexes</legend>
                  {advice.candidates.map((entry) => (
                    <CandidateRow
                      key={candidateKey(entry)}
                      name={radios}
                      candidate={entry}
                      selected={candidateKey(entry) === key}
                      onSelect={() => setPicked(candidateKey(entry))}
                    />
                  ))}
                </fieldset>
              )}

              {candidate && (
                <Field
                  label="Columns"
                  htmlFor={fieldId}
                  hint={`In the order the index is built, on ${candidate.schema}.${candidate.table}.`}
                  trailing={
                    <Button
                      size="xs"
                      onClick={() => void tryIt()}
                      pending={trying}
                      disabled={advice.hypopg.state !== "ready" || columns.length === 0}
                    >
                      Try it
                    </Button>
                  }
                >
                  <Input
                    id={fieldId}
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono text-xs"
                    value={text}
                    onChange={(event) => setEdited({ key, text: event.target.value })}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault()
                        if (advice.hypopg.state === "ready") void tryIt()
                      }
                    }}
                  />
                </Field>
              )}

              <div aria-live="polite" className="space-y-3">
                {answer?.error && (
                  <FormNote role="alert" tone="danger" className="break-words">
                    {answer.error.message}
                  </FormNote>
                )}
                {answer?.result && <WhatIfResult result={answer.result} />}
              </div>
            </>
          )}
        </div>
      </Modal>
      {creating && candidate && real && (
        <ChangeDialog
          onClose={() => setCreating(false)}
          onDone={onClose}
          title="Create index"
          subject={{
            name: `${candidate.schema ? `${candidate.schema}.` : ""}${candidate.table}`,
            facts: (
              <FormFact label="Columns" mono>
                {columns.join(", ")}
              </FormFact>
            ),
          }}
          request={real}
          waiting="Name at least one column."
          command="Create index"
          done={`Created an index on ${candidate.table}`}
          dirty={false}
        >
          <FormNote>
            Built without blocking writes, which takes longer and can leave an unusable index behind
            if it is interrupted.
          </FormNote>
        </ChangeDialog>
      )}
    </>
  )
}

function CandidateRow({
  name,
  candidate,
  selected,
  onSelect,
}: {
  name: string
  candidate: IndexCandidate
  selected: boolean
  onSelect: () => void
}) {
  return (
    <label
      className={cn(
        "flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 focus-ring-within transition-colors",
        selected
          ? "border-rule-brand bg-wash-brand"
          : "border-hairline hover:border-border-strong hover:bg-row-hover",
      )}
    >
      <input
        type="radio"
        name={name}
        className="sr-only"
        checked={selected}
        onChange={onSelect}
        aria-label={`${candidate.table} (${candidate.columns.join(", ")})`}
      />
      <span className="min-w-0 flex-1 space-y-0.5">
        <span className="block font-mono text-xs font-medium break-all">
          {candidate.schema ? `${candidate.schema}.` : ""}
          {candidate.table} ({columnsText(candidate.columns)})
        </span>
        <span className="block text-hint text-muted-foreground">{usesText(candidate)}</span>
      </span>
    </label>
  )
}

function WhatIfResult({ result }: { result: WhatIf }) {
  const change = costChange(result.beforeCost, result.afterCost)
  return (
    <div className="animate-rise space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <PlanSide
          label="Without the index"
          cost={result.beforeCost}
          lines={stepLines(result.before)}
        />
        <PlanSide
          label="With the index"
          cost={result.afterCost}
          lines={stepLines(result.after, result.indexName)}
          lit={result.used}
        />
      </div>
      <p className="text-body">
        The cost <span className="font-medium">{change.words}</span>
        <span className="text-muted-foreground">
          {result.used
            ? ", and the planner chose the index."
            : ". The planner did not choose this index."}
        </span>
      </p>
      <Statement label="Tried as a hypothetical index" sql={result.statement} placeholder="" />
    </div>
  )
}

function PlanSide({
  label,
  cost,
  lines,
  lit,
}: {
  label: string
  cost: number
  lines: StepLine[]
  lit?: boolean
}) {
  return (
    <section
      aria-label={label}
      className={cn(
        "min-w-0 space-y-2 rounded-lg border p-3",
        lit ? "border-rule-brand bg-wash-brand" : "border-hairline",
      )}
    >
      <p className="eyebrow">{label}</p>
      <p className="numeric text-2xl leading-tight font-semibold tracking-tight">
        {costText(cost)}
        <span className="pl-1.5 text-hint font-normal text-muted-foreground">cost</span>
      </p>
      <ol className="max-h-44 space-y-1 overflow-auto">
        {lines.map((line, i) => (
          <li
            key={i}
            style={{ paddingLeft: `${line.depth * 0.75}rem` }}
            className="flex min-w-0 items-baseline gap-1.5 text-xs"
          >
            <span className="min-w-0 break-words">{line.text}</span>
            {line.hypothetical && <Tag>hypothetical</Tag>}
          </li>
        ))}
      </ol>
    </section>
  )
}

/* ------------------------------------------------------------ missing indexes */

function MissingIndexesDialog({
  onClose,
  onOpenStatement,
}: {
  onClose: () => void
  onOpenStatement?: (sql: string, title?: string) => void
}) {
  const { id } = useDatabase()
  const { can } = useAuth()
  const [attempt, setAttempt] = useState(0)
  const [loaded, setLoaded] = useState<Loaded<MissingIndexes>>()
  const key = String(attempt)
  useEffect(() => {
    const controller = new AbortController()
    get<MissingIndexes>(`/databases/${id}/missing-indexes`, undefined, controller.signal)
      .then((value) => {
        if (!Array.isArray(value.indexes))
          throw new Error("The server answered in a form this page does not read.")
        setLoaded({ key, value })
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setLoaded({ key, error: error instanceof Error ? error : new Error(String(error)) })
        }
      })
    return () => controller.abort()
  }, [id, key])
  const answer = loaded?.key === key ? loaded : undefined
  const report = answer?.value

  return (
    <Modal
      open
      onOpenChange={(next) => !next && onClose()}
      size="lg"
      title="Missing indexes"
      description="The indexes SQL Server's optimizer wished for while compiling plans in this database"
      footer={
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="space-y-4">
        {!answer && <LoadingRows rows={3} />}
        {answer?.error && (
          <ErrorState error={answer.error} retryable onRetry={() => setAttempt((n) => n + 1)} />
        )}
        {report && !report.supported && (
          <NotAvailable title="The missing indexes cannot be read" reason={report.reason} />
        )}
        {report?.supported && report.indexes.length === 0 && (
          <EmptyState
            className="py-8"
            icon={ListOrdered}
            title="The optimizer has wished for no index"
            description={
              report.since
                ? `Nothing has been noted since the server started, ${timestamp(report.since)}.`
                : "Nothing has been noted in this database."
            }
          />
        )}
        {report?.supported && report.indexes.length > 0 && (
          <>
            <FormFacts>
              <FormFact label="Listed">
                {plural(report.indexes.length, "index", "indexes")}
              </FormFact>
              {report.since && (
                <FormFact label="Counted since the server started">
                  {timestamp(report.since)}
                </FormFact>
              )}
            </FormFacts>
            <ul aria-label="Missing indexes" className="divide-y divide-hairline">
              {report.indexes.map((index) => (
                <li key={index.statement} className="min-w-0 space-y-1.5 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5">
                    <p className="min-w-0 font-mono text-xs font-medium break-all">
                      {index.schema}.{index.table}
                    </p>
                    <FormFacts>
                      <FormFact label="Expected saving">
                        <span className="numeric">{Math.round(index.avgImpact)}%</span>
                      </FormFact>
                      <FormFact label="Wanted by">
                        <span className="numeric">
                          {plural(index.seeks + index.scans, "plan run", "plan runs")}
                        </span>
                      </FormFact>
                    </FormFacts>
                  </div>
                  <p className="font-mono text-hint break-words text-muted-foreground">
                    {missingColumnsText(index)}
                  </p>
                  <p className="font-mono text-hint break-words">{index.statement}</p>
                  <div className="flex gap-1.5">
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={() => void copyText(index.statement, "Statement copied")}
                    >
                      Copy
                    </Button>
                    {onOpenStatement && can("service.control") && (
                      <Button
                        size="xs"
                        variant="ghost"
                        onClick={() => {
                          onOpenStatement(index.statement, `Index on ${index.table}`)
                          onClose()
                        }}
                      >
                        Open in the editor
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </Modal>
  )
}
