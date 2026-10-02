"use client"

import { CodeView } from "@/components/database/kit"
import { explain, type MongoTarget } from "@/components/database/mongo/api"
import { parseJson, printJson } from "@/components/database/mongo/bson"
import type {
  MongoExplain,
  MongoExplainVerbosity,
  MongoFindSpec,
  MongoPlanNode,
} from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"
import { Segments } from "@/components/deploy/settings/segments"
import { FormNote } from "@/components/form"
import { Meter } from "@/components/meter"
import { Modal } from "@/components/modal"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { cn } from "@/lib/utils"
import { useEffect, useMemo, useState } from "react"

/** What is explained: a find as the query bar states it, or a pipeline. */
export type ExplainSubject =
  | { kind: "find"; spec: MongoFindSpec; statement: string }
  | { kind: "pipeline"; pipeline: string; statement: string }

const grouped = (n: number | null) => (n === null ? "—" : n.toLocaleString("en-US"))

/** Every node of a plan with how deep it sits, the last thing done first. */
function flatten(node: MongoPlanNode, depth = 0): { node: MongoPlanNode; depth: number }[] {
  return [{ node, depth }, ...node.children.flatMap((child) => flatten(child, depth + 1))]
}

/**
 * How the server would run a query, as the tree of steps it takes.
 *
 * The plan reads from the top down: the first row is the last thing done,
 * the rows under it are what fed it, and the leaves are the scans. Planning
 * costs nothing; "Run it" executes the query — a read — and fills in what
 * each step examined and returned, which is where a collection scan or a
 * sort in memory shows what it costs.
 */
export function ExplainDialog({
  mongo,
  target,
  subject,
  onOpenChange,
}: {
  mongo: Mongo
  target: MongoTarget
  /** `null` closes the dialog. */
  subject: ExplainSubject | null
  onOpenChange: (open: boolean) => void
}) {
  if (!subject) return null
  return <Explain mongo={mongo} target={target} subject={subject} onOpenChange={onOpenChange} />
}

function Explain({
  mongo,
  target,
  subject,
  onOpenChange,
}: {
  mongo: Mongo
  target: MongoTarget
  subject: ExplainSubject
  onOpenChange: (open: boolean) => void
}) {
  const { engine } = mongo
  const canRun = engine.can("explainAnalyze")
  const [verbosity, setVerbosity] = useState<MongoExplainVerbosity>("queryPlanner")
  const [view, setView] = useState<"tree" | "raw">("tree")
  const [answer, setAnswer] = useState<{ verbosity: string; data?: MongoExplain; error?: Error }>()
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    const request =
      subject.kind === "pipeline"
        ? { pipeline: subject.pipeline, verbosity }
        : { ...subject.spec, verbosity }
    explain(target, request, controller.signal)
      .then((data) => setAnswer({ verbosity, data }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setAnswer({ verbosity, error: err instanceof Error ? err : new Error(String(err)) })
      })
    return () => controller.abort()
  }, [target, subject, verbosity, attempt])

  const loading = answer?.verbosity !== verbosity
  const data = answer?.data
  const summary = data?.summary
  const rows = useMemo(() => (data ? flatten(data.plan) : []), [data])
  const mostExamined = Math.max(...rows.map((row) => row.node.docsExamined ?? 0), 1)
  const raw = useMemo(() => {
    if (!data) return ""
    try {
      return printJson(parseJson(data.raw), true)
    } catch {
      return data.raw
    }
  }, [data])

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      size="xl"
      title="Explain"
      description={`How the server runs ${subject.statement}`}
      initialFocus="body"
      bodyClassName="space-y-4 p-4"
      actions={
        canRun && (
          <Segments
            label="What to explain with"
            value={verbosity === "queryPlanner" ? "queryPlanner" : "executionStats"}
            options={[
              { value: "queryPlanner", label: "Plan only" },
              { value: "executionStats", label: "Run it" },
            ]}
            onChange={(next) => setVerbosity(next)}
          />
        )
      }
    >
      <p className="font-mono text-xs break-all text-muted-foreground">{subject.statement}</p>

      {answer?.error && !loading ? (
        <ReadError error={answer.error} onRetry={() => setAttempt((n) => n + 1)} />
      ) : !data || loading ? (
        <LoadingRows rows={6} />
      ) : (
        <div className="animate-rise space-y-4" key={answer.verbosity}>
          {data.note && <FormNote tone="warning">{data.note}</FormNote>}
          {!summary?.executed && (
            <FormNote>
              This is the plan without running it, so no step has figures.
              {canRun ? " Run it to see what each step examines and returns." : ""}
            </FormNote>
          )}

          {summary?.executed && (
            <p className="text-body text-muted-foreground">
              Returned {grouped(summary.returned)} documents
              {summary.timeMs !== null ? ` in ${grouped(summary.timeMs)} ms (server estimate)` : ""}
              ; examined {grouped(summary.docsExamined)} documents and{" "}
              {grouped(summary.keysExamined)} index keys.
            </p>
          )}
          {summary && (
            <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5">
              {summary.collectionScan ? (
                <Status tone="warning" label="Scans the whole collection" />
              ) : summary.indexesUsed.length > 0 ? (
                <span className="flex items-center gap-1.5">
                  <Status tone="running" label="Uses an index" />
                  {summary.indexesUsed.map((name) => (
                    <Tag key={name} mono>
                      {name}
                    </Tag>
                  ))}
                </span>
              ) : null}
              {summary.inMemorySort && <Status tone="warning" label="Sorts in memory" />}
              {summary.usedDisk && <Status tone="warning" label="Spills to disk" />}
              {summary.rejectedPlans > 0 && (
                <span className="text-hint text-muted-foreground">
                  {summary.rejectedPlans} other{" "}
                  {summary.rejectedPlans === 1 ? "plan was" : "plans were"} considered
                </span>
              )}
              <span className="text-hint text-muted-foreground">
                {summary.engine === "sbe" ? "slot-based engine" : "classic engine"}
              </span>
            </div>
          )}

          <div
            role="group"
            aria-label="Explain views"
            className="flex gap-1 border-b border-hairline"
          >
            {(["tree", "raw"] as const).map((entry) => (
              <button
                key={entry}
                type="button"
                aria-pressed={view === entry}
                onClick={() => setView(entry)}
                className={tabClasses(view === entry, "h-9")}
              >
                {entry === "tree" ? "Plan" : "Raw output"}
              </button>
            ))}
          </div>

          {view === "raw" ? (
            <CodeView code={raw} language="json" label="The server's reply" className="h-96" />
          ) : (
            <ol aria-label="Plan steps, the last one done first" className="min-w-0">
              <li
                aria-hidden
                className="flex items-center gap-3 border-b border-hairline pb-1.5 text-hint font-medium text-muted-foreground"
              >
                <span className="min-w-0 flex-1">Step</span>
                <span className="w-20 text-right">Returned</span>
                <span className="w-20 text-right max-sm:hidden">Documents</span>
                <span className="w-16 text-right max-sm:hidden">Keys</span>
                <span className="w-14 text-right">ms</span>
              </li>
              {rows.map(({ node, depth }, index) => (
                <PlanRow
                  key={index}
                  node={node}
                  depth={depth}
                  executed={Boolean(summary?.executed)}
                  share={(node.docsExamined ?? 0) / mostExamined}
                />
              ))}
            </ol>
          )}
        </div>
      )}
    </Modal>
  )
}

