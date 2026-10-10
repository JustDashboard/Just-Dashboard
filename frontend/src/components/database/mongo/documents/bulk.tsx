"use client"

import { useState } from "react"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { grouped } from "@/components/database/data/view"
import { EngineMark } from "@/components/database/kit"
import {
  countDocuments,
  deleteDocuments,
  updateDocuments,
  type MongoTarget,
} from "@/components/database/mongo/api"
import { CodeField } from "@/components/database/mongo/code-field"
import { isEmptyDocument, shapeProblem } from "@/components/database/mongo/shell"
import type { Mongo } from "@/components/database/mongo/use-mongo"

export type BulkMode = "update" | "delete"

/** A count taken for one filter text: it stands only while the text is that. */
type Counted = { filter: string; scope: "many" | "one"; matched: number; exact: boolean }

const documents = (n: number) => `${grouped(n)} ${n === 1 ? "document" : "documents"}`

/**
 * Change or remove every document a filter matches.
 *
 * The matches are counted first, and the command is then named for the
 * count: "Update 1,175 documents". Editing the filter takes the count away
 * until it is taken again, so the number on the button is never of another
 * filter. A filter that matches everything is asked for by name and, for a
 * removal or an update of every document, confirmed once more — and offered
 * only to a role that may make one.
 */
