"use client"

import { useMemo, useRef, useState } from "react"
import {
  ChevronDown,
  Copy,
  Download,
  Eye,
  FloppyDisk,
  Play,
  Plus,
  RefreshClockwise,
  Trash,
} from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { EmptyState, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import type { Verb } from "@/components/verbs"
import { useGridLayout } from "@/components/database/grid"
import { EngineMark, downloadText } from "@/components/database/kit"
import {
  STAGES,
  enabledStages,
  exportText,
  listText,
  moved,
  newStage,
  parsePipeline,
  pipelineKind,
  pipelineText,
  previewIndex,
  sameAsSaved,
  stageProblem,
  withSaved,
  withoutSaved,
  type SavedPipeline,
  type Stage,
} from "@/components/database/mongo/aggregations/pipeline"
import { StageCard } from "@/components/database/mongo/aggregations/stage-card"
import { runPipeline } from "@/components/database/mongo/api"
import { CodeField } from "@/components/database/mongo/code-field"
import { editorText } from "@/components/database/mongo/documents/document-editor"
import {
  JsonView,
  ListView,
  TableView,
  useListed,
} from "@/components/database/mongo/documents/views"
import { ExplainDialog, type ExplainSubject } from "@/components/database/mongo/explain"
import { CollectionMark, collectionKind } from "@/components/database/mongo/kinds"
import type { MongoAggregateResult, MongoDoc } from "@/components/database/mongo/types"
import type { Workbench } from "@/components/database/mongo/workbench"

const LIMITS = ["100", "500", "1000"] as const
type Limit = (typeof LIMITS)[number]

type ResultView = "list" | "json" | "table"

/** A run's answer, with the pipeline it is the answer to. */
type Ran = { pipeline: string; limit: number; result?: MongoAggregateResult; error?: string }

const uid = () => crypto.randomUUID()

/** The pipelines saved for each collection of a connection, by `database` and `collection`. */
export const savedKey = (id: number) => `databases.${id}.mongo.pipelines`

/**
 * The aggregation builder: a pipeline as a column of stages, each with a
 * sample of what it leaves beside it.
 *
 * The stages are the reader's work in progress, so they are kept for the tab
 * per collection: a look at another page, or at another collection, does not
 * lose them. A run's result is kept with the pipeline it came from, and is
 * said to be of an earlier pipeline once the stages have moved on. Whether a
 * pipeline writes is read off its stage operators, the way the server reads
 * it, and a run that writes is confirmed and asked only of a role that may.
 */
export function PipelineView({
  mongo,
  collection: info,
  confirm,
  newCollection,
}: Pick<Workbench, "mongo" | "collection" | "confirm" | "newCollection">) {
  const { id, target, database, collection, engine, readOnly, canRun, canDestroy } = mongo
  const scope = `${database}\u0000${collection}`

  const [stages, setStages] = useSessionState<Stage[]>(
    `databases.${id}.mongo.pipeline.${scope}`,
    [],
  )
  const [savedAll, setSavedAll] = useViewState<Record<string, SavedPipeline[]>>(savedKey(id), {})
  const saved = useMemo(() => savedAll[scope] ?? [], [savedAll, scope])
  const [loadedName, setLoadedName] = useSessionState(
    `databases.${id}.mongo.pipeline.name.${scope}`,
    "",
  )
  const loaded = saved.find((entry) => entry.name === loadedName)
  const [auto, setAuto] = useViewState(`databases.${id}.mongo.autoPreview`, true)
  const [nonce, setNonce] = useState(0)

  const [limit, setLimit] = useViewState<Limit>(`databases.${id}.mongo.aggregate.limit`, "100")
  const [ran, setRan] = useState<Ran | null>(null)
  const [running, setRunning] = useState(false)
  const [resultView, setResultView] = useViewState<ResultView>(
    `databases.${id}.mongo.aggregate.view`,
    "list",
  )
  const resultRef = useRef<HTMLElement>(null)

  const [saving, setSaving] = useState(false)
  const [texting, setTexting] = useState(false)
  const [explaining, setExplaining] = useState<ExplainSubject | null>(null)

  const problems = useMemo(() => stages.map(stageProblem), [stages])
  const enabled = enabledStages(stages)
  const kind = pipelineKind(stages)
  const text = pipelineText(stages)
  const sendable = stages.every((stage, index) => !stage.enabled || problems[index] === null)
  const writes = kind.writes !== null || kind.unknown.length > 0
  const mayRun = canRun && (!writes || (canDestroy && !readOnly))
  // Operators in the pipeline that the builder's list does not have: kept in the picker.
  const foreign = useMemo(
    () =>
      [...new Set(stages.map((stage) => stage.op))].filter(
        (op) => !STAGES.some((s) => s.op === op),
      ),
    [stages],
  )

  const change = (next: Stage[]) => setStages(next)
  const add = (op = "$match") => change([...stages, newStage(uid(), op)])

  /** The enabled stages up to and including the one at `index`, as text; `null` when one cannot be sent. */
  const prefixAt = (index: number): string | null => {
    const upTo = stages.slice(0, index + 1)
    if (upTo.some((stage, at) => stage.enabled && problems[at] !== null)) return null
    return pipelineText(upTo)
  }

  const run = async () => {
    setRunning(true)
    const asked = { pipeline: text, limit: Number(limit) }
    try {
      const result = await runPipeline(target, text, Number(limit))
      setRan({ ...asked, result })
      if (result.writes) notify.success("The pipeline ran, and wrote its result")
    } catch (err) {
      setRan({ ...asked, error: errorMessage(err) })
    } finally {
      setRunning(false)
      requestAnimationFrame(() =>
        resultRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }),
      )
    }
  }

  const pressRun = () => {
    if (!writes) {
      void run()
      return
    }
    const request: ConfirmRequest = {
      title: "Run a pipeline that writes",
      description: kind.writes ? (
        <>
          The pipeline ends in <span className="font-mono">{kind.writes}</span>:{" "}
          {kind.writes === "$out"
            ? "the collection it names is replaced by the result."
            : "the result is written into the collection it names."}
        </>
      ) : (
        <>
          <span className="font-mono">{kind.unknown.join(", ")}</span>{" "}
          {kind.unknown.length === 1 ? "is not a stage" : "are not stages"} the dashboard knows to
          only read, so the server treats this run as one that may write.
        </>
      ),
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{collection}</span>,
        facts: (
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
            <FormFact label="Stages" mono>
              {enabled.map((stage) => stage.op).join(" → ")}
            </FormFact>
          </FormFacts>
        ),
      },
      confirmLabel: "Run it",
      action: async () => {
        await run()
        return "reported"
      },
    }
    confirm(request)
  }

  const load = (entry: SavedPipeline) => {
    change(entry.stages.map((stage) => ({ ...stage, id: uid() })))
    setLoadedName(entry.name)
    setRan(null)
  }
  const store = (name: string) => {
    const entry: SavedPipeline = {
      name,
      stages: stages.map(({ op, body, enabled: on }) => ({ op, body, enabled: on })),
      savedAt: Date.now(),
    }
    setSavedAll((held) => ({ ...held, [scope]: withSaved(held[scope] ?? [], entry) }))
    setLoadedName(name)
    notify.success(`Saved as ${name}`)
  }
  const forget = (name: string) => {
    setSavedAll((held) => ({ ...held, [scope]: withoutSaved(held[scope] ?? [], name) }))
    if (name === loadedName) setLoadedName("")
  }

  const unsaved = stages.length > 0 && !sameAsSaved(stages, loaded)
  const stale = ran !== null && (ran.pipeline !== text || ran.limit !== Number(limit))
  const shell = exportText(collection, stages)
  const markKind = info ? collectionKind(info) : "collection"

  return (
    <div data-slot="mongo-pipeline" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-3 py-1">
        <h2 className="flex min-w-0 items-center gap-1.5">
          <CollectionMark kind={markKind} />
          <span className="min-w-0 truncate font-mono text-sm leading-6 font-medium">
            {collection}
          </span>
        </h2>
        <span className="min-w-0 truncate text-hint text-muted-foreground">
          {loaded ? loaded.name : stages.length > 0 ? "not saved" : ""}
          {loaded && unsaved ? " · changed" : ""}
        </span>
        <span className="min-w-0 flex-1" />
        <div className="flex min-w-0 flex-wrap items-center gap-1">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="xs" variant="ghost">
                Saved
                <span className="numeric text-muted-foreground">{saved.length}</span>
                <ChevronDown className="size-3" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-72">
              <DropdownMenuLabel>Pipelines saved for {collection}</DropdownMenuLabel>
              {saved.length === 0 ? (
                <p className="px-2 py-3 text-hint text-muted-foreground">
                  None yet. A saved pipeline is kept in this browser.
                </p>
              ) : (
                saved.map((entry) => (
                  <DropdownMenuItem
                    key={entry.name}
                    className={cn("items-baseline gap-3", entry.name === loadedName && "bg-accent")}
                    onSelect={() => load(entry)}
                  >
                    <span className="min-w-0 flex-1 truncate">{entry.name}</span>
                    <span className="numeric shrink-0 text-hint text-muted-foreground">
                      {entry.stages.length} {entry.stages.length === 1 ? "stage" : "stages"}
                    </span>
                  </DropdownMenuItem>
                ))
              )}
              {loaded && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onSelect={() => forget(loaded.name)}>
                    <Trash className="size-3.5" />
                    Forget {loaded.name}
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            size="xs"
            variant="ghost"
            disabled={stages.length === 0}
            onClick={() => setSaving(true)}
          >
            <FloppyDisk />
            Save
          </Button>
          <Button size="xs" variant="ghost" onClick={() => setTexting(true)}>
            As text
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="xs" variant="ghost" disabled={stages.length === 0}>
                Export
                <ChevronDown className="size-3" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => void copyText(shell, "Pipeline copied")}>
                <Copy className="size-3.5" />
                Copy as shell text
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => downloadText(`${shell}\n`, `${collection}-pipeline.js`)}
              >
                <Download className="size-3.5" />
                Download as a file
              </DropdownMenuItem>
              {newCollection && engine.can("views") && info?.type !== "view" && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    // A view may not write, and is made of stages the server can read.
                    disabled={!sendable || writes || enabled.length === 0}
                    onSelect={() =>
                      newCollection({ viewOn: collection, pipeline: listText(stages) })
                    }
                  >
                    <Eye className="size-3.5" />
                    Create a view from it…
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          <label className="flex h-7 cursor-pointer items-center gap-1.5 px-1.5 text-xs font-medium">
            <Switch
              checked={auto}
              onCheckedChange={setAuto}
              aria-label="Preview each stage as it is typed"
            />
            Auto-preview
          </label>
          {!auto && (
            <Button
              size="xs"
              variant="outline"
              disabled={!sendable || enabled.length === 0}
              onClick={() => setNonce((n) => n + 1)}
            >
              <RefreshClockwise />
              Preview
            </Button>
          )}
          {engine.can("explainJSON") && (
            <Button
              size="xs"
              variant="outline"
              disabled={!sendable || enabled.length === 0}
              onClick={() =>
                setExplaining({
                  kind: "pipeline",
                  pipeline: text,
                  statement: `${collection}.aggregate([${enabled.map((stage) => stage.op).join(", ")}])`,
                })
              }
            >
              Explain
            </Button>
          )}
          {canRun && (
            <Button
              size="xs"
              pending={running}
              disabled={!sendable || enabled.length === 0 || !mayRun}
              onClick={pressRun}
            >
              <Play />
              Run
            </Button>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {stages.length === 0 ? (
          <Start saved={saved} onLoad={load} onAdd={add} onText={() => setTexting(true)} />
        ) : (
          <div className="space-y-3 p-3">
            {writes && (
              <FormNote tone={mayRun ? "default" : "warning"}>
                {kind.writes
                  ? `This pipeline writes (${kind.writes}).`
                  : `${kind.unknown.join(", ")}: not known to only read, so a run counts as one that writes.`}{" "}
                {mayRun
                  ? "A preview never writes; a run is confirmed first."
                  : readOnly
                    ? "The connection is protected, so it can be previewed but not run."
                    : "Running it needs the permission to remove data, which your role does not have. It can still be previewed."}
              </FormNote>
            )}
            <ol aria-label="Pipeline stages" className="space-y-3">
              {stages.map((stage, index) => (
                <StageCard
                  key={stage.id}
                  target={target}
                  stage={stage}
                  number={index + 1}
                  count={stages.length}
                  problem={problems[index]}
                  prefix={prefixAt(index)}
                  index={previewIndex(stages, stage.id)}
                  auto={auto}
                  nonce={nonce}
                  operators={foreign}
                  onChange={(next) =>
                    change(stages.map((held) => (held.id === stage.id ? next : held)))
                  }
                  onMove={(by) => change(moved(stages, stage.id, by))}
                  onDuplicate={() =>
                    change([
                      ...stages.slice(0, index + 1),
                      { ...stage, id: uid() },
                      ...stages.slice(index + 1),
                    ])
                  }
                  onRemove={() => change(stages.filter((held) => held.id !== stage.id))}
                  onRun={
                    canRun && sendable && enabled.length > 0 && mayRun && !running
                      ? pressRun
                      : undefined
                  }
                />
              ))}
            </ol>
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" onClick={() => add()}>
                <Plus />
                Add a stage
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  change([])
                  setLoadedName("")
                  setRan(null)
                }}
              >
                Clear the pipeline
              </Button>
            </div>

            {ran && (
              <section
                ref={resultRef}
                aria-label="Result of the run"
                className="animate-rise space-y-2 pt-2"
              >
                <Result
                  id={id}
                  scope={scope}
                  ran={ran}
                  stale={stale}
                  limit={limit}
                  onLimit={setLimit}
                  view={resultView}
                  onView={setResultView}
                  target={target}
                />
              </section>
            )}
          </div>
        )}
      </div>

      <SaveDialog
        open={saving}
        initial={loadedName}
        taken={saved.map((entry) => entry.name)}
        onOpenChange={setSaving}
        onSave={store}
      />
      <TextDialog
        open={texting}
        initial={listText(stages)}
        onOpenChange={setTexting}
        onApply={(parsed) => {
          change(
            parsed.map((stage) => ({ id: uid(), op: stage.op, body: stage.body, enabled: true })),
          )
          setRan(null)
        }}
      />
      <ExplainDialog
        mongo={mongo}
        target={target}
        subject={explaining}
        onOpenChange={(open) => !open && setExplaining(null)}
      />
    </div>
  )
}

