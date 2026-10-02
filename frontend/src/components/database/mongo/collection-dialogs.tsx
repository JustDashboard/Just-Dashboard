"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import { bytes, duration } from "@/lib/format"
import { notify } from "@/lib/toast"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import {
  Disclosure,
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { grouped } from "@/components/database/data/view"
import { EngineMark } from "@/components/database/kit"
import {
  createCollection,
  dropCollection,
  modifyCollection,
  renameCollection,
  type MongoModifyCollection,
  type MongoTarget,
} from "@/components/database/mongo/api"
import { CodeField } from "@/components/database/mongo/code-field"
import {
  COLLECTION_KINDS,
  CollectionMark,
  collectionKind,
  type CollectionKind,
} from "@/components/database/mongo/kinds"
import { scan, shapeProblem, textOf } from "@/components/database/mongo/shell"
import type {
  MongoCollection,
  MongoCreateCollection,
  MongoValidationAction,
  MongoValidationLevel,
} from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"

type NewKind = "collection" | "capped" | "timeseries" | "view"

/** A view to be made: what it reads, and the pipeline it reads it through. */
export type ViewPreset = { viewOn: string; pipeline: string }

const KIND_HINT: Record<NewKind, string> = {
  collection: "Documents of any shape, kept until they are deleted.",
  capped: "A fixed size: the oldest documents make room for new ones.",
  timeseries: "Measurements over time, stored in buckets by a time field.",
  view: "A saved pipeline over another collection. It holds nothing itself.",
}

/** A collection's name as the server takes one. */
function nameProblem(name: string): string | null {
  if (!name) return null
  if (name.startsWith("system.")) return "Names that begin with system. are the server's own."
  if (name.includes("$")) return "A name cannot hold a $."
  if (name.includes("\u0000")) return "A name cannot hold a NUL character."
  return null
}

type Draft = {
  kind: NewKind
  name: string
  sizeMb: string
  max: string
  timeField: string
  metaField: string
  granularity: "seconds" | "minutes" | "hours"
  expire: string
  viewOn: string
  pipeline: string
  clustered: boolean
  validator: string
  level: MongoValidationLevel
  action: MongoValidationAction
  collation: string
}

const EMPTY: Draft = {
  kind: "collection",
  name: "",
  sizeMb: "64",
  max: "",
  timeField: "",
  metaField: "",
  granularity: "seconds",
  expire: "",
  viewOn: "",
  pipeline: "[]",
  clustered: false,
  validator: "",
  level: "strict",
  action: "error",
  collation: "",
}

const WHOLE = /^\d+$/

/**
 * New collection: what kind first, then its name and what that kind needs.
 *
 * The kinds are cards because they are four different things to end up with,
 * not four values of one setting: a capped collection deletes, a view stores
 * nothing. An engine that only has ordinary collections is not asked.
 */
export function NewCollectionDialog({
  mongo,
  collections,
  open,
  view,
  onOpenChange,
  onCreated,
}: {
  mongo: Mongo
  collections: MongoCollection[]
  open: boolean
  /** Opens on a view of this collection through this pipeline: "save the pipeline as a view". */
  view?: ViewPreset
  onOpenChange: (open: boolean) => void
  onCreated: (name: string) => void
}) {
  const { id, database, engine } = mongo
  const [draft, setDraft] = useState<Draft>(() =>
    view ? { ...EMPTY, kind: "view", viewOn: view.viewOn, pipeline: view.pipeline } : EMPTY,
  )
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const set = (patch: Partial<Draft>) => {
    setDraft((held) => ({ ...held, ...patch }))
    setRefused("")
  }
  // Closed — by the reader, or by the address moving to what was made — the
  // form starts again from nothing the next time it opens.
  const [wasOpen, setWasOpen] = useState(open)
  if (wasOpen !== open) {
    setWasOpen(open)
    if (!open) {
      setDraft(EMPTY)
      setRefused("")
      setBusy(false)
    }
  }

  const kinds: NewKind[] = [
    "collection",
    ...(engine.can("collectionOptions") ? (["capped", "timeseries"] as const) : []),
    ...(engine.can("views") ? (["view"] as const) : []),
  ]
  const sources = collections.filter((entry) => !entry.system)

  const name = draft.name.trim()
  const named = nameProblem(name)
  const taken = collections.some((entry) => entry.name === name)
  const sizeBad = draft.kind === "capped" && !(Number(draft.sizeMb) > 0)
  const maxBad = draft.kind === "capped" && draft.max.trim() !== "" && !WHOLE.test(draft.max.trim())
  const expireBad = draft.expire.trim() !== "" && !WHOLE.test(draft.expire.trim())
  const pipelineBad = draft.kind === "view" ? shapeProblem(draft.pipeline, "list") : null
  const validatorBad = shapeProblem(draft.validator, "document")
  const collationBad = shapeProblem(draft.collation, "document")
  const incomplete =
    !name ||
    Boolean(named) ||
    taken ||
    sizeBad ||
    maxBad ||
    expireBad ||
    Boolean(pipelineBad) ||
    Boolean(validatorBad) ||
    Boolean(collationBad) ||
    (draft.kind === "timeseries" && !draft.timeField.trim()) ||
    (draft.kind === "view" && !draft.viewOn)

  const close = (next: boolean) => {
    if (!busy) onOpenChange(next)
  }

  const create = async () => {
    const request: MongoCreateCollection = { database, collection: name }
    if (draft.kind === "capped") {
      request.capped = true
      request.size = Math.round(Number(draft.sizeMb) * 1024 * 1024)
      if (draft.max.trim()) request.max = Number(draft.max)
    } else if (draft.kind === "timeseries") {
      request.timeseries = {
        timeField: draft.timeField.trim(),
        metaField: draft.metaField.trim() || undefined,
        granularity: draft.granularity,
      }
      if (draft.expire.trim()) request.expireAfterSeconds = Number(draft.expire)
    } else if (draft.kind === "view") {
      request.viewOn = draft.viewOn
      request.pipeline = draft.pipeline.trim() || "[]"
    }
    if (draft.kind !== "view") {
      if (draft.kind === "collection" && draft.clustered) request.clusteredIndex = true
      if (draft.validator.trim()) {
        request.validator = draft.validator
        request.validationLevel = draft.level
        request.validationAction = draft.action
      }
    }
    if (draft.collation.trim()) request.collation = draft.collation
    setBusy(true)
    try {
      await createCollection(id, request)
      notify.success(`Created ${name}`)
      // The caller goes to the new collection, which closes this dialog: one
      // change of address rather than a close and then a move. Until the
      // address has moved the dialog stays as it is, its command still busy,
      // rather than showing an emptied form for a moment.
      onCreated(name)
    } catch (err) {
      setRefused(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      size="lg"
      title={`New ${engine.nouns.object}`}
      description={`Create a ${engine.nouns.object} in ${database}`}
      footer={
        <>
          {refused && (
            <p role="alert" className="mr-auto min-w-0 text-hint text-destructive">
              {refused}
            </p>
          )}
          <Button variant="outline" disabled={busy} onClick={() => close(false)}>
            Cancel
          </Button>
          <Button pending={busy} disabled={incomplete} onClick={() => void create()}>
            Create {draft.kind === "view" ? "view" : engine.nouns.object}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
          </FormFacts>
        </div>

        {kinds.length > 1 && (
          <ChoiceGrid columns={2} role="group" aria-label="Kind of collection" className="gap-2">
            {kinds.map((kind) => (
              <ChoiceCard
                key={kind}
                selected={draft.kind === kind}
                onClick={() => set({ kind })}
                className="h-full min-h-0 gap-1 p-2.5"
              >
                <span className="flex items-center gap-1.5">
                  <CollectionMark kind={kind} />
                  <ChoiceCardTitle>{COLLECTION_KINDS[kind].label}</ChoiceCardTitle>
                </span>
                <ChoiceCardHint>{KIND_HINT[kind]}</ChoiceCardHint>
              </ChoiceCard>
            ))}
          </ChoiceGrid>
        )}

        <Field
          label="Name"
          htmlFor="mongo-new-name"
          error={
            named ?? (taken ? `${database} already has a collection called ${name}.` : undefined)
          }
        >
          <Input
            id="mongo-new-name"
            value={draft.name}
            spellCheck={false}
            autoComplete="off"
            placeholder={draft.kind === "view" ? "active_users" : "events"}
            className="font-mono"
            onChange={(event) => set({ name: event.target.value })}
          />
        </Field>

        {draft.kind === "capped" && (
          <FieldRow>
            <Field
              label="Size"
              htmlFor="mongo-new-size"
              hint="Megabytes of data it holds before the oldest go."
              error={sizeBad ? "A size above zero." : undefined}
            >
              <Input
                id="mongo-new-size"
                inputMode="decimal"
                value={draft.sizeMb}
                className="numeric"
                onChange={(event) => set({ sizeMb: event.target.value })}
              />
            </Field>
            <Field
              label="At most"
              htmlFor="mongo-new-max"
              hint="Documents. Blank leaves only the size as the limit."
              error={maxBad ? "A whole number." : undefined}
            >
              <Input
                id="mongo-new-max"
                inputMode="numeric"
                value={draft.max}
                className="numeric"
                onChange={(event) => set({ max: event.target.value })}
              />
            </Field>
          </FieldRow>
        )}

        {draft.kind === "timeseries" && (
          <>
            <FieldRow>
              <Field
                label="Time field"
                htmlFor="mongo-new-time"
                hint="The field of every document that holds its moment."
              >
                <Input
                  id="mongo-new-time"
                  value={draft.timeField}
                  spellCheck={false}
                  placeholder="timestamp"
                  className="font-mono"
                  onChange={(event) => set({ timeField: event.target.value })}
                />
              </Field>
              <Field
                label="Meta field"
                htmlFor="mongo-new-meta"
                hint="What a series is of: a sensor, a host. Optional."
              >
                <Input
                  id="mongo-new-meta"
                  value={draft.metaField}
                  spellCheck={false}
                  placeholder="sensor"
                  className="font-mono"
                  onChange={(event) => set({ metaField: event.target.value })}
                />
              </Field>
            </FieldRow>
            <FieldRow>
              <Field label="Measurements arrive every few" htmlFor="mongo-new-granularity">
                <Segments
                  id="mongo-new-granularity"
                  label="Granularity"
                  value={draft.granularity}
                  options={[
                    { value: "seconds", label: "Seconds" },
                    { value: "minutes", label: "Minutes" },
                    { value: "hours", label: "Hours" },
                  ]}
                  onChange={(granularity) => set({ granularity })}
                />
              </Field>
              <Field
                label="Delete after"
                htmlFor="mongo-new-expire"
                hint="Seconds. Blank keeps every measurement."
                error={expireBad ? "A whole number of seconds." : undefined}
              >
                <Input
                  id="mongo-new-expire"
                  inputMode="numeric"
                  value={draft.expire}
                  className="numeric"
                  onChange={(event) => set({ expire: event.target.value })}
                />
              </Field>
            </FieldRow>
          </>
        )}

        {draft.kind === "view" && (
          <>
            <Field label="View on" htmlFor="mongo-new-source">
              <Select value={draft.viewOn} onValueChange={(viewOn) => set({ viewOn })}>
                <SelectTrigger id="mongo-new-source" className="w-full font-mono">
                  <SelectValue placeholder="Choose a collection" />
                </SelectTrigger>
                <SelectContent>
                  {sources.map((entry) => (
                    <SelectItem key={entry.name} value={entry.name} className="font-mono">
                      {entry.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Pipeline"
              htmlFor="mongo-new-pipeline"
              hint="The stages every read of the view runs. It may not write ($out, $merge)."
              error={pipelineBad ?? undefined}
            >
              <CodeField
                id="mongo-new-pipeline"
                value={draft.pipeline}
                invalid={Boolean(pipelineBad)}
                placeholder='[ { "$match": { "active": true } } ]'
                onChange={(pipeline) => set({ pipeline })}
              />
            </Field>
          </>
        )}

        {draft.kind !== "view" && engine.can("collectionOptions") && (
          <Disclosure quiet summary="More options">
            <div className="space-y-4 pt-1">
              {draft.kind === "collection" && (
                <OptionList>
                  <OptionRow
                    title="Store the documents in _id order (a clustered collection)"
                    checked={draft.clustered}
                    onCheckedChange={(clustered) => set({ clustered })}
                  />
                </OptionList>
              )}
              {engine.can("validation") && (
                <Field
                  label="Validation rule"
                  htmlFor="mongo-new-validator"
                  hint="What every written document must match. Blank is no rule."
                  error={validatorBad ?? undefined}
                >
                  <CodeField
                    id="mongo-new-validator"
                    value={draft.validator}
                    invalid={Boolean(validatorBad)}
                    placeholder='{ "$jsonSchema": { "required": ["sku"] } }'
                    onChange={(validator) => set({ validator })}
                  />
                </Field>
              )}
              <Field
                label="Collation"
                htmlFor="mongo-new-collation"
                hint="How its strings compare. Blank compares them byte by byte."
                error={collationBad ?? undefined}
              >
                <CodeField
                  id="mongo-new-collation"
                  dense
                  value={draft.collation}
                  invalid={Boolean(collationBad)}
                  placeholder='{ "locale": "en", "strength": 2 }'
                  onChange={(collation) => set({ collation })}
                />
              </Field>
            </div>
          </Disclosure>
        )}
      </div>
    </Modal>
  )
}

/** Rename a collection. Replacing the one already called that is a removal, and is asked for by name. */
export function RenameCollectionDialog({
  mongo,
  collection,
  collections,
  confirm,
  onOpenChange,
  onRenamed,
}: {
  mongo: Mongo
  /** The collection being renamed; `null` closes the dialog. */
  collection: MongoCollection | null
  collections: MongoCollection[]
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onRenamed: (from: string, to: string) => void
}) {
  const { id, database, engine, canDestroy } = mongo
  const [to, setTo] = useState("")
  const [replace, setReplace] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [held, setHeld] = useState(collection)
  // The dialog opens on the collection's own name, ready to be changed.
  if (collection !== held) {
    setHeld(collection)
    setTo(collection?.name ?? "")
    setReplace(false)
    setRefused("")
  }
  if (!collection) return null

  const name = to.trim()
  const named = nameProblem(name)
  const existing = collections.find((entry) => entry.name === name && name !== collection.name)
  const target: MongoTarget = { id, database, collection: collection.name }

  const rename = async (dropTarget: boolean) => {
    setBusy(true)
    try {
      await renameCollection(target, name, dropTarget)
      notify.success(`Renamed ${collection.name} to ${name}`)
      onOpenChange(false)
      onRenamed(collection.name, name)
    } catch (err) {
      setRefused(errorMessage(err))
      throw err
    } finally {
      setBusy(false)
    }
  }

  const submit = () => {
    if (!existing) {
      void rename(false).catch(() => {})
      return
    }
    // One dialog at a time: the confirmation takes this one's place.
    onOpenChange(false)
    confirm({
      title: `Replace ${existing.name}`,
      description: (
        <>
          <span className="font-mono">{existing.name}</span> is dropped, with every document and
          index in it, and <span className="font-mono">{collection.name}</span> takes its name.
        </>
      ),
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{existing.name}</span>,
        facts: (
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
            {existing.statsKnown && (
              <FormFact label="Holds">
                {grouped(existing.count)} documents · {bytes(existing.size)}
              </FormFact>
            )}
          </FormFacts>
        ),
      },
      confirmLabel: `Drop ${existing.name} and rename`,
      action: async () => {
        await rename(true)
        return "reported"
      },
    })
  }

  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title={`Rename ${collection.name}`}
      description={`Give the collection ${collection.name} another name`}
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            pending={busy}
            disabled={!name || name === collection.name || Boolean(named) || (existing && !replace)}
            onClick={submit}
          >
            Rename
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="flex min-w-0 items-center gap-1.5 font-mono text-body font-medium">
              <CollectionMark kind={collectionKind(collection)} />
              <span className="truncate">{collection.name}</span>
            </p>
            <FormFacts>
              <FormFact label="Database" mono>
                {database}
              </FormFact>
            </FormFacts>
          </div>
        </div>
        <Field
          label="New name"
          htmlFor="mongo-rename-to"
          error={
            refused ||
            named ||
            (existing && !canDestroy
              ? `${database} already has a collection called ${name}.`
              : undefined)
          }
        >
          <Input
            id="mongo-rename-to"
            value={to}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            onChange={(event) => {
              setTo(event.target.value)
              setRefused("")
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return
              if (!name || name === collection.name || named || (existing && !replace)) return
              submit()
            }}
          />
        </Field>
        {existing && canDestroy && (
          <OptionList>
            <OptionRow
              tone={replace ? "danger" : "default"}
              title={`Replace the collection already called ${existing.name}`}
              hint={
                existing.statsKnown
                  ? `It holds ${grouped(existing.count)} documents, and is dropped first.`
                  : "It is dropped first."
              }
              checked={replace}
              onCheckedChange={setReplace}
            />
          </OptionList>
        )}
        <FormNote>
          Applications that read <span className="font-mono">{collection.name}</span> by name stop
          finding it the moment it is renamed.
        </FormNote>
      </div>
    </Modal>
  )
}

/* ----------------------------------------------------------------- options */

const MIB = 1024 * 1024
const GRANULARITIES = ["seconds", "minutes", "hours"] as const

/** A number of megabytes as a field holds it: whole where it is whole. */
const megabytes = (size: number) => String(Math.round((size / MIB) * 100) / 100)

/** Whether a collection has options that can be changed after it is made. */
export function hasOptions(collection: MongoCollection): boolean {
  return (
    !collection.system &&
    (collection.type === "view" ||
      collection.type === "timeseries" ||
      collection.capped ||
      collection.clustered)
  )
}

/**
 * Whether the reader may change them. A view's definition and a time
 * series' granularity are a write; a capped collection's size and an expiry
 * remove documents, and take the permission to remove data.
 */
export function mayModify(mongo: Mongo, collection: MongoCollection): boolean {
  if (!hasOptions(collection) || !mongo.canWrite) return false
  if (collection.type === "view") return mongo.engine.can("views")
  if (!mongo.engine.can("collectionOptions")) return false
  // Without that permission a time series can still be made coarser, and an expiry turned off.
  return collection.capped ? mongo.canDestroy : true
}

type OptionsDraft = {
  sizeMb: string
  max: string
  granularity: (typeof GRANULARITIES)[number]
  expire: string
  viewOn: string
  pipeline: string
}

/** A view's stored pipeline laid out for editing, a stage to a line. */
function laidOut(pipeline: string | undefined): string {
  const text = pipeline?.trim() || "[]"
  const read = scan(text)
  if (!read.ok || read.value.kind !== "array" || read.value.items.length === 0) return text
  return `[\n${read.value.items.map((item) => `  ${textOf(text, item)}`).join(",\n")}\n]`
}

const optionsOf = (collection: MongoCollection): OptionsDraft => ({
  sizeMb: collection.cappedSize ? megabytes(collection.cappedSize) : "",
  max: collection.cappedMax ? String(collection.cappedMax) : "",
  granularity: collection.timeseries?.granularity ?? "seconds",
  expire: collection.expireAfterSeconds !== undefined ? String(collection.expireAfterSeconds) : "",
  viewOn: collection.viewOn ?? "",
  pipeline: laidOut(collection.pipeline),
})

/**
 * The options of a collection that exists: a capped collection's size, a time
 * series' granularity and expiry, a clustered collection's expiry, what a
 * view reads and through which stages.
 *
 * These are the few things the server lets a collection change about itself
 * without being made again. Two of them remove documents the moment they are
 * set — a smaller cap drops the oldest, an expiry deletes what is already
 * older — so those are confirmed with the collection named, and are offered
 * only to a role that may remove data. The rest are saved as they stand.
 */
export function CollectionOptionsDialog({
  mongo,
  collection,
  collections,
  confirm,
  onOpenChange,
  onChanged,
}: {
  mongo: Mongo
  /** The collection whose options are open; `null` closes the dialog. */
  collection: MongoCollection | null
  collections: MongoCollection[]
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { id, database, engine, canDestroy } = mongo
  const [draft, setDraft] = useState<OptionsDraft | null>(null)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [held, setHeld] = useState(collection)
  if (collection !== held) {
    setHeld(collection)
    setDraft(collection ? optionsOf(collection) : null)
    setRefused("")
  }
  if (!collection || !draft) return null

  const set = (patch: Partial<OptionsDraft>) => {
    setDraft({ ...draft, ...patch })
    setRefused("")
  }
  const saved = optionsOf(collection)
  const kind = collectionKind(collection)
  const view = collection.type === "view"
  const series = collection.type === "timeseries"
  const expires = series || collection.clustered
  const target: MongoTarget = { id, database, collection: collection.name }

  const sizeBad = collection.capped && !(Number(draft.sizeMb) > 0)
  const maxText = draft.max.trim()
  const maxBad =
    collection.capped &&
    (maxText === "" ? Boolean(collection.cappedMax) : !WHOLE.test(maxText) || Number(maxText) < 1)
  const expireText = draft.expire.trim()
  const expireBad = expires && expireText !== "" && !WHOLE.test(expireText)
  const pipelineBad = view ? shapeProblem(draft.pipeline, "list") : null

  // Only what was changed is sent: the server takes each field as a change of its own.
  const change: MongoModifyCollection = {}
  if (collection.capped) {
    if (draft.sizeMb.trim() !== saved.sizeMb)
      change.cappedSize = Math.round(Number(draft.sizeMb) * MIB)
    if (maxText !== saved.max && maxText !== "") change.cappedMax = Number(maxText)
  }
  if (series && draft.granularity !== saved.granularity) change.granularity = draft.granularity
  if (expires && expireText !== saved.expire) {
    // A blank field is "keep everything": a negative number turns expiry off.
    change.expireAfterSeconds = expireText === "" ? -1 : Number(expireText)
  }
  if (view && (draft.viewOn !== saved.viewOn || draft.pipeline.trim() !== saved.pipeline.trim())) {
    change.viewOn = draft.viewOn
    change.pipeline = draft.pipeline.trim() || "[]"
  }
  const removes =
    change.cappedSize !== undefined ||
    change.cappedMax !== undefined ||
    (change.expireAfterSeconds !== undefined && change.expireAfterSeconds >= 0)
  const changed = Object.keys(change).length > 0
  const incomplete =
    !changed || sizeBad || maxBad || expireBad || Boolean(pipelineBad) || (view && !draft.viewOn)

  const save = async () => {
    setBusy(true)
    try {
      await modifyCollection(target, change)
      notify.success(
        view
          ? `The view ${collection.name} was redefined`
          : `Saved the options of ${collection.name}`,
      )
      onOpenChange(false)
      onChanged()
    } catch (err) {
      setRefused(errorMessage(err))
      throw err
    } finally {
      setBusy(false)
    }
  }

  const submit = () => {
    if (!removes) {
      void save().catch(() => {})
      return
    }
    const resized = change.cappedSize !== undefined || change.cappedMax !== undefined
    const expiry = change.expireAfterSeconds
    // One dialog at a time: the confirmation takes this one's place.
    onOpenChange(false)
    confirm({
      title: resized ? `Resize ${collection.name}` : `Change when ${collection.name} expires`,
      description: resized ? (
        <>
          Documents past the new limit are removed at once, oldest first, and cannot be brought
          back.
        </>
      ) : (
        <>
          Everything older than {duration(expiry)} is deleted — what is already that old at once,
          the rest as it ages. This cannot be undone.
        </>
      ),
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: (
          <span className="flex min-w-0 items-center gap-1.5 font-mono">
            <CollectionMark kind={kind} />
            <span className="truncate">{collection.name}</span>
          </span>
        ),
        facts: (
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
            {collection.statsKnown && (
              <FormFact label="Holds">
                {grouped(collection.count)} documents · {bytes(collection.size)}
              </FormFact>
            )}
            {change.cappedSize !== undefined && (
              <FormFact label="New size">{bytes(change.cappedSize)}</FormFact>
            )}
            {change.cappedMax !== undefined && (
              <FormFact label="At most">{grouped(change.cappedMax)} documents</FormFact>
            )}
            {expiry !== undefined && expiry >= 0 && (
              <FormFact label="Delete after">
                {grouped(expiry)} s ({duration(expiry)})
              </FormFact>
            )}
          </FormFacts>
        ),
      },
      confirmLabel: resized ? "Resize collection" : "Set expiry",
      action: async () => {
        await save()
        return "reported"
      },
    })
  }

  const expireField = expires && (
    <Field
      label="Delete after"
      htmlFor="mongo-options-expire"
      hint={
        expireText && !expireBad
          ? `Seconds: ${duration(Number(expireText))}. What is already older is deleted when this is saved.`
          : "Seconds. Blank keeps everything."
      }
      error={expireBad ? "A whole number of seconds." : undefined}
    >
      <Input
        id="mongo-options-expire"
        inputMode="numeric"
        value={draft.expire}
        // A new limit deletes; taking the limit away does not.
        readOnly={!canDestroy && expireText === ""}
        className="numeric"
        onChange={(event) => {
          const next = event.target.value
          if (canDestroy || next.trim() === "") set({ expire: next })
        }}
      />
    </Field>
  )

  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size={view ? "lg" : "md"}
      title={`Options of ${collection.name}`}
      description={`Change what the server lets ${collection.name} change without being made again`}
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            {refused ? (
              <span role="alert" className="text-destructive">
                {refused}
              </span>
            ) : removes ? (
              "Removes documents: asked for once more before it runs."
            ) : view ? (
              "The view is redefined in place. Nothing it reads is touched."
            ) : (
              "Only what you changed is sent."
            )}
          </p>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button pending={busy} disabled={incomplete} onClick={submit}>
            {removes ? "Save options…" : "Save options"}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="flex min-w-0 items-center gap-1.5 font-mono text-body font-medium">
              <CollectionMark kind={kind} />
              <span className="truncate">{collection.name}</span>
            </p>
            <FormFacts>
              <FormFact label="Database" mono>
                {database}
              </FormFact>
              <FormFact label="Kind">{COLLECTION_KINDS[kind].label}</FormFact>
              {collection.statsKnown && (
                <FormFact label="Holds">
                  {grouped(collection.count)} documents · {bytes(collection.size)}
                </FormFact>
              )}
              {series && collection.timeseries && (
                <FormFact label="Time field" mono>
                  {collection.timeseries.timeField}
                </FormFact>
              )}
            </FormFacts>
          </div>
        </div>

        {collection.capped && (
          <FieldRow>
            <Field
              label="Size"
              htmlFor="mongo-options-size"
              hint="Megabytes of data it holds before the oldest go."
              error={sizeBad ? "A size above zero." : undefined}
            >
              <Input
                id="mongo-options-size"
                inputMode="decimal"
                value={draft.sizeMb}
                className="numeric"
                onChange={(event) => set({ sizeMb: event.target.value })}
              />
            </Field>
            <Field
              label="At most"
              htmlFor="mongo-options-max"
              hint={
                collection.cappedMax
                  ? "Documents. The limit can be moved, not taken away."
                  : "Documents. Blank leaves only the size as the limit."
              }
              error={maxBad ? "A whole number, 1 or more." : undefined}
            >
              <Input
                id="mongo-options-max"
                inputMode="numeric"
                value={draft.max}
                className="numeric"
                onChange={(event) => set({ max: event.target.value })}
              />
            </Field>
          </FieldRow>
        )}

        {series && (
          <FieldRow>
            <Field
              label="Measurements arrive every few"
              htmlFor="mongo-options-granularity"
              hint="It can only be made coarser, a step at a time: seconds to minutes, minutes to hours."
            >
              <Segments
                id="mongo-options-granularity"
                label="Granularity"
                value={draft.granularity}
                options={GRANULARITIES.filter(
                  // The stored one, and the one step up from it the server allows.
                  (_, at) =>
                    at >= GRANULARITIES.indexOf(saved.granularity) &&
                    at <= GRANULARITIES.indexOf(saved.granularity) + 1,
                ).map((value) => ({
                  value,
                  label: value.charAt(0).toUpperCase() + value.slice(1),
                }))}
                onChange={(granularity) => set({ granularity })}
              />
            </Field>
            {expireField}
          </FieldRow>
        )}

        {!series && expireField}

        {view && (
          <>
            <Field label="View on" htmlFor="mongo-options-source">
              <Select value={draft.viewOn} onValueChange={(viewOn) => set({ viewOn })}>
                <SelectTrigger id="mongo-options-source" className="w-full font-mono">
                  <SelectValue placeholder="Choose a collection" />
                </SelectTrigger>
                <SelectContent>
                  {collections
                    .filter((entry) => !entry.system && entry.name !== collection.name)
                    .map((entry) => (
                      <SelectItem key={entry.name} value={entry.name} className="font-mono">
                        {entry.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Pipeline"
              htmlFor="mongo-options-pipeline"
              hint="The stages every read of the view runs. It may not write ($out, $merge)."
              error={pipelineBad ?? undefined}
            >
              <CodeField
                id="mongo-options-pipeline"
                value={draft.pipeline}
                invalid={Boolean(pipelineBad)}
                className="min-h-32"
                onChange={(pipeline) => set({ pipeline })}
              />
            </Field>
          </>
        )}

        {!canDestroy && expires && (
          <FormNote>
            Setting an expiry deletes documents, which your role may not do. It can be turned off
            from here by clearing the field.
          </FormNote>
        )}
      </div>
    </Modal>
  )
}

/** The confirmation that drops a collection or a view, with what it holds named. */
export function dropRequest(
  mongo: Mongo,
  collection: MongoCollection,
  onDropped: () => void,
): ConfirmRequest {
  const { id, database, engine } = mongo
  const view = collection.type === "view"
  const kind: CollectionKind = collectionKind(collection)
  return {
    title: view ? `Drop the view ${collection.name}` : `Drop ${collection.name}`,
    description: view ? (
      <>
        The view is removed. <span className="font-mono">{collection.viewOn}</span>, which it reads,
        is not touched.
      </>
    ) : (
      "The collection is removed with every document and every index in it. This cannot be undone."
    ),
    subject: {
      mark: <EngineMark engine={engine} size="sm" />,
      name: (
        <span className="flex min-w-0 items-center gap-1.5 font-mono">
          <CollectionMark kind={kind} />
          <span className="truncate">{collection.name}</span>
        </span>
      ),
      facts: (
        <FormFacts>
          <FormFact label="Database" mono>
            {database}
          </FormFact>
          {collection.statsKnown && (
            <FormFact label="Holds">
              {grouped(collection.count)} documents · {bytes(collection.size)}
            </FormFact>
          )}
          {collection.statsKnown && collection.indexCount > 0 && (
            <FormFact label="Indexes">{grouped(collection.indexCount)}</FormFact>
          )}
        </FormFacts>
      ),
    },
    confirmLabel: view ? "Drop view" : "Drop collection",
    action: async () => {
      await dropCollection({ id, database, collection: collection.name })
    },
    onDone: onDropped,
  }
}
