"use client"

import { useState } from "react"
import { Cross, Plus } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useMemoryState } from "@/lib/view-state"
import {
  Disclosure,
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  OptionList,
  OptionRow,
  Statement,
} from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EngineMark } from "@/components/database/kit"
import { createIndex } from "@/components/database/mongo/api"
import { shellCollection } from "@/components/database/mongo/aggregations/pipeline"
import { CodeField } from "@/components/database/mongo/code-field"
import { collectionKey } from "@/components/database/mongo/query"
import { filterPath } from "@/components/database/mongo/schema/fields"
import { shapeProblem } from "@/components/database/mongo/shell"
import type {
  MongoCreateIndex,
  MongoIndex,
  MongoIndexKeyType,
  MongoSchema,
} from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"

const KEY_TYPES: { value: MongoIndexKeyType; label: string; shell: string }[] = [
  { value: "asc", label: "Ascending", shell: "1" },
  { value: "desc", label: "Descending", shell: "-1" },
  { value: "text", label: "Text", shell: '"text"' },
  { value: "hashed", label: "Hashed", shell: '"hashed"' },
  { value: "2dsphere", label: "2dsphere", shell: '"2dsphere"' },
  { value: "2d", label: "2d", shell: '"2d"' },
]
const SHELL = new Map(KEY_TYPES.map((entry) => [entry.value, entry.shell]))

type KeyDraft = { id: number; field: string; type: MongoIndexKeyType }

type Draft = {
  keys: KeyDraft[]
  name: string
  unique: boolean
  sparse: boolean
  hidden: boolean
  ttl: boolean
  ttlSeconds: string
  partial: string
  collation: string
  weights: string
  language: string
  wildcard: string
}

const EMPTY: Draft = {
  keys: [{ id: 0, field: "", type: "asc" }],
  name: "",
  unique: false,
  sparse: false,
  hidden: false,
  ttl: false,
  ttlSeconds: "3600",
  partial: "",
  collation: "",
  weights: "",
  language: "",
  wildcard: "",
}

/** The name the server gives an index when none is typed: field_direction, joined. */
function defaultName(keys: KeyDraft[]): string {
  return keys
    .filter((key) => key.field.trim())
    .map((key) => `${key.field.trim()}_${SHELL.get(key.type)!.replace(/"/g, "")}`)
    .join("_")
}

/**
 * Create index: the fields it is on and which way, then what else it is.
 *
 * The command is shown as the shell would write it, next to the button that
 * runs it. An index that expires documents is the one option here that
 * removes data — the moment it exists the server deletes everything older
 * than the limit — so it is said in those words and offered only to a role
 * that may remove data.
 */
