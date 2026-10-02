"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Copy, Eye, EyeOff, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { previewPipeline, type MongoTarget } from "@/components/database/mongo/api"
import {
  STAGES,
  STAGE_KINDS,
  stageKind,
  stageSpec,
  withOperator,
  type Stage,
} from "@/components/database/mongo/aggregations/pipeline"
import { parseDocument, type BsonNode } from "@/components/database/mongo/bson"
import { BsonTree, useTreeRun } from "@/components/database/mongo/bson-tree"
import { CodeField } from "@/components/database/mongo/code-field"
import { stageColor } from "@/components/database/mongo/kinds"
import type { MongoDoc, MongoPreview } from "@/components/database/mongo/types"

/** How many documents a stage's preview asks for. */
const SAMPLE = 8
/** How long typing rests before a preview is asked for. */
const PAUSE_MS = 500

type PreviewState = { key: string; data?: MongoPreview; error?: Error }

/** How a stage's own preview came out, for the stages after it. */
export type PreviewOutcome = "ok" | "refused"

/**
 * What the pipeline holds after one stage: a few documents, read by running
 * the stages up to and including it.
 *
 * Only the stages *before and at* this one are sent, so editing a later
 * stage does not re-run this preview, and a mistake further down does not
 * blank it. The read waits for a pause in typing, and one that is overtaken
 * by another is dropped.
 */