export function BulkDialog({
  mongo,
  target,
  mode,
  initialFilter,
  confirm,
  onOpenChange,
  onDone,
}: {
  mongo: Mongo
  target: MongoTarget
  /** `null` closes the dialog. */
  mode: BulkMode | null
  /** The filter the query bar holds when the dialog is opened. */
  initialFilter: string
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  if (!mode) return null
  return (
    <Bulk
      key={mode}
      mongo={mongo}
      target={target}
      mode={mode}
      initialFilter={initialFilter}
      confirm={confirm}
      onOpenChange={onOpenChange}
      onDone={onDone}
    />
  )
}

function Bulk({
  mongo,
  target,
  mode,
  initialFilter,
  confirm,
  onOpenChange,
  onDone,
}: {
  mongo: Mongo
  target: MongoTarget
  mode: BulkMode
  initialFilter: string
  confirm: (request: ConfirmRequest) => void
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const { engine, canDestroy } = mongo
  const [filter, setFilter] = useState(initialFilter)
  const [update, setUpdate] = useState('{\n  "$set": {  }\n}')
  const [scope, setScope] = useState<"many" | "one">("many")
  const [upsert, setUpsert] = useState(false)
  const [counted, setCounted] = useState<Counted | null>(null)
  const [busy, setBusy] = useState<"counting" | "running" | null>(null)
  const [refused, setRefused] = useState("")

  const everything = isEmptyDocument(filter)
  const filterBad = shapeProblem(filter, "document")
  const updateBad =
    mode === "update"
      ? (shapeProblem(update, "document or list") ??
        (update.trim() ? null : "Write the update: { $set: { … } }."))
      : null
  const current =
    counted !== null && counted.filter === filter && counted.scope === scope ? counted : null
  // One document with no filter is whichever comes first: the server refuses it, and so does this.
  const aimless = everything && scope === "one"
  // Every document of the collection: a removal's capability, for either verb.
  const forbidden = everything && scope === "many" && !canDestroy

  const count = async () => {
    setBusy("counting")
    setRefused("")
    try {
      if (mode === "update") {
        // The route's own dry run: it counts what the update would match and writes nothing.
        const result = await updateDocuments(target, {
          filter,
          update,
          many: scope === "many",
          dryRun: true,
          all: everything && scope === "many" ? true : undefined,
        })
        setCounted({ filter, scope, matched: result.matched, exact: true })
      } else {
        const result = await countDocuments(target, { filter })
        const matched = scope === "one" ? Math.min(result.value, 1) : result.value
        setCounted({ filter, scope, matched, exact: result.exact && result.scope === "filter" })
      }
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const finish = () => {
    onOpenChange(false)
    onDone()
  }

  const runUpdate = async (all: boolean) => {
    const result = await updateDocuments(target, {
      filter,
      update,
      many: scope === "many",
      upsert: upsert || undefined,
      all: all || undefined,
    })
    if (result.upsertedId) notify.success("Nothing matched, so one document was inserted")
    else if (result.matched === 0) notify.info("Nothing matched, so nothing was changed")
    else {
      notify.success(`${documents(result.modified)} changed`, {
        description:
          result.modified < result.matched
            ? `${documents(result.matched)} matched; the rest already read that way.`
            : undefined,
      })
    }
  }

  const run = async () => {
    if (!current) return
    const subject = {
      mark: <EngineMark engine={engine} size="sm" />,
      name: <span className="font-mono">{target.collection}</span>,
      facts: (
        <FormFacts>
          <FormFact label="Database" mono>
            {target.database}
          </FormFact>
          <FormFact label="Filter" mono>
            {everything ? "none: every document" : filter.trim().replace(/\s+/g, " ")}
          </FormFact>
        </FormFacts>
      ),
    }
    if (mode === "delete") {
      setBusy("running")
      setRefused("")
      try {
        // Counted once more by the route that will delete, so the number
        // confirmed is the number of this moment.
        const dry = await deleteDocuments(target, {
          filter,
          many: scope === "many",
          dryRun: true,
          all: everything && scope === "many" ? true : undefined,
        })
        if (dry.matched === 0) {
          setCounted({ filter, scope, matched: 0, exact: true })
          return
        }
        onOpenChange(false)
        confirm({
          title: `Delete ${documents(dry.matched)}`,
          description:
            everything && scope === "many"
              ? "Every document of the collection is deleted. Its indexes and its rule stay. This cannot be undone."
              : "The documents the filter matches are deleted. This cannot be undone.",
          subject,
          confirmLabel: `Delete ${documents(dry.matched)}`,
          action: async () => {
            const result = await deleteDocuments(target, {
              filter,
              many: scope === "many",
              all: everything && scope === "many" ? true : undefined,
            })
            notify.success(`${documents(result.deleted)} deleted`)
            return "reported"
          },
          onDone,
        })
      } catch (err) {
        setRefused(errorMessage(err))
      } finally {
        setBusy(null)
      }
      return
    }
    if (everything && scope === "many") {
      onOpenChange(false)
      confirm({
        title: `Update every document of ${target.collection}`,
        description: `No filter is set, so the update is applied to all ${documents(current.matched)}.`,
        subject,
        confirmLabel: `Update ${documents(current.matched)}`,
        action: async () => {
          await runUpdate(true)
          return "reported"
        },
        onDone,
      })
      return
    }
    setBusy("running")
    setRefused("")
    try {
      await runUpdate(false)
      finish()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const verb = mode === "update" ? "Update" : "Delete"
  const blocked = Boolean(filterBad) || Boolean(updateBad) || aimless || forbidden

  return (
    <Modal
      open
      onOpenChange={(open) => busy === null && onOpenChange(open)}
      size="lg"
      title={mode === "update" ? "Update documents" : "Delete documents"}
      description={`${verb} the documents of ${target.collection} that a filter matches`}
      footer={
        <>
          <p aria-live="polite" className="mr-auto min-w-0 text-body">
            {current ? (
              <>
                <span className="numeric font-medium">
                  {current.exact ? "" : "about "}
                  {grouped(current.matched)}
                </span>{" "}
                <span className="text-muted-foreground">
                  {current.matched === 1 ? "document matches" : "documents match"}
                </span>
              </>
            ) : (
              <span className="text-hint text-muted-foreground">
                Nothing is written until the matches are counted.
              </span>
            )}
          </p>
          <Button variant="outline" disabled={busy !== null} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {current && (current.matched > 0 || (mode === "update" && upsert)) ? (
            <Button
              variant={mode === "delete" ? "destructive" : "default"}
              pending={busy === "running"}
              disabled={blocked}
              onClick={() => void run()}
            >
              {verb} {documents(current.matched)}
              {mode === "delete" ? "…" : ""}
            </Button>
          ) : (
            <Button
              pending={busy === "counting"}
              disabled={blocked || current !== null}
              onClick={() => void count()}
            >
              Count the matches
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{target.collection}</p>
            <FormFacts>
              <FormFact label="Database" mono>
                {target.database}
              </FormFact>
            </FormFacts>
          </div>
        </div>

        <Field
          label="Filter"
          htmlFor="mongo-bulk-filter"
          hint={
            everything
              ? "No filter: every document of the collection."
              : "Which documents. The same text the query bar takes."
          }
          error={filterBad ?? undefined}
        >
          <CodeField
            id="mongo-bulk-filter"
            value={filter}
            invalid={Boolean(filterBad)}
            className="min-h-12"
            placeholder={'{ status: "cancelled" }'}
            onChange={(next) => {
              setFilter(next)
              setRefused("")
            }}
          />
        </Field>

        {mode === "update" && (
          <Field
            label="Update"
            htmlFor="mongo-bulk-update"
            hint="Update operators ($set, $unset, $inc, $push…), or a pipeline as a list."
            error={updateBad ?? undefined}
          >
            <CodeField
              id="mongo-bulk-update"
              value={update}
              invalid={Boolean(updateBad)}
              onChange={(next) => {
                setUpdate(next)
                setRefused("")
                // The dry run parsed the update too: a changed one is counted again.
                setCounted(null)
              }}
            />
          </Field>
        )}

        <Field label="How many">
          <Segments
            label="How many documents"
            value={scope}
            options={[
              { value: "many", label: "Every match" },
              { value: "one", label: "The first match" },
            ]}
            onChange={setScope}
          />
        </Field>

        {mode === "update" && (
          <OptionList>
            <OptionRow
              title="Insert a document when nothing matches"
              checked={upsert}
              onCheckedChange={setUpsert}
            />
          </OptionList>
        )}

        {aimless && (
          <FormNote tone="warning">
            With no filter, &ldquo;the first match&rdquo; is whichever document comes first. Write a
            filter, or choose every match.
          </FormNote>
        )}
        {forbidden && (
          <FormNote tone="warning">
            {verb === "Update" ? "Updating" : "Deleting"} every document of a collection needs the
            permission to remove data, which your role does not have. Write a filter.
          </FormNote>
        )}
        {current && current.matched === 0 && !(mode === "update" && upsert) && (
          <FormNote>Nothing matches this filter, so there is nothing to {mode}.</FormNote>
        )}
        {refused && (
          <Notice tone="danger" title={`Nothing was ${mode === "update" ? "changed" : "deleted"}`}>
            {refused}
          </Notice>
        )}
      </div>
    </Modal>
  )
}