export function CreateIndexDialog({
  mongo,
  open,
  existing,
  onOpenChange,
  onCreated,
}: {
  mongo: Mongo
  open: boolean
  existing: MongoIndex[]
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const { id, database, collection, engine, canDestroy } = mongo
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [serial, setSerial] = useState(1)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  // The fields the last schema analysis found, offered as the field box is typed in.
  const [schemas] = useMemoryState<Record<string, MongoSchema>>(`databases.${id}.mongo.schema`, {})
  const known = [
    ...new Set(
      (schemas[collectionKey(database, collection)]?.fields ?? [])
        .filter((field) => field.name !== "[]")
        .map((field) => filterPath(field.path)),
    ),
  ]

  const set = (patch: Partial<Draft>) => {
    setDraft((held) => ({ ...held, ...patch }))
    setRefused("")
  }
  const setKey = (keyId: number, patch: Partial<KeyDraft>) =>
    set({ keys: draft.keys.map((key) => (key.id === keyId ? { ...key, ...patch } : key)) })

  const keys = draft.keys.filter((key) => key.field.trim())
  const duplicate = keys.find(
    (key, index) => keys.findIndex((other) => other.field.trim() === key.field.trim()) !== index,
  )
  const name = draft.name.trim() || defaultName(draft.keys)
  const taken = existing.some((index) => index.name === name)
  const text = keys.some((key) => key.type === "text")
  const wildcard = keys.some((key) => key.field.includes("$**"))
  const ttlBad = draft.ttl && !/^\d+$/.test(draft.ttlSeconds.trim())
  const ttlKeys = draft.ttl && keys.length !== 1
  const partialBad = shapeProblem(draft.partial, "document")
  const collationBad = shapeProblem(draft.collation, "document")
  const weightsBad = shapeProblem(draft.weights, "document")
  const wildcardBad = shapeProblem(draft.wildcard, "document")
  const incomplete =
    keys.length === 0 ||
    Boolean(duplicate) ||
    taken ||
    ttlBad ||
    ttlKeys ||
    Boolean(partialBad) ||
    Boolean(collationBad) ||
    Boolean(weightsBad) ||
    Boolean(wildcardBad)

  const options = [
    draft.name.trim() && `name: ${JSON.stringify(draft.name.trim())}`,
    draft.unique && "unique: true",
    draft.sparse && "sparse: true",
    draft.hidden && "hidden: true",
    draft.ttl && !ttlBad && `expireAfterSeconds: ${draft.ttlSeconds.trim()}`,
    draft.partial.trim() && `partialFilterExpression: ${draft.partial.trim()}`,
    draft.collation.trim() && `collation: ${draft.collation.trim()}`,
    text && draft.weights.trim() && `weights: ${draft.weights.trim()}`,
    text && draft.language.trim() && `default_language: ${JSON.stringify(draft.language.trim())}`,
    wildcard && draft.wildcard.trim() && `wildcardProjection: ${draft.wildcard.trim()}`,
  ].filter(Boolean)
  const statement =
    keys.length === 0
      ? ""
      : `${shellCollection(collection)}.createIndex(\n  { ${keys
          .map((key) => `${JSON.stringify(key.field.trim())}: ${SHELL.get(key.type)}`)
          .join(", ")} }${options.length > 0 ? `,\n  { ${options.join(", ")} }` : ""}\n)`

  const close = (next: boolean) => {
    if (busy) return
    onOpenChange(next)
    if (!next) {
      setDraft(EMPTY)
      setRefused("")
    }
  }

  const create = async () => {
    const request: MongoCreateIndex = {
      database,
      collection,
      keys: keys.map((key) => ({ field: key.field.trim(), type: key.type })),
      name: draft.name.trim() || undefined,
      unique: draft.unique || undefined,
      sparse: draft.sparse || undefined,
      hidden: draft.hidden || undefined,
      expireAfterSeconds: draft.ttl ? Number(draft.ttlSeconds) : undefined,
      partialFilterExpression: draft.partial.trim() ? draft.partial : undefined,
      collation: draft.collation.trim() ? draft.collation : undefined,
      weights: text && draft.weights.trim() ? draft.weights : undefined,
      defaultLanguage: text && draft.language.trim() ? draft.language.trim() : undefined,
      wildcardProjection: wildcard && draft.wildcard.trim() ? draft.wildcard : undefined,
    }
    setBusy(true)
    setRefused("")
    try {
      const made = await createIndex(id, request)
      notify.success(`Created the index ${made.name}`)
      onOpenChange(false)
      setDraft(EMPTY)
      onCreated()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      size="lg"
      title="Create index"
      description={`Create an index on ${collection}`}
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            {busy
              ? "The request stays open while the index is built."
              : "Building an index over a large collection takes a while."}
          </p>
          <Button variant="outline" disabled={busy} onClick={() => close(false)}>
            Cancel
          </Button>
          <Button
            variant={draft.ttl ? "destructive" : "default"}
            pending={busy}
            disabled={incomplete}
            onClick={() => void create()}
          >
            Create index
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{collection}</p>
            <FormFacts>
              <FormFact label="Database" mono>
                {database}
              </FormFact>
            </FormFacts>
          </div>
        </div>

        <div role="group" aria-labelledby="mongo-index-keys" className="space-y-1.5">
          <p id="mongo-index-keys" className="text-body font-medium">
            Fields
          </p>
          {draft.keys.map((key, index) => (
            <div key={key.id} className="flex min-w-0 items-center gap-2">
              <Input
                value={key.field}
                list="mongo-index-fields"
                spellCheck={false}
                autoComplete="off"
                aria-label={`Field ${index + 1}`}
                placeholder={index === 0 ? "email, address.city, $** for every field" : "field"}
                className="min-w-0 flex-1 font-mono"
                onChange={(event) => setKey(key.id, { field: event.target.value })}
              />
              <Select
                value={key.type}
                onValueChange={(type) => setKey(key.id, { type: type as MongoIndexKeyType })}
              >
                <SelectTrigger aria-label={`How field ${index + 1} is indexed`} className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KEY_TYPES.map((entry) => (
                    <SelectItem key={entry.value} value={entry.value}>
                      {entry.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <IconAction
                label={`Remove field ${index + 1}`}
                disabled={draft.keys.length === 1}
                onClick={() => set({ keys: draft.keys.filter((held) => held.id !== key.id) })}
              >
                <Cross />
              </IconAction>
            </div>
          ))}
          <datalist id="mongo-index-fields">
            {known.map((path) => (
              <option key={path} value={path} />
            ))}
          </datalist>
          {duplicate && (
            <p role="alert" className="text-hint text-destructive">
              {duplicate.field.trim()} is listed twice.
            </p>
          )}
          <Button
            size="xs"
            variant="ghost"
            disabled={draft.keys.length >= 32}
            onClick={() => {
              set({ keys: [...draft.keys, { id: serial, field: "", type: "asc" }] })
              setSerial(serial + 1)
            }}
          >
            <Plus />
            Add a field
          </Button>
        </div>

        <Field
          label="Name"
          htmlFor="mongo-index-name"
          hint="Blank names it after its fields."
          error={taken ? `${collection} already has an index called ${name}.` : undefined}
        >
          <Input
            id="mongo-index-name"
            value={draft.name}
            spellCheck={false}
            autoComplete="off"
            placeholder={defaultName(draft.keys) || "email_1"}
            className="font-mono"
            onChange={(event) => set({ name: event.target.value })}
          />
        </Field>

        <OptionList>
          <OptionRow
            title="Refuse two documents with the same value (unique)"
            checked={draft.unique}
            onCheckedChange={(unique) => set({ unique })}
          />
          <OptionRow
            title="Leave out documents that lack the field (sparse)"
            checked={draft.sparse}
            onCheckedChange={(sparse) => set({ sparse })}
          />
          {engine.can("indexHide") && (
            <OptionRow
              title="Build it hidden from the planner"
              hint="It is kept up to date and used by no query until it is unhidden."
              checked={draft.hidden}
              onCheckedChange={(hidden) => set({ hidden })}
            />
          )}
          {canDestroy && (
            <OptionRow
              tone={draft.ttl ? "danger" : "default"}
              title="Delete documents when the field's date is older than a limit (TTL)"
              hint="The moment the index exists, the server deletes every document already older than the limit."
              checked={draft.ttl}
              onCheckedChange={(ttl) => set({ ttl })}
            >
              <Field
                label="Seconds after the date"
                htmlFor="mongo-index-ttl"
                error={
                  ttlBad
                    ? "A whole number of seconds."
                    : ttlKeys
                      ? "An index that expires documents is on exactly one field."
                      : undefined
                }
              >
                <Input
                  id="mongo-index-ttl"
                  inputMode="numeric"
                  value={draft.ttlSeconds}
                  className="numeric w-40"
                  onChange={(event) => set({ ttlSeconds: event.target.value })}
                />
              </Field>
            </OptionRow>
          )}
        </OptionList>

        <Disclosure quiet summary="More options">
          <div className="space-y-4 pt-1">
            <Field
              label="Only documents matching (partial)"
              htmlFor="mongo-index-partial"
              error={partialBad ?? undefined}
            >
              <CodeField
                id="mongo-index-partial"
                dense
                value={draft.partial}
                invalid={Boolean(partialBad)}
                placeholder="{ status: { $exists: true } }"
                onChange={(partial) => set({ partial })}
              />
            </Field>
            <Field
              label="Collation"
              htmlFor="mongo-index-collation"
              error={collationBad ?? undefined}
            >
              <CodeField
                id="mongo-index-collation"
                dense
                value={draft.collation}
                invalid={Boolean(collationBad)}
                placeholder='{ locale: "en", strength: 2 }'
                onChange={(collation) => set({ collation })}
              />
            </Field>
            {text && (
              <FieldRow>
                <Field
                  label="Weights"
                  htmlFor="mongo-index-weights"
                  error={weightsBad ?? undefined}
                >
                  <CodeField
                    id="mongo-index-weights"
                    dense
                    value={draft.weights}
                    invalid={Boolean(weightsBad)}
                    placeholder="{ title: 10, body: 1 }"
                    onChange={(weights) => set({ weights })}
                  />
                </Field>
                <Field label="Language" htmlFor="mongo-index-language">
                  <Input
                    id="mongo-index-language"
                    value={draft.language}
                    placeholder="english"
                    onChange={(event) => set({ language: event.target.value })}
                  />
                </Field>
              </FieldRow>
            )}
            {wildcard && (
              <Field
                label="Fields the wildcard covers"
                htmlFor="mongo-index-wildcard"
                error={wildcardBad ?? undefined}
              >
                <CodeField
                  id="mongo-index-wildcard"
                  dense
                  value={draft.wildcard}
                  invalid={Boolean(wildcardBad)}
                  placeholder="{ attributes: 1 }"
                  onChange={(next) => set({ wildcard: next })}
                />
              </Field>
            )}
          </div>
        </Disclosure>

        <Statement
          label="Command"
          sql={statement}
          placeholder="Name a field and the command is written here."
        />

        {refused && (
          <Notice tone="danger" title="The index was not created">
            {refused}
          </Notice>
        )}
      </div>
    </Modal>
  )
}
