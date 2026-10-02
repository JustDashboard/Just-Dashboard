"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Copy, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Spinner } from "@/components/state"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { previewPipeline, type MongoTarget } from "@/components/database/mongo/api"
import {
  STAGES,
  stageSpec,
  withOperator,
  type Stage,
} from "@/components/database/mongo/aggregations/pipeline"
import { parseDocument, type BsonNode } from "@/components/database/mongo/bson"
import { BsonTree } from "@/components/database/mongo/bson-tree"
import { CodeField } from "@/components/database/mongo/code-field"
import type { MongoPreview } from "@/components/database/mongo/types"

/** How many documents a stage's preview asks for. */
const SAMPLE = 8
/** How long typing rests before a preview is asked for. */
const PAUSE_MS = 500

type PreviewState = { key: string; data?: MongoPreview; error?: Error }

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
) {
  const [state, setState] = useState<PreviewState>()
  const latest = useRef(prefix)
  useEffect(() => {
    latest.current = prefix
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
          .then((data) => setState({ key, data }))
          .catch((err: unknown) => {
            if (controller.signal.aborted) return
            setState({ key, error: err instanceof Error ? err : new Error(String(err)) })
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

/**
 * One stage of a pipeline: its operator, what the operator is given, and —
 * beside it — a sample of what the pipeline holds once the stage has run.
 *
 * The stage and its preview are one card because they are one thought: the
 * text on the left is only as right as the documents on the right. A stage
 * that is switched off stays in the pipeline's text as a comment and is
 * skipped by every run.
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
  onChange,
  onMove,
  onDuplicate,
  onRemove,
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
  onChange: (stage: Stage) => void
  onMove: (by: -1 | 1) => void
  onDuplicate: () => void
  onRemove: () => void
}) {
  const spec = stageSpec(stage.op)
  const preview = usePreview(target, stage.enabled ? prefix : null, index, auto, nonce)
  const label = `Stage ${number}, ${stage.op}`
  const bodyId = `mongo-stage-${stage.id}`

  return (
    // A fence, because the stage and its preview are one thing among several in a column.
    <li
      data-slot="mongo-stage"
      data-enabled={stage.enabled || undefined}
      aria-label={label}
      className={cn(
        "@container min-w-0 overflow-hidden rounded-lg border border-hairline",
        !stage.enabled && "opacity-60",
      )}
    >
      <div className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline bg-surface-header px-2 py-1">
        <span className="numeric w-5 shrink-0 text-center text-hint text-muted-foreground">
          {number}
        </span>
        <Select value={stage.op} onValueChange={(op) => onChange(withOperator(stage, op))}>
          <SelectTrigger
            size="sm"
            aria-label={`Operator of stage ${number}`}
            className="h-7 gap-1.5 px-2 font-mono text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper" align="start" className="max-h-80">
            {operators
              .filter((op) => !stageSpec(op))
              .map((op) => (
                <SelectItem key={op} value={op} className="font-mono text-xs">
                  {op}
                </SelectItem>
              ))}
            {STAGES.map((entry) => (
              <SelectItem
                key={entry.op}
                value={entry.op}
                className="font-mono text-xs"
                hint={<span className="font-sans">{entry.hint}</span>}
              >
                {entry.op}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
          {spec?.hint ?? "Not one of the stages the builder knows: the server decides what it does"}
        </span>
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
          <IconAction label={`Remove stage ${number}`} className="size-7" onClick={onRemove}>
            <Trash />
          </IconAction>
          <Switch
            aria-label={`Stage ${number} is part of the pipeline`}
            checked={stage.enabled}
            onCheckedChange={(enabled) => onChange({ ...stage, enabled })}
            className="ml-1.5"
          />
        </div>
      </div>

      <div className="grid min-w-0 @3xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
        <div className="min-w-0 space-y-1 p-2">
          <CodeField
            id={bodyId}
            value={stage.body}
            invalid={Boolean(problem)}
            aria-label={`What ${stage.op} is given, stage ${number}`}
            aria-describedby={problem ? `${bodyId}-problem` : undefined}
            className="max-h-96 min-h-16"
            onChange={(body) => onChange({ ...stage, body })}
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
  preview,
}: {
  stage: Stage
  number: number
  /** A stage up to this one cannot be sent as typed. */
  blocked: boolean
  preview: ReturnType<typeof usePreview>
}) {
  const { state, waiting, idle } = preview
  const data = state?.data
  const trees = useMemo(
    () =>
      (data?.documents ?? []).map((doc) => {
        try {
          return parseDocument(doc.canonical)
        } catch {
          return null
        }
      }),
    [data],
  )

  let note: React.ReactNode
  if (!stage.enabled) note = "Switched off: every run skips this stage."
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
        {data && stage.enabled && !blocked && (
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
      {state?.error && stage.enabled && !blocked ? (
        <p
          role="alert"
          className="font-mono text-hint leading-relaxed break-words text-destructive"
        >
          {state.error.message}
        </p>
      ) : note ? (
        <p className="py-4 text-center text-hint text-muted-foreground">{note}</p>
      ) : !data ? (
        <div aria-hidden className="flex gap-2">
          {[0, 1, 2].map((n) => (
            <div
              key={n}
              className="h-24 w-72 shrink-0 rounded-md border border-hairline bg-surface-sunken"
            />
          ))}
        </div>
      ) : (
        <ol
          className={cn(
            "flex min-w-0 gap-2 overflow-x-auto pb-1",
            waiting && "opacity-60 transition-opacity",
          )}
        >
          {trees.map((tree, at) => (
            <li
              key={at}
              // A well: output to read, recessed into the card that produced it.
              className="max-h-64 w-72 shrink-0 overflow-auto rounded-md border border-hairline bg-surface-sunken p-2"
            >
              {tree ? (
                <BsonTree root={tree as BsonNode} expand={0} />
              ) : (
                <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                  {data.documents[at].canonical}
                </pre>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