/** Before the first stage: the saved pipelines to take up, and the usual places to start. */
function Start({
  saved,
  onLoad,
  onAdd,
  onText,
}: {
  saved: SavedPipeline[]
  onLoad: (entry: SavedPipeline) => void
  onAdd: (op: string) => void
  onText: () => void
}) {
  return (
    <div className="animate-rise space-y-6 p-4">
      <EmptyState
        className="border-0 py-6"
        icon={Play}
        title="No stages yet"
        description="A pipeline passes the collection's documents through stages, one after another. Each stage shows a sample of what it leaves."
        action={
          <div className="flex flex-wrap justify-center gap-2">
            {["$match", "$group", "$sort", "$project", "$lookup"].map((op) => (
              <Button key={op} size="sm" variant="outline" onClick={() => onAdd(op)}>
                <Plus />
                <span className="font-mono">{op}</span>
              </Button>
            ))}
            <Button size="sm" variant="ghost" onClick={onText}>
              Paste a pipeline
            </Button>
          </div>
        }
      />
      {saved.length > 0 && (
        <section aria-labelledby="mongo-pipeline-saved" className="mx-auto max-w-2xl space-y-2">
          <h3 id="mongo-pipeline-saved" className="eyebrow">
            Saved for this collection
          </h3>
          <ChoiceList>
            {saved.map((entry, index) => (
              <ChoiceRow
                key={entry.name}
                index={index}
                title={entry.name}
                verb={`Open the pipeline ${entry.name}`}
                description={
                  <span className="font-mono">
                    {entry.stages.map((stage) => stage.op).join(" → ") || "no stages"}
                  </span>
                }
                trailing={
                  <span className="text-hint whitespace-nowrap text-muted-foreground">
                    saved {relativeTime(new Date(entry.savedAt).toISOString())}
                  </span>
                }
                onSelect={() => onLoad(entry)}
              />
            ))}
          </ChoiceList>
        </section>
      )}
    </div>
  )
}