function usePreview(
  target: MongoTarget,
  /** The pipeline up to this stage; `null` while a stage in it cannot be sent. */
  prefix: string | null,
  /** Where this stage sits in `prefix`. */
  index: number,
  auto: boolean,
  /** Changes when the reader asks for the previews again. */
  nonce: number,
  /** Told how the preview came out, each time one settles. */
  onSettled: (outcome: PreviewOutcome) => void,
) {
  const [state, setState] = useState<PreviewState>()
  const latest = useRef(prefix)
  const settled = useRef(onSettled)
  useEffect(() => {
    latest.current = prefix
    settled.current = onSettled
  })
  // With the automatic preview off, only the reader's own press reads again.
  const trigger = auto ? prefix : null
  const key = `${auto ? prefix : "manual"}\u0000${nonce}`
  useEffect(() => {
    const text = latest.current
    if (text === null || (!auto && nonce === 0)) return
    const controller = new AbortController()
    const timer = setTimeout(
      () => {
        previewPipeline(target, text, index, SAMPLE, controller.signal)
          .then((data) => {
            setState({ key, data })
            settled.current("ok")
          })
          .catch((err: unknown) => {
            if (controller.signal.aborted) return
            setState({ key, error: err instanceof Error ? err : new Error(String(err)) })
            settled.current("refused")
          })
      },
      auto ? PAUSE_MS : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // `key` stands for the prefix and the nonce.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, trigger, index, key])

  const waiting = prefix !== null && (auto || nonce > 0) && state?.key !== key
  return { state, waiting, idle: !auto && nonce === 0 }
}

/** An operator as it is written wherever stages are named: in its kind's hue. */
export function StageOperator({ op, className }: { op: string; className?: string }) {
  return (
    <span className={cn("font-mono", className)} style={{ color: stageColor(stageKind(op)) }}>
      {op}
    </span>
  )
}

/**
 * One stage of a pipeline: its operator, what the operator is given, and —
 * beside it — a sample of what the pipeline holds once the stage has run.
 *
 * The stage and its preview are one card because they are one thought: the
 * text on the left is only as right as the documents on the right. The
 * operator is written in the hue of what it does — keep, shape, group, join,
 * write — so a pipeline reads as its shape down the column. A stage that is
 * skipped stays in the pipeline's text as a comment and is left out of every
 * run.
 */
export function StageCard({
  target,
  stage,
  number,
  count,
  problem,
  prefix,
  index,
  auto,
  nonce,
  operators,
  refusedAt,
  onPreview,
  onChange,
  onMove,
  onDuplicate,
  onRemove,
  onRun,
}: {
  target: MongoTarget
  stage: Stage
  /** Its place in the list, from 1. */
  number: number
  count: number
  /** What is wrong with the stage as typed. */
  problem: string | null
  /** The enabled stages up to and including this one, as text; `null` when one of them cannot be sent. */
  prefix: string | null
  /** This stage's place among the enabled ones; `-1` when it is switched off. */
  index: number
  auto: boolean
  nonce: number
  /** Operators the pipeline uses that the builder's list does not have, so the picker can show them. */
  operators: readonly string[]
  /** The number of an earlier stage the server refused: this one's preview waits for it. */
  refusedAt: number | null
  onPreview: (outcome: PreviewOutcome) => void
  onChange: (stage: Stage) => void
  onMove: (by: -1 | 1) => void
  onDuplicate: () => void
  onRemove: () => void
  /** Ctrl+Enter / Cmd+Enter in the stage's text: run the pipeline, where it can be. */
  onRun?: () => void
}) {
  const spec = stageSpec(stage.op)
  // A stage behind one the server refused is not asked for: its answer would
  // be that stage's refusal over again.
  const preview = usePreview(
    target,
    stage.enabled && refusedAt === null ? prefix : null,
    index,
    auto,
    nonce,
    onPreview,
  )
  const label = `Stage ${number}, ${stage.op}`
  const bodyId = `mongo-stage-${stage.id}`
  const foreign = operators.filter((op) => !stageSpec(op))

  return (
    // A fence, because the stage and its preview are one thing among several in a column.
    <li
      data-slot="mongo-stage"
      data-enabled={stage.enabled || undefined}
      aria-label={label}
      className="@container min-w-0 overflow-hidden rounded-lg border border-hairline"
    >
      <div className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-2 py-1">
        <span className="numeric w-5 shrink-0 text-center text-hint text-muted-foreground">
          {number}
        </span>
        <Select value={stage.op} onValueChange={(op) => onChange(withOperator(stage, op))}>
          <SelectTrigger
            size="sm"
            aria-label={`Operator of stage ${number}`}
            className={cn(
              "h-7 gap-1.5 px-2 font-mono text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7",
              !stage.enabled && "opacity-60",
            )}
            style={{ color: stageColor(spec?.kind) }}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper" align="start" className="max-h-80">
            {foreign.length > 0 && (
              <SelectGroup>
                <SelectLabel>In this pipeline</SelectLabel>
                {foreign.map((op) => (
                  <SelectItem key={op} value={op} className="font-mono text-xs">
                    {op}
                  </SelectItem>
                ))}
              </SelectGroup>
            )}
            {STAGE_KINDS.map(({ kind, label: word }) => (
              <SelectGroup key={kind}>
                <SelectLabel>{word}</SelectLabel>
                {STAGES.filter((entry) => entry.kind === kind).map((entry) => (
                  <SelectItem
                    key={entry.op}
                    value={entry.op}
                    className="font-mono text-xs"
                    style={{ color: stageColor(kind) }}
                    hint={<span className="font-sans">{entry.hint}</span>}
                  >
                    {entry.op}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-hint text-muted-foreground",
            !stage.enabled && "opacity-60",
          )}
        >
          {spec?.hint ?? "Not one of the stages the builder knows: the server decides what it does"}
        </span>
        {!stage.enabled && <Tag>skipped</Tag>}
        <div className="flex shrink-0 items-center gap-0.5">
          <IconAction
            label={`Move stage ${number} up`}
            className="size-7"
            disabled={number === 1}
            onClick={() => onMove(-1)}
          >
            <ArrowUp />
          </IconAction>
          <IconAction
            label={`Move stage ${number} down`}
            className="size-7"
            disabled={number === count}
            onClick={() => onMove(1)}
          >
            <ArrowDown />
          </IconAction>
          <IconAction label={`Duplicate stage ${number}`} className="size-7" onClick={onDuplicate}>
            <Copy />
          </IconAction>
          <IconAction
            // One of the stage's controls, as quiet as the rest: a switch here
            // was the brightest thing on the page, on every card.
            label={stage.enabled ? `Skip stage ${number}` : `Run stage ${number} again`}
            aria-pressed={!stage.enabled}
            className={cn("size-7", !stage.enabled && "bg-accent")}
            onClick={() => onChange({ ...stage, enabled: !stage.enabled })}
          >
            {stage.enabled ? <Eye /> : <EyeOff />}
          </IconAction>
          <IconAction label={`Remove stage ${number}`} className="size-7" onClick={onRemove}>
            <Trash />
          </IconAction>
        </div>
      </div>

      <div className="grid min-w-0 @3xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
        <div className={cn("min-w-0 space-y-1 p-2", !stage.enabled && "opacity-60")}>
          <CodeField
            id={bodyId}
            value={stage.body}
            invalid={Boolean(problem)}
            aria-label={`What ${stage.op} is given, stage ${number}`}
            aria-describedby={problem ? `${bodyId}-problem` : undefined}
            className="max-h-96 min-h-16"
            onChange={(body) => onChange({ ...stage, body })}
            onSubmit={onRun}
          />
          {problem && (
            <p
              id={`${bodyId}-problem`}
              role="alert"
              className="text-hint leading-relaxed text-destructive"
            >
              {problem}
            </p>
          )}
        </div>
        <StagePreview
          stage={stage}
          number={number}
          blocked={stage.enabled && prefix === null}
          refusedAt={stage.enabled ? refusedAt : null}
          preview={preview}
        />
      </div>
    </li>
  )
}

function StagePreview({
  stage,
  number,
  blocked,
  refusedAt,
  preview,
}: {
  stage: Stage
  number: number
  /** A stage up to this one cannot be sent as typed. */
  blocked: boolean
  /** The number of an earlier stage the server refused. */
  refusedAt: number | null
  preview: ReturnType<typeof usePreview>
}) {
  const { state, waiting, idle } = preview
  const data = state?.data
  const held = stage.enabled && !blocked && refusedAt === null

  let note: React.ReactNode
  if (!stage.enabled) note = "Skipped: every run leaves this stage out."
  else if (refusedAt !== null) note = `Shown once stage ${refusedAt} can be run.`
  else if (blocked) note = "Shown once every stage up to here can be read."
  else if (idle) note = "Press Preview to see what the pipeline holds after this stage."
  else if (!state && waiting) note = null
  else if (state?.error) note = null
  else if (data && data.documents.length === 0) {
    note = data.writeStage
      ? `${data.writeStage} is not run in a preview, and nothing reaches it.`
      : "No document is left after this stage."
  }

  return (
    <div
      data-slot="mongo-stage-preview"
      aria-label={`Documents after stage ${number}`}
      className="min-w-0 border-hairline p-2 @max-3xl:border-t @3xl:border-l"
    >
      <div className="flex min-h-5 items-center gap-2 pb-1.5 text-hint text-muted-foreground">
        {waiting && <Spinner className="size-3" />}
        {data && held && (
          <span className={cn("min-w-0 truncate", waiting && "opacity-60")}>
            {data.writeStage
              ? `What reaches ${data.writeStage}: it is not run in a preview`
              : `${data.returned.toLocaleString("en-US")} sample ${data.returned === 1 ? "document" : "documents"} after ${stage.op}`}
            {data.inputLimited
              ? ` · over the first ${data.inputLimit.toLocaleString("en-US")} input documents`
              : ""}
            {` · ${data.durationMs.toLocaleString("en-US")} ms`}
          </span>
        )}
      </div>
      {state?.error && held ? (
        <p
          role="alert"
          className="font-mono text-hint leading-relaxed break-words text-destructive"
        >
          {state.error.message}
        </p>
      ) : note ? (
        <p className="py-4 text-center text-hint text-muted-foreground">{note}</p>
      ) : !data ? (
        <SamplesSkeleton />
      ) : (
        <Samples
          documents={data.documents}
          label={(at) => `Sample document ${at + 1} after stage ${number}`}
          className={cn(waiting && "opacity-60 transition-opacity")}
        />
      )}
    </div>
  )
}

function SamplesSkeleton() {
  return (
    <div aria-hidden className="flex divide-x divide-hairline overflow-hidden">
      {[0, 1, 2].map((n) => (
        <div key={n} className="w-88 shrink-0 space-y-2 px-3 py-1 first:pl-0">
          {[70, 52, 60, 44].map((width, line) => (
            <Skeleton key={line} className="h-3" style={{ width: `${width - n * 6}%` }} />
          ))}
        </div>
      ))}
    </div>
  )
}

/**
 * A few documents side by side, each a tree of its fields. They stand on the
 * ground of whatever holds them with a hairline between one and the next:
 * the output of the stage beside them, not boxes of their own. The run is
 * one stop for Tab, and the arrows walk its rows.
 */
function Samples({
  documents,
  label,
  className,
}: {
  documents: readonly MongoDoc[]
  /** What each tree is, for a reader who cannot see it. */
  label: (at: number) => string
  className?: string
}) {
  const trees = useMemo(
    () =>
      documents.map((doc) => {
        try {
          return parseDocument(doc.canonical)
        } catch {
          return null
        }
      }),
    [documents],
  )
  const keys = useMemo(() => trees.map((_, at) => String(at)), [trees])
  const run = useTreeRun(keys)
  return (
    <ol
      className={cn("flex min-w-0 divide-x divide-hairline overflow-x-auto pb-1", className)}
      onKeyDown={run.onKeyDown}
    >
      {trees.map((tree, at) => (
        <li
          key={at}
          className="max-h-64 w-88 shrink-0 overflow-y-auto px-3 first:pl-0"
          onFocusCapture={run.enter(keys[at])}
        >
          {tree ? (
            <BsonTree
              root={tree as BsonNode}
              label={label(at)}
              expand={0}
              tabbable={run.active === keys[at]}
            />
          ) : (
            <pre className="font-mono text-hint break-all whitespace-pre-wrap">
              {documents[at].canonical}
            </pre>
          )}
        </li>
      ))}
    </ol>
  )
}

/**
 * What enters the pipeline: a few of the collection's own documents, drawn
 * over the first stage the way each stage's output is drawn beside it. The
 * fields a first `$match` or `$group` can name are then in sight while it is
 * written, and a builder with no stage yet is not an empty pane.
 */
export function PipelineInput({
  target,
  collection,
  count,
}: {
  target: MongoTarget
  collection: string
  /** How many documents the collection holds, where that is known. */
  count?: number
}) {
  const poll = usePoll((signal) => previewPipeline(target, "[]", -1, SAMPLE, signal), 0, [
    target.id,
    target.database,
    target.collection,
  ])
  const data = poll.data
  return (
    // A fence, like the stages under it: the head of the same column.
    <section
      data-slot="mongo-pipeline-input"
      aria-label="Documents entering the pipeline"
      className="min-w-0 overflow-hidden rounded-lg border border-hairline"
    >
      <div className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-3 py-1">
        <h3 className="eyebrow">Input</h3>
        <span className="font-mono text-xs">{collection}</span>
        <span className="numeric min-w-0 flex-1 truncate text-hint text-muted-foreground">
          {data
            ? `${data.returned.toLocaleString("en-US")} of ${
                count === undefined ? "its" : `its ${count.toLocaleString("en-US")}`
              } documents, as they reach the first stage`
            : ""}
        </span>
      </div>
      <div className="p-2 pl-3">
        {poll.error && !data ? (
          <p role="alert" className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
            <span className="min-w-0 text-destructive">
              The collection&rsquo;s documents could not be read: {poll.error.message}
            </span>
            <Button size="xs" variant="outline" onClick={poll.refresh}>
              Try again
            </Button>
          </p>
        ) : !data ? (
          <SamplesSkeleton />
        ) : data.documents.length === 0 ? (
          <p className="py-3 text-center text-hint text-muted-foreground">
            The collection holds no documents: every stage will have nothing to work on.
          </p>
        ) : (
          <Samples
            documents={data.documents}
            label={(at) => `Sample document ${at + 1} of ${collection}`}
            className="animate-rise"
          />
        )}
      </div>
    </section>
  )
}
