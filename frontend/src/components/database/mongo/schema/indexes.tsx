"use client"

import { useState } from "react"
import { Eye, EyeOff, Plus, RefreshClockwise, Trash } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormFact, FormFacts, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { EmptyNote, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
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
import { dropIndex, hideIndex, mongoIndexes } from "@/components/database/mongo/api"
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
    index.expireAfterSeconds !== null &&
      `expires after ${index.expireAfterSeconds.toLocaleString("en-US")} s`,
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

function usageWords(index: MongoIndex): { figure: string; since: string } | null {
  if (!index.usage) return null
  return {
    figure: `${index.usage.ops.toLocaleString("en-US")} ${index.usage.ops === 1 ? "use" : "uses"}`,
    since: `since ${relativeTime(index.usage.since)}`,
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
 */
export function IndexesView({
  mongo,
  confirm,
  onChanged,
}: Pick<Workbench, "mongo" | "confirm"> & {
  /** An index was made or dropped: the collection's own count of them is out of date. */
  onChanged: () => void
}) {
  const { id, target, database, collection, engine, canWrite, canDestroy } = mongo
  const poll = usePoll((signal) => mongoIndexes(target, signal), 30_000, [id, database, collection])
  const [creating, setCreating] = useState(false)
  const [changing, setChanging] = useState<string | null>(null)
  // A table needs its six columns. Under that width — the pane's, not the
  // window's — a row is drawn down instead of across, and nothing is dropped.
  const [pane, width] = useColumnWidth()
  const wide = width === 0 || width >= 640

  const indexes = poll.data?.indexes
  const total = indexes?.reduce((sum, index) => sum + Math.max(index.size, 0), 0) ?? 0

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
                  poll.refresh()
                  onChanged()
                },
              }),
          },
        ]
      : []),
  ]

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

      <div ref={pane} className="min-h-0 flex-1 overflow-auto">
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
                            <>
                              <span className="numeric">{usage.figure}</span>{" "}
                              <span className="text-muted-foreground">{usage.since}</span>
                            </>
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