const resultVerbs = (doc: MongoDoc): Verb[] => [
  {
    key: "copy",
    label: "Copy",
    icon: Copy,
    inline: true,
    run: () => void copyText(editorText(doc.canonical), "Document copied as Extended JSON"),
  },
]

function Result({
  id,
  scope,
  ran,
  stale,
  limit,
  onLimit,
  view,
  onView,
  target,
}: {
  id: number
  scope: string
  ran: Ran
  stale: boolean
  limit: Limit
  onLimit: (limit: Limit) => void
  view: ResultView
  onView: (view: ResultView) => void
  target: Parameters<typeof ListView>[0]["target"]
}) {
  const result = ran.result
  const listed = useListed(result?.documents ?? [])
  const [layout, setLayout] = useGridLayout(`databases.${id}.mongo.aggregate.grid.${scope}`)
  if (ran.error !== undefined) {
    return (
      <Notice tone="danger" title="The pipeline did not run">
        <span className="font-mono text-xs break-words">{ran.error}</span>
      </Notice>
    )
  }
  if (!result) return null
  return (
    <>
      <div className="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline">
        <h3 className="text-body font-medium">Result</h3>
        <div role="group" aria-label="Result views" className="flex gap-1 self-stretch">
          {(["list", "json", "table"] as const).map((entry) => (
            <button
              key={entry}
              type="button"
              aria-pressed={view === entry}
              onClick={() => onView(entry)}
              className={tabClasses(view === entry, "h-9")}
            >
              {entry === "list" ? "List" : entry === "json" ? "JSON" : "Table"}
            </button>
          ))}
        </div>
        <span className="numeric min-w-0 flex-1 truncate text-hint text-muted-foreground">
          {result.returned.toLocaleString("en-US")}{" "}
          {result.returned === 1 ? "document" : "documents"}
          {result.hasMore ? ", and more past the limit" : ""}
          {result.truncated ? " · cut at 8 MiB" : ""}
          {` · ${result.durationMs.toLocaleString("en-US")} ms`}
          {stale ? " · of the pipeline as it was run, not as it reads now" : ""}
        </span>
        <Segments
          label="Documents a run returns at most"
          value={limit}
          options={LIMITS.map((value) => ({ value, label: value }))}
          onChange={onLimit}
        />
      </div>
      {result.returned === 0 ? (
        <p className="py-6 text-center text-body text-muted-foreground">
          {result.writes
            ? "The pipeline wrote its result and returned no documents."
            : "The pipeline returned no documents."}
        </p>
      ) : view === "table" ? (
        // A frame because the grid is a working region with its own scroll.
        <div className="flex h-96 min-h-0 flex-col overflow-hidden rounded-lg border border-hairline">
          <TableView
            label="Result of the pipeline"
            listed={listed}
            layout={layout}
            onLayoutChange={setLayout}
            offset={0}
            loading={false}
          />
        </div>
      ) : view === "json" ? (
        <JsonView listed={listed} verbsFor={resultVerbs} />
      ) : (
        <ListView
          target={target}
          listed={listed}
          expanded={false}
          editable={false}
          edits={{}}
          onEdits={() => {}}
          verbsFor={resultVerbs}
          onUpdated={() => {}}
        />
      )}
    </>
  )
}

