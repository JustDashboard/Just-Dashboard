"use client"

import { useEffect, useId, useState } from "react"
import Link from "next/link"
import { Clock, Eye, EyeOff, Plus, RefreshClockwise, Trash } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { bytes, duration, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import { Modal } from "@/components/modal"
import { EmptyNote, EmptyState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { VerbActions, type Verb } from "@/components/verbs"
import { EngineMark } from "@/components/database/kit"
import { dropIndex, hideIndex, mongoIndexes, setIndexExpiry } from "@/components/database/mongo/api"
import { CreateIndexDialog } from "@/components/database/mongo/schema/index-dialog"
import type { MongoIndex } from "@/components/database/mongo/types"
import type { Workbench } from "@/components/database/mongo/workbench"
import { ReadError } from "@/components/database/redis/read-error"

const KEY_MARK: Record<string, string> = { asc: "↑", desc: "↓" }

/** An index's keys as the reader writes them: a field and which way, or what kind. */
function IndexKeys({ index }: { index: MongoIndex }) {
  return (
    <span className="flex min-w-0 flex-wrap gap-x-2 gap-y-0.5">
      {index.keys.map((key) => (
        <span key={`${key.field}:${key.type}`} className="font-mono text-hint whitespace-nowrap">
          <span className="text-muted-foreground">{key.field}</span>{" "}
          <span className="text-(--tag-cyan)">{KEY_MARK[key.type] ?? key.type}</span>
        </span>
      ))}
    </span>
  )
}

/** What an index is beyond its keys, as properties: each a fact, none of them a state. */
function IndexProperties({ index, quiet }: { index: MongoIndex; quiet?: boolean }) {
  const tags = [
    index.primary && "the _id index",
    index.unique && "unique",
    index.sparse && "sparse",
    index.expireAfterSeconds !== null && `expires after ${duration(index.expireAfterSeconds)}`,
    index.partialFilterExpression && "partial",
    index.collation && "collation",
    index.kind === "text" && index.defaultLanguage && index.defaultLanguage,
  ].filter((tag): tag is string => Boolean(tag))
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
      {tags.map((tag) => (
        <Tag key={tag}>{tag}</Tag>
      ))}
      {index.hidden && <Status tone="warning" label="Hidden from the planner" />}
      {index.building && <Status tone="notice" label="Building" />}
      {tags.length === 0 && !index.hidden && !index.building && !quiet && (
        <span className="text-muted-foreground/60">—</span>
      )}
    </span>
  )
}

/**
 * How much an index has been used, and over how long: the count runs from the
 * server's last start or the index's creation, whichever is later.
 */
function usageWords(index: MongoIndex): { figure: string; since: string } | null {
  if (!index.usage) return null
  const ago = relativeTime(index.usage.since)
  return {
    figure: `${index.usage.ops.toLocaleString("en-US")} ${index.usage.ops === 1 ? "use" : "uses"}`,
    since: ago === "just now" ? "in the last moments" : `in the last ${ago.replace(/ ago$/, "")}`,
  }
}

/**
 * The indexes of a collection: what each is on, how large it is, how much
 * the server has used it, and what it is besides.
 *
 * "Used" counts from the server's last start or the index's creation, so a
 * zero says how long it has gone unused, not that it is useless. Hiding an
 * index is how to find out: the planner stops using it, it is still kept up
 * to date, and unhiding it costs nothing — a drop cannot be undone.
 *
 * A view has no index of its own: its reads use those of the collection it
 * is on, so the server is not asked and the way to that collection's is
 * offered instead.
 */
export function IndexesView({
  mongo,
  catalog,
  collection: info,
  confirm,
  onChanged,
}: Pick<Workbench, "mongo" | "catalog" | "collection" | "confirm"> & {
  /** An index was made or dropped: the collection's own count of them is out of date. */
  onChanged: () => void
}) {
  const { id, target, database, collection, engine, canWrite, canDestroy, href } = mongo
  const view = info?.type === "view"
  // What the collection is comes from the list. Until the list has answered
  // (or failed), the indexes are not asked for: the server refuses a view's.
  const known = info !== undefined || Boolean(catalog.collections.error)
  const poll = usePoll(
    (signal) => mongoIndexes(target, signal),
    30_000,
    [id, database, collection],
    { enabled: known && !view },
  )
  const [creating, setCreating] = useState(false)
  const [changing, setChanging] = useState<string | null>(null)
  const [expiring, setExpiring] = useState<MongoIndex | null>(null)
  // A table needs its six columns. Under that width — the pane's, not the
  // window's — a row is drawn down instead of across, and nothing is dropped.
  const [pane, width] = useColumnWidth()
  const wide = width === 0 || width >= 640

  const indexes = poll.data?.indexes
  const total = indexes?.reduce((sum, index) => sum + Math.max(index.size, 0), 0) ?? 0
  // The most any index here has been used: what each one's bar is drawn against.
  const busiest = Math.max(...(indexes ?? []).map((index) => index.usage?.ops ?? 0), 1)

  // After a drop the row has gone, and its control with it: the list takes the
  // keyboard. It is handed over when the confirmation ends, and again once the
  // list has been read without the row, if the keyboard was left on a control
  // that has since gone.
  const listId = useId()
  const [dropped, setDropped] = useState<string | null>(null)
  const focusList = () => document.getElementById(listId)?.focus({ preventScroll: true })
  const landed = dropped !== null && poll.data?.indexes.every((index) => index.name !== dropped)
  useEffect(() => {
    if (landed && document.activeElement === document.body) {
      document.getElementById(listId)?.focus({ preventScroll: true })
    }
  }, [landed, listId])

  const setHidden = async (index: MongoIndex, hidden: boolean) => {
    setChanging(index.name)
    try {
      await hideIndex(target, index.name, hidden)
      notify.success(
        hidden ? `${index.name} is hidden from the planner` : `${index.name} is in use again`,
      )
      poll.refresh()
    } catch (err) {
      notify.error(`Could not ${hidden ? "hide" : "unhide"} ${index.name}`, err)
    } finally {
      setChanging(null)
    }
  }

  const verbsFor = (index: MongoIndex): Verb[] => [
    ...(canWrite && engine.can("indexHide") && !index.primary
      ? [
          {
            key: "hide",
            label: index.hidden ? "Unhide" : "Hide",
            icon: index.hidden ? Eye : EyeOff,
            inline: true,
            run: () => void setHidden(index, !index.hidden),
          },
        ]
      : []),
    ...(canDestroy && !index.primary && index.expireAfterSeconds !== null
      ? [
          {
            key: "expiry",
            label: "Change expiry…",
            icon: Clock,
            run: () => setExpiring(index),
          },
        ]
      : []),
    ...(canDestroy && !index.primary
      ? [
          {
            key: "drop",
            label: "Drop",
            icon: Trash,
            inline: true,
            danger: true,
            run: () =>
              confirm({
                title: `Drop the index ${index.name}`,
                description:
                  "The index is removed. Queries that used it scan instead, and building it again takes as long as it did the first time.",
                subject: {
                  mark: <EngineMark engine={engine} size="sm" />,
                  name: <span className="font-mono">{index.name}</span>,
                  facts: (
                    <FormFacts>
                      <FormFact label="Collection" mono>
                        {database}.{collection}
                      </FormFact>
                      <FormFact label="On" mono>
                        {index.keys.map((key) => key.field).join(", ")}
                      </FormFact>
                      {index.size >= 0 && <FormFact label="Size">{bytes(index.size)}</FormFact>}
                    </FormFacts>
                  ),
                },
                confirmLabel: "Drop index",
                action: async () => {
                  await dropIndex(target, index.name)
                },
                onDone: () => {
                  setDropped(index.name)
                  poll.refresh()
                  onChanged()
                  focusList()
                },
              }),
          },
        ]
      : []),
  ]

  if (view) {
    return (
      <EmptyState
        mark={<EngineMark engine={engine} />}
        className="m-4 min-h-0 flex-1 border-0"
        title="A view has no indexes of its own"
        description={`${collection} reads ${info?.viewOn ?? "another collection"}: a query on it uses the indexes of that collection.`}
        action={
          info?.viewOn && (
            <Button size="sm" variant="outline" asChild>
              <Link href={href("schema", { collection: info.viewOn, view: "indexes" })}>
                Open the indexes of {info.viewOn}
              </Link>
            </Button>
          )
        }
      />
    )
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline px-3 py-1">
        <p className="numeric min-w-0 flex-1 text-hint text-muted-foreground">
          {indexes
            ? `${indexes.length.toLocaleString("en-US")} ${indexes.length === 1 ? "index" : "indexes"} · ${bytes(total)} on disk`
            : ""}
        </p>
        <IconAction label="Read the indexes again" className="size-7" onClick={poll.refresh}>
          <RefreshClockwise />
        </IconAction>
        {canWrite && (
          <Button size="xs" onClick={() => setCreating(true)}>
            <Plus />
            Create index
          </Button>
        )}
      </div>

      <div
        ref={pane}
        id={listId}
        tabIndex={-1}
        aria-label={`Indexes of ${collection}`}
        className="min-h-0 flex-1 overflow-auto outline-none"
      >
        {poll.error && !indexes ? (
          <ReadError error={poll.error} onRetry={poll.refresh} className="m-4" />
        ) : !indexes ? (
          <LoadingRows rows={4} className="p-4" />
        ) : indexes.length === 0 ? (
          <EmptyNote className="py-10">This collection has no index.</EmptyNote>
        ) : (
          <div className="animate-rise">
            {poll.error && (
              <p role="status" className="px-3 pt-2 text-hint text-warning">
                The last read failed, so this list may be out of date: {errorMessage(poll.error)}
              </p>
            )}
            {poll.data?.usageNote && (
              <FormNote className="px-3 pt-2">
                How much each index is used could not be read: {poll.data.usageNote}
              </FormNote>
            )}
            {wide ? (
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Name and keys</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead className="text-right">Size</TableHead>
                    <TableHead>Used</TableHead>
                    <TableHead>Properties</TableHead>
                    <TableHead className="w-0">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {indexes.map((index) => {
                    const usage = usageWords(index)
                    return (
                      <TableRow
                        key={index.name}
                        data-slot="mongo-index"
                        className={cn("group", index.hidden && "text-muted-foreground")}
                      >
                        <TableCell className="max-w-80">
                          <p className="truncate font-mono font-medium" title={index.name}>
                            {index.name}
                          </p>
                          <IndexKeys index={index} />
                        </TableCell>
                        <TableCell>
                          <Tag>{index.kind}</Tag>
                        </TableCell>
                        <TableCell className="numeric text-right whitespace-nowrap">
                          {index.size >= 0 ? bytes(index.size) : "—"}
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          {usage ? (
                            <div className="w-36 space-y-1">
                              <p className="flex items-baseline gap-1.5">
                                <span className="numeric">{usage.figure}</span>
                                <span className="truncate text-hint text-muted-foreground">
                                  {usage.since}
                                </span>
                              </p>
                              {/* Against the busiest index here: which ones carry the reads is seen at a glance. */}
                              <Meter
                                size="thin"
                                value={((index.usage?.ops ?? 0) / busiest) * 100}
                                label={`How much ${index.name} is used beside the busiest index`}
                              />
                            </div>
                          ) : (
                            <span className="text-muted-foreground/60">—</span>
                          )}
                        </TableCell>
                        <TableCell>
                          <IndexProperties index={index} />
                        </TableCell>
                        <TableCell className="py-1 text-right">
                          <VerbActions
                            verbs={verbsFor(index).map((verb) =>
                              verb.key === "hide" && changing === index.name
                                ? { ...verb, disabled: true }
                                : verb,
                            )}
                            dim
                            menuLabel={`More actions for ${index.name}`}
                          />
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            ) : (
              <ul className="divide-y divide-hairline">
                {indexes.map((index) => {
                  const usage = usageWords(index)
                  return (
                    <li
                      key={index.name}
                      data-slot="mongo-index"
                      className={cn(
                        "group space-y-1.5 px-3 py-2.5",
                        index.hidden && "text-muted-foreground",
                      )}
                    >
                      <div className="flex min-w-0 items-start gap-2">
                        <div className="min-w-0 flex-1">
                          <p className="truncate font-mono text-xs font-medium">{index.name}</p>
                          <IndexKeys index={index} />
                        </div>
                        <VerbActions
                          verbs={verbsFor(index)}
                          dim
                          menuLabel={`More actions for ${index.name}`}
                        />
                      </div>
                      <p className="numeric flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs">
                        <Tag>{index.kind}</Tag>
                        <span>{index.size >= 0 ? bytes(index.size) : "size unknown"}</span>
                        {usage && (
                          <span className="text-muted-foreground">
                            {usage.figure} {usage.since}
                          </span>
                        )}
                      </p>
                      <IndexProperties index={index} quiet />
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
        )}
      </div>

      {canDestroy && (
        <ExpiryDialog
          mongo={mongo}
          index={expiring}
          confirm={confirm}
          onOpenChange={(open) => !open && setExpiring(null)}
          onChanged={poll.refresh}
        />
      )}
      {canWrite && (
        <CreateIndexDialog
          mongo={mongo}
          open={creating}
          existing={indexes ?? []}
          onOpenChange={setCreating}
          onCreated={() => {
            poll.refresh()
            onChanged()
          }}
        />
      )}
    </div>
  )
}

/**
 * A TTL index's limit, changed in place rather than by dropping the index and
 * building it again. The moment the new limit lands the server deletes every
 * document already older than it, so the change is asked for once more with
 * the index named.
 */
function ExpiryDialog({
  mongo,
  index,
  confirm,
  onOpenChange,
  onChanged,
}: Pick<Workbench, "mongo" | "confirm"> & {
  /** The index whose expiry is open; `null` closes the dialog. */
  index: MongoIndex | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { target, database, collection, engine } = mongo
  const [text, setText] = useState("")
  const [held, setHeld] = useState(index)
  if (index !== held) {
    setHeld(index)
    setText(index && index.expireAfterSeconds !== null ? String(index.expireAfterSeconds) : "")
  }
  if (!index) return null

  const value = text.trim()
  const bad = !/^\d+$/.test(value) || Number(value) > 2147483647
  const seconds = Number(value)
  const field = index.keys[0]?.field ?? ""

  const submit = () => {
    // One dialog at a time: the confirmation takes this one's place.
    onOpenChange(false)
    confirm({
      title: `Change when ${collection} expires`,
      description: (
        <>
          Every document whose <span className="font-mono">{field}</span> is more than{" "}
          {duration(seconds)} old is deleted — what is already that old within a minute, the rest as
          it ages. This cannot be undone.
        </>
      ),
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{index.name}</span>,
        facts: (
          <FormFacts>
            <FormFact label="Collection" mono>
              {database}.{collection}
            </FormFact>
            <FormFact label="Was">{duration(index.expireAfterSeconds)}</FormFact>
            <FormFact label="Becomes">
              {seconds.toLocaleString("en-US")} s ({duration(seconds)})
            </FormFact>
          </FormFacts>
        ),
      },
      confirmLabel: "Change expiry",
      action: async () => {
        await setIndexExpiry(target, index.name, seconds)
        notify.success(`${index.name} now expires documents after ${duration(seconds)}`)
        return "reported"
      },
      onDone: onChanged,
    })
  }

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      size="sm"
      title="Change expiry"
      description={`Change after how long the index ${index.name} deletes a document`}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={bad || seconds === index.expireAfterSeconds} onClick={submit}>
            Change expiry…
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{index.name}</p>
            <FormFacts>
              <FormFact label="Collection" mono>
                {database}.{collection}
              </FormFact>
              <FormFact label="On" mono>
                {field}
              </FormFact>
            </FormFacts>
          </div>
        </div>
        <Field
          label="Delete a document after"
          htmlFor="mongo-index-expiry"
          hint={
            bad
              ? "Seconds, counted from the date the field holds."
              : `Seconds: ${duration(seconds)}, counted from the date the field holds.`
          }
          error={bad && value ? "A whole number of seconds." : undefined}
        >
          <Input
            id="mongo-index-expiry"
            inputMode="numeric"
            value={text}
            className="numeric"
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !bad && seconds !== index.expireAfterSeconds) submit()
            }}
          />
        </Field>
      </div>
    </Modal>
  )
}