function PlanRow({
  node,
  depth,
  executed,
  share,
}: {
  node: MongoPlanNode
  depth: number
  executed: boolean
  share: number
}) {
  const scan = node.stage === "COLLSCAN"
  const detail = [
    node.index && `index ${node.index}`,
    node.direction,
    node.indexBounds && `bounds ${node.indexBounds}`,
    node.filter,
  ].filter(Boolean)
  return (
    <li className="border-b border-hairline py-1.5 last:border-b-0">
      <div className="flex min-w-0 items-center gap-3">
        <div
          className="flex min-w-0 flex-1 items-center gap-2"
          style={{ paddingLeft: `${depth * 16}px` }}
        >
          {depth > 0 && (
            <span aria-hidden className="shrink-0 text-muted-foreground/50">
              └
            </span>
          )}
          <span className={cn("shrink-0 font-mono text-xs font-medium", scan && "text-warning")}>
            {node.stage}
          </span>
          {detail.length > 0 && (
            <span
              className="min-w-0 truncate font-mono text-hint text-muted-foreground"
              title={detail.join(" · ")}
            >
              {detail.join(" · ")}
            </span>
          )}
        </div>
        <span className="numeric w-20 shrink-0 text-right text-xs">{grouped(node.returned)}</span>
        <span className="numeric w-20 shrink-0 text-right text-xs max-sm:hidden">
          {grouped(node.docsExamined)}
        </span>
        <span className="numeric w-16 shrink-0 text-right text-xs max-sm:hidden">
          {grouped(node.keysExamined)}
        </span>
        <span className="numeric w-14 shrink-0 text-right text-xs text-muted-foreground">
          {grouped(node.timeMs)}
        </span>
      </div>
      {executed && node.docsExamined !== null && node.docsExamined > 0 && (
        <div style={{ paddingLeft: `${depth * 16 + (depth > 0 ? 20 : 0)}px` }} className="pt-1">
          <Meter
            size="thin"
            value={share * 100}
            tone={scan ? "warning" : "default"}
            label={`${node.stage} examined ${grouped(node.docsExamined)} documents`}
          />
        </div>
      )}
    </li>
  )
}