function SaveDialog({
  open,
  initial,
  taken,
  onOpenChange,
  onSave,
}: {
  open: boolean
  initial: string
  taken: string[]
  onOpenChange: (open: boolean) => void
  onSave: (name: string) => void
}) {
  const [name, setName] = useState(initial)
  const [held, setHeld] = useState(open)
  if (held !== open) {
    setHeld(open)
    if (open) setName(initial)
  }
  const trimmed = name.trim()
  const submit = () => {
    if (!trimmed) return
    onSave(trimmed)
    onOpenChange(false)
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title="Save pipeline"
      description="Give the pipeline a name to open it by later"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!trimmed} onClick={submit}>
            {taken.includes(trimmed) ? "Save over it" : "Save"}
          </Button>
        </>
      }
    >
      <Field
        label="Name"
        htmlFor="mongo-pipeline-name"
        hint={
          taken.includes(trimmed)
            ? "A pipeline of that name is saved for this collection: this one replaces it."
            : "Kept in this browser, for this collection."
        }
      >
        <Input
          id="mongo-pipeline-name"
          value={name}
          autoComplete="off"
          placeholder="Revenue by status"
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => event.key === "Enter" && submit()}
        />
      </Field>
    </Modal>
  )
}

function TextDialog({
  open,
  initial,
  onOpenChange,
  onApply,
}: {
  open: boolean
  initial: string
  onOpenChange: (open: boolean) => void
  onApply: (stages: { op: string; body: string }[]) => void
}) {
  const [text, setText] = useState(initial)
  const [held, setHeld] = useState(open)
  if (held !== open) {
    setHeld(open)
    if (open) setText(initial)
  }
  const parsed = parsePipeline(text)
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title="The pipeline as text"
      description="Edit the whole pipeline as one list, or paste one"
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            {parsed.ok
              ? `${parsed.stages.length} ${parsed.stages.length === 1 ? "stage" : "stages"}. Stages that are switched off are not part of the text.`
              : ""}
          </p>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={!parsed.ok}
            onClick={() => {
              if (!parsed.ok) return
              onApply(parsed.stages)
              onOpenChange(false)
            }}
          >
            Use these stages
          </Button>
        </>
      }
    >
      <Field
        label="Pipeline"
        htmlFor="mongo-pipeline-text"
        hint="A list of stages. A whole db.collection.aggregate([ … ]) call can be pasted too."
        error={parsed.ok ? undefined : parsed.message}
      >
        <CodeField
          id="mongo-pipeline-text"
          value={text}
          invalid={!parsed.ok}
          className="max-h-[50svh] min-h-64"
          onChange={setText}
        />
      </Field>
    </Modal>
  )
}
