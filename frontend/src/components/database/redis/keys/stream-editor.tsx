"use client"

import { useState } from "react"
import { Copy, Eye, Minus, Plus, Trash } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, plural, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { Disclosure, Field, FormFact, FormNote } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Detail, DetailList, Metric, MetricStrip } from "@/components/page"
import { EmptyNote, EmptyState, LoadingRows } from "@/components/state"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
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
import {
  redisAck,
  redisDelete,
  redisGroupRemove,
  redisGroupSet,
  redisRun,
  redisStream,
  redisStreamPending,
  redisTrim,
  redisWrite,
} from "@/components/database/redis/api"
import { bytesId, bytesLabel } from "@/components/database/redis/bytes"
import { EditorStrip } from "@/components/database/redis/keys/editor-parts"
import type { KeyEditorProps } from "@/components/database/redis/keys/key-pane"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import { MemberTable, type MemberColumn } from "@/components/database/redis/keys/member-table"
import { useMembers } from "@/components/database/redis/keys/members"
import { ReadError } from "@/components/database/redis/read-error"
import { entryMoment, localMoment } from "@/components/database/redis/ttl"
import type { RedisBytes, RedisRow, RedisStreamGroup } from "@/components/database/redis/types"

type View = "entries" | "groups"

type EntryDraft = { id: string; pairs: { field: string; value: string }[] }

const NEW_ENTRY: EntryDraft = { id: "*", pairs: [{ field: "", value: "" }] }

/**
 * A stream: the entries in it, and the consumer groups reading them.
 *
 * An entry is its id and its pairs, in the order they were written; it is
 * added and removed, never edited, because a stream is a log. Groups are the
 * other half of one: where each has read to, what it was handed and has not
 * acknowledged, and which consumers hold it.
 */
export function StreamEditor(props: KeyEditorProps) {
  const { redis, name, epoch } = props
  const [view, setView] = useState<View>("entries")
  const info = usePoll(
    (signal) => redisStream(redis.target, name, signal),
    view === "groups" ? 10_000 : 0,
    [redis.target.id, redis.target.db, bytesId(name), epoch],
  )
  const tab = (id: View, label: string, count?: number) => (
    <button
      type="button"
      aria-pressed={view === id}
      onClick={() => setView(id)}
      className={tabClasses(view === id, "h-10")}
    >
      {label}
      {count !== undefined && <ChipCount>{count.toLocaleString()}</ChipCount>}
    </button>
  )
  const tabs = (
    <div role="group" aria-label="What of the stream is shown" className="-ml-3 flex self-stretch">
      {tab("entries", "Entries")}
      {tab("groups", "Consumer groups", info.data?.groups.length)}
    </div>
  )
  return view === "entries" ? (
    <Entries {...props} tabs={tabs} onStream={info.refresh} />
  ) : (
    <Groups {...props} tabs={tabs} info={info} />
  )
}

function Entries({
  redis,
  name,
  meta,
  epoch,
  onChanged,
  confirm,
  tabs,
  onStream,
}: KeyEditorProps & { tabs: React.ReactNode; onStream: () => void }) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [order, setOrder] = useState<"asc" | "desc">("desc")
  const members = useMembers(target, name, { order }, epoch)
  const rows = members.state?.rows ?? []

  const [draft, setDraft] = useState<EntryDraft | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [shown, setShown] = useState<RedisRow | null>(null)
  const [trimming, setTrimming] = useState(false)

  const pairs = draft?.pairs.filter((pair) => pair.field !== "" || pair.value !== "") ?? []

  const add = async () => {
    if (!draft || pairs.length === 0) return
    setBusy(true)
    setRefused("")
    try {
      const answer = await redisWrite(target, {
        key: name,
        type: "stream",
        id: draft.id.trim() || "*",
        entries: pairs.map((pair) => [pair.field, pair.value]),
      })
      notify.success(`Entry ${answer.id ?? ""} added`)
      setOpen(false)
      setDraft(null)
      members.reload()
      onStream()
      onChanged()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const remove = (row: RedisRow) =>
    confirm({
      title: "Delete entry",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{row.id}</span>,
        facts: (
          <>
            <FormFact label="Of" mono>
              {bytesLabel(name)}
            </FormFact>
            <FormFact label="In">db{db}</FormFact>
          </>
        ),
      },
      description:
        "The entry is removed from the stream. A group that was handed it and has not acknowledged it keeps a pending id with nothing behind it.",
      confirmLabel: "Delete entry",
      action: async () => {
        await redisDelete(target, { key: name, type: "stream", members: [row.id ?? ""] })
        members.patch((held) => ({
          rows: held.rows.filter((other) => other.id !== row.id),
          length: Math.max(held.length - 1, 0),
        }))
      },
      onDone: () => {
        onStream()
        onChanged()
      },
    })

  const columns: MemberColumn<RedisRow>[] = [
    {
      id: "id",
      label: "Entry",
      width: "11.5rem",
      cell: (row) => <span className="truncate">{row.id}</span>,
    },
    {
      id: "at",
      label: "Added",
      width: "10rem",
      lead: true,
      cell: (row) => {
        const at = entryMoment(row.id ?? "")
        return (
          <span className="truncate font-sans text-muted-foreground">
            {at ? timestamp(at.toISOString()) : "—"}
          </span>
        )
      },
    },
    {
      id: "fields",
      label: "Fields",
      width: "minmax(10rem,1fr)",
      cell: (row) => (
        <span className="flex min-w-0 items-center gap-3 overflow-hidden">
          {(row.fields ?? []).map(([field, value], index) => (
            <span key={index} className="flex min-w-0 shrink-0 items-center">
              <span className="text-muted-foreground">{bytesLabel(field)}</span>
              <span className="text-muted-foreground/50">=</span>
              <span className="max-w-64 truncate">{bytesLabel(value)}</span>
            </span>
          ))}
        </span>
      ),
    },
  ]

  return (
    <>
      <EditorStrip className="py-0">
        {tabs}
        <div role="group" aria-label="Order of the entries" className="flex gap-0.5">
          <FilterChip selected={order === "desc"} onClick={() => setOrder("desc")}>
            Newest first
          </FilterChip>
          <FilterChip selected={order === "asc"} onClick={() => setOrder("asc")}>
            Oldest first
          </FilterChip>
        </div>
        {/* One group, so the commands wrap together and stay on the right edge. */}
        <div className="ml-auto flex items-center gap-2 py-1.5">
          {canDestroy && (
            <Button size="xs" variant="ghost" onClick={() => setTrimming(true)}>
              Trim…
            </Button>
          )}
          {canWrite && (
            <Button
              size="xs"
              variant="outline"
              onClick={() => {
                setDraft(draft ?? NEW_ENTRY)
                setRefused("")
                setOpen(true)
              }}
            >
              <Plus />
              Add entry
            </Button>
          )}
        </div>
      </EditorStrip>
      <MemberTable
        label={`Entries of ${bytesLabel(name)}`}
        columns={columns}
        rows={rows}
        rowId={(row) => row.id ?? ""}
        paged={members}
        total={members.state?.length ?? meta.length}
        noun="entry"
        nouns="entries"
        empty={<EmptyNote>The stream holds no entries.</EmptyNote>}
        actionsWidth="5rem"
        // An id, a time and the pairs: three columns need more room than two.
        fits={640}
        actions={(row) => (
          <RowActions>
            <IconAction
              label={`Read entry ${row.id}`}
              className="size-6"
              onClick={() => setShown(row)}
            >
              <Eye />
            </IconAction>
            <IconAction
              label={`Copy entry ${row.id} as JSON`}
              className="size-6"
              onClick={() => void copyText(entryJson(row), "Entry copied")}
            >
              <Copy />
            </IconAction>
            {canDestroy && (
              <IconAction
                label={`Delete entry ${row.id}`}
                className="size-6"
                onClick={() => remove(row)}
              >
                <Trash />
              </IconAction>
            )}
          </RowActions>
        )}
      />

      <Modal
        open={shown !== null}
        onOpenChange={(next) => !next && setShown(null)}
        title={<span className="font-mono text-body">{shown?.id}</span>}
        description="One entry of the stream, with every field in full."
        size="lg"
        initialFocus="body"
      >
        <DetailList className="font-mono">
          {(shown?.fields ?? []).map(([field, value], index) => (
            <Detail key={index} label={bytesLabel(field)} className="break-all whitespace-pre-wrap">
              {bytesLabel(value)}
            </Detail>
          ))}
        </DetailList>
      </Modal>

      {draft && (
        <MemberDialog
          open={open}
          onOpenChange={setOpen}
          onCancel={() => {
            setOpen(false)
            setDraft(null)
          }}
          title="Add entry"
          name={name}
          type="stream"
          db={db}
          command="Add entry"
          onSubmit={add}
          busy={busy}
          error={refused}
          disabled={pairs.length === 0}
          size="lg"
        >
          <Field
            label="Entry id"
            htmlFor="redis-stream-id"
            hint="* lets the server give it the time it arrives. An id of your own must be later than the last one."
          >
            <Input
              id="redis-stream-id"
              value={draft.id}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              onChange={(event) => setDraft({ ...draft, id: event.target.value })}
            />
          </Field>
          <div role="group" aria-labelledby="redis-stream-pairs" className="space-y-1.5">
            <p id="redis-stream-pairs" className="text-body font-medium">
              Fields
            </p>
            {draft.pairs.map((pair, index) => (
              <div key={index} className="flex items-center gap-1.5">
                <Input
                  aria-label={`Field ${index + 1} name`}
                  placeholder="field"
                  value={pair.field}
                  spellCheck={false}
                  autoComplete="off"
                  className="font-mono sm:w-2/5"
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      pairs: draft.pairs.map((other, at) =>
                        at === index ? { ...other, field: event.target.value } : other,
                      ),
                    })
                  }
                />
                <Input
                  aria-label={`Field ${index + 1} value`}
                  placeholder="value"
                  value={pair.value}
                  spellCheck={false}
                  autoComplete="off"
                  className="font-mono"
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      pairs: draft.pairs.map((other, at) =>
                        at === index ? { ...other, value: event.target.value } : other,
                      ),
                    })
                  }
                />
                <IconAction
                  label={`Remove field ${index + 1}`}
                  disabled={draft.pairs.length === 1}
                  onClick={() =>
                    setDraft({ ...draft, pairs: draft.pairs.filter((_, at) => at !== index) })
                  }
                >
                  <Minus />
                </IconAction>
              </div>
            ))}
            <Button
              type="button"
              size="xs"
              variant="ghost"
              onClick={() =>
                setDraft({ ...draft, pairs: [...draft.pairs, { field: "", value: "" }] })
              }
            >
              <Plus />
              Another field
            </Button>
          </div>
        </MemberDialog>
      )}

      {trimming && (
        <TrimDialog
          redis={redis}
          name={name}
          length={members.state?.length ?? meta.length}
          onOpenChange={setTrimming}
          onTrimmed={() => {
            members.reload()
            onStream()
            onChanged()
          }}
        />
      )}
    </>
  )
}

function entryJson(row: RedisRow): string {
  return JSON.stringify(
    {
      id: row.id,
      fields: Object.fromEntries(
        (row.fields ?? []).map(([field, value]) => [bytesLabel(field), bytesLabel(value)]),
      ),
    },
    null,
    2,
  )
}

type TrimBy = "count" | "id" | "time"

/**
 * Drop the old end of a stream: keep the newest so many entries, or remove
 * everything before an entry — named by its id, or by the moment an id of
 * that time would have been given.
 */
function TrimDialog({
  redis,
  name,
  length,
  onOpenChange,
  onTrimmed,
}: {
  redis: KeyEditorProps["redis"]
  name: RedisBytes
  length: number
  onOpenChange: (open: boolean) => void
  onTrimmed: () => void
}) {
  const [by, setBy] = useState<TrimBy>("count")
  const [keep, setKeep] = useState("")
  const [before, setBefore] = useState("")
  const [moment, setMoment] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const counted = /^\d+$/.test(keep.trim())
  const going = counted ? Math.max(length - Number(keep), 0) : 0
  // An entry id is a time in milliseconds, then a sequence number within it.
  const id = before.trim()
  const named = /^\d+(-\d+)?$/.test(id)
  const at = moment ? new Date(moment).getTime() : Number.NaN
  const valid =
    by === "count" ? counted && going > 0 : by === "id" ? named : Number.isFinite(at) && at > 0
  const run = async () => {
    setBusy(true)
    setRefused("")
    try {
      const answer = await redisTrim(
        redis.target,
        by === "count"
          ? { key: name, maxLen: Number(keep) }
          : { key: name, minId: by === "id" ? id : String(at) },
      )
      notify.success(`${plural(answer.removed, "entry", "entries")} removed`)
      onOpenChange(false)
      onTrimmed()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <MemberDialog
      open
      onOpenChange={onOpenChange}
      onCancel={() => onOpenChange(false)}
      title="Trim stream"
      name={name}
      type="stream"
      db={redis.db}
      command={
        by === "count" && going > 0
          ? `Remove ${plural(going, "entry", "entries")}`
          : "Remove the older entries"
      }
      onSubmit={run}
      busy={busy}
      error={refused}
      disabled={!valid}
    >
      <Field label="What is removed">
        <Segments
          label="How the stream is trimmed"
          fill
          value={by}
          onChange={(next) => {
            setBy(next)
            setRefused("")
          }}
          options={[
            { value: "count", label: "All but the newest" },
            { value: "id", label: "Before an entry" },
            { value: "time", label: "Before a time" },
          ]}
        />
      </Field>
      {by === "count" ? (
        <Field
          label="Entries to keep"
          htmlFor="redis-stream-keep"
          hint={`The stream holds ${length.toLocaleString()}. The newest are kept and the oldest removed, for good.`}
        >
          <Input
            id="redis-stream-keep"
            inputMode="numeric"
            value={keep}
            className="font-mono"
            onChange={(event) => setKeep(event.target.value)}
          />
        </Field>
      ) : by === "id" ? (
        <Field
          label="Entry id"
          htmlFor="redis-stream-before"
          hint="Every entry with a lower id is removed, for good. This one and the ones after it stay."
          error={
            id && !named ? "An entry id is a number, or two with a dash: 1727800000000-0." : ""
          }
        >
          <Input
            id="redis-stream-before"
            value={before}
            spellCheck={false}
            autoComplete="off"
            placeholder="1727800000000-0"
            className="font-mono"
            onChange={(event) => setBefore(event.target.value)}
          />
        </Field>
      ) : (
        <Field
          label="Date and time"
          htmlFor="redis-stream-before-time"
          hint="Every entry added before this moment is removed, for good. An entry whose id was chosen by hand is judged by that id."
        >
          <Input
            id="redis-stream-before-time"
            type="datetime-local"
            value={moment}
            max={localMoment(new Date())}
            onChange={(event) => setMoment(event.target.value)}
          />
        </Field>
      )}
    </MemberDialog>
  )
}

function Groups({
  redis,
  name,
  confirm,
  tabs,
  info,
}: KeyEditorProps & {
  tabs: React.ReactNode
  info: ReturnType<typeof usePoll<Awaited<ReturnType<typeof redisStream>>>>
}) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [creating, setCreating] = useState(false)
  const [moving, setMoving] = useState<RedisStreamGroup | null>(null)
  const stream = info.data

  const subject = (label: React.ReactNode) => ({
    mark: <EngineMark engine={engine} size="sm" />,
    name: <span className="font-mono">{label}</span>,
    facts: (
      <>
        <FormFact label="Of" mono>
          {bytesLabel(name)}
        </FormFact>
        <FormFact label="In">db{db}</FormFact>
      </>
    ),
  })

  const destroy = (group: RedisStreamGroup) =>
    confirm({
      title: "Destroy group",
      subject: subject(bytesLabel(group.name)),
      description: `The group, its ${plural(group.consumers.length, "consumer")} and its record of ${plural(group.pending, "pending entry", "pending entries")} are removed. The stream's entries stay.`,
      confirmLabel: "Destroy group",
      action: async () => {
        // No `consumer`: that is what makes this the whole group.
        await redisGroupRemove(target, { key: name, group: group.name })
      },
      onDone: info.refresh,
    })

  const dropConsumer = (group: RedisStreamGroup, consumer: RedisBytes, pending: number) =>
    confirm({
      title: "Remove consumer",
      subject: subject(bytesLabel(consumer) || "(empty name)"),
      description: `The consumer is removed from ${bytesLabel(group.name)}, with the ${plural(pending, "entry", "entries")} it was handed and has not acknowledged. The group stays.`,
      confirmLabel: "Remove consumer",
      action: async () => {
        // `consumer` is always sent here, even when its name is empty: that
        // is what keeps this from destroying the group.
        await redisGroupRemove(target, { key: name, group: group.name, consumer })
      },
      onDone: info.refresh,
    })

  return (
    <>
      <EditorStrip className="py-0">
        {tabs}
        <span className="min-w-0 flex-1" />
        {canWrite && (
          <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
            <Plus />
            New group
          </Button>
        )}
      </EditorStrip>
      <div className="min-h-0 flex-1 overflow-auto">
        {info.error && !stream ? (
          <ReadError error={info.error} onRetry={info.refresh} className="m-3" />
        ) : !stream ? (
          <LoadingRows rows={4} className="p-3" />
        ) : (
          <div className="space-y-5 p-3">
            <MetricStrip>
              <Metric label="Entries" value={stream.length.toLocaleString()} />
              {stream.entriesAdded !== undefined && (
                <Metric label="Ever added" value={stream.entriesAdded.toLocaleString()} />
              )}
              <Metric
                label="First entry"
                value={<span className="font-mono text-xs">{stream.firstId ?? "—"}</span>}
              />
              <Metric
                label="Last entry"
                value={<span className="font-mono text-xs">{stream.lastId ?? "—"}</span>}
              />
            </MetricStrip>
            {stream.groups.length === 0 ? (
              <EmptyState
                title="No consumer group reads this stream"
                description="A group hands each entry to one of its consumers and remembers which were acknowledged."
                action={
                  canWrite && (
                    <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                      <Plus />
                      New group
                    </Button>
                  )
                }
              />
            ) : (
              stream.groups.map((group) => {
                const label = bytesLabel(group.name)
                const verbs: Verb[] = [
                  ...(canWrite
                    ? [
                        {
                          key: "move",
                          label: "Move its position…",
                          icon: Plus,
                          run: () => setMoving(group),
                        },
                      ]
                    : []),
                  ...(canDestroy
                    ? [
                        {
                          key: "destroy",
                          label: "Destroy group",
                          icon: Trash,
                          danger: true,
                          run: () => destroy(group),
                        },
                      ]
                    : []),
                ]
                return (
                  <section key={bytesId(group.name)} className="space-y-2">
                    <div className="flex min-w-0 items-center gap-2 border-b border-hairline pb-1.5">
                      <h3 className="min-w-0 truncate font-mono text-xs font-medium">{label}</h3>
                      <span className="min-w-0 flex-1" />
                      {verbs.length > 0 && (
                        <VerbActions verbs={verbs} menuLabel={`Actions for group ${label}`} />
                      )}
                    </div>
                    <MetricStrip>
                      <Metric
                        label="Read up to"
                        value={<span className="font-mono text-xs">{group.lastDeliveredId}</span>}
                      />
                      <Metric label="Pending" value={group.pending.toLocaleString()} />
                      {group.lag !== undefined && (
                        <Metric label="Not yet read" value={group.lag.toLocaleString()} />
                      )}
                      <Metric label="Consumers" value={group.consumers.length.toLocaleString()} />
                    </MetricStrip>
                    {group.consumers.length > 0 && (
                      <Table>
                        <TableHeader>
                          <TableRow className="hover:bg-transparent">
                            <TableHead className="h-8 px-2">Consumer</TableHead>
                            <TableHead className="h-8 px-2 text-right">Pending</TableHead>
                            <TableHead className="h-8 px-2 text-right">Idle</TableHead>
                            <TableHead className="h-8 w-10 px-2">
                              <span className="sr-only">Actions</span>
                            </TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {group.consumers.map((consumer) => (
                            <TableRow key={bytesId(consumer.name)} className="group">
                              <TableCell className="px-2 py-1.5 font-mono">
                                {bytesLabel(consumer.name) || (
                                  <span className="text-muted-foreground italic">empty name</span>
                                )}
                              </TableCell>
                              <TableCell className="numeric px-2 py-1.5 text-right">
                                {consumer.pending.toLocaleString()}
                              </TableCell>
                              <TableCell className="numeric px-2 py-1.5 text-right text-muted-foreground">
                                {duration(consumer.idleMs / 1000)}
                              </TableCell>
                              <TableCell className="px-2 py-1">
                                {canDestroy && (
                                  <RowActions>
                                    <IconAction
                                      label={`Remove consumer ${bytesLabel(consumer.name)} from ${label}`}
                                      className="size-6"
                                      onClick={() =>
                                        dropConsumer(group, consumer.name, consumer.pending)
                                      }
                                    >
                                      <Trash />
                                    </IconAction>
                                  </RowActions>
                                )}
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    )}
                    {group.pending > 0 && typeof group.name === "string" && (
                      <Disclosure quiet summary="Pending entries" className="px-3">
                        <Pending
                          redis={redis}
                          name={name}
                          group={group.name}
                          consumers={group.consumers.map((consumer) => consumer.name)}
                          onAcked={info.refresh}
                        />
                      </Disclosure>
                    )}
                  </section>
                )
              })
            )}
            {stream.groupsOmitted ? (
              <FormNote>
                {stream.groupsOmitted.toLocaleString()} more groups are not listed here.
              </FormNote>
            ) : null}
          </div>
        )}
      </div>
      {(creating || moving) && (
        <GroupDialog
          redis={redis}
          name={name}
          group={moving ?? undefined}
          onOpenChange={() => {
            setCreating(false)
            setMoving(null)
          }}
          onSaved={info.refresh}
        />
      )}
    </>
  )
}

/**
 * What a group was handed and has not acknowledged. An entry leaves the list
 * two ways: acknowledged, which says its work is done, or claimed, which
 * hands it to another consumer because the one holding it will not finish.
 */
function Pending({
  redis,
  name,
  group,
  consumers,
  onAcked,
}: {
  redis: KeyEditorProps["redis"]
  name: RedisBytes
  group: string
  /** The group's consumers, as the ones an entry can be handed to. */
  consumers: RedisBytes[]
  onAcked: () => void
}) {
  const [claiming, setClaiming] = useState<{ id: string; holder: RedisBytes } | null>(null)
  const pending = usePoll(
    (signal) => redisStreamPending(redis.target, name, { group, count: 200 }, signal),
    0,
    [redis.target.id, redis.target.db, bytesId(name), group],
  )
  const [busy, setBusy] = useState("")
  const ack = async (ids: string[]) => {
    setBusy(ids.length === 1 ? ids[0] : "all")
    try {
      const answer = await redisAck(redis.target, { key: name, group, ids })
      notify.success(`${answer.acknowledged.toLocaleString()} acknowledged`)
      pending.refresh()
      onAcked()
    } catch (err) {
      notify.error("Could not acknowledge", err)
    } finally {
      setBusy("")
    }
  }
  if (pending.error && !pending.data) {
    return <ReadError error={pending.error} onRetry={pending.refresh} />
  }
  if (!pending.data) return <LoadingRows rows={2} />
  const entries = pending.data.entries
  if (entries.length === 0) return <EmptyNote className="py-3">Nothing is pending.</EmptyNote>
  return (
    <div className="space-y-2">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 px-2">Entry</TableHead>
            <TableHead className="h-8 px-2">Held by</TableHead>
            <TableHead className="h-8 px-2 text-right">For</TableHead>
            <TableHead className="h-8 px-2 text-right">Deliveries</TableHead>
            <TableHead className="h-8 px-2">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((entry) => (
            <TableRow key={entry.id}>
              <TableCell className="px-2 py-1.5 font-mono">{entry.id}</TableCell>
              <TableCell className="px-2 py-1.5 font-mono">{bytesLabel(entry.consumer)}</TableCell>
              <TableCell className="numeric px-2 py-1.5 text-right text-muted-foreground">
                {duration(entry.idleMs / 1000)}
              </TableCell>
              <TableCell
                className={cn(
                  "numeric px-2 py-1.5 text-right",
                  entry.deliveries > 1 && "text-warning",
                )}
              >
                {entry.deliveries.toLocaleString()}
              </TableCell>
              <TableCell className="px-2 py-1">
                {redis.canWrite && (
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      size="xs"
                      variant="ghost"
                      disabled={busy !== ""}
                      onClick={() => setClaiming({ id: entry.id, holder: entry.consumer })}
                    >
                      Claim…
                    </Button>
                    <Button
                      size="xs"
                      variant="ghost"
                      pending={busy === entry.id}
                      disabled={busy !== ""}
                      onClick={() => void ack([entry.id])}
                    >
                      Acknowledge
                    </Button>
                  </div>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {!pending.data.done && (
        <FormNote>The first {entries.length.toLocaleString()} are listed.</FormNote>
      )}
      {claiming && (
        <ClaimDialog
          redis={redis}
          name={name}
          group={group}
          entry={claiming.id}
          holder={claiming.holder}
          consumers={consumers}
          onOpenChange={() => setClaiming(null)}
          onClaimed={() => {
            pending.refresh()
            onAcked()
          }}
        />
      )}
    </div>
  )
}

/**
 * Hand a pending entry to another consumer of its group.
 *
 * The server has the command and the dashboard no route for it, so it goes
 * through the console's: `XCLAIM`, classified and audited as a line typed
 * there. Only the entry's owner changes — nothing is read, so the entry's
 * fields never pass through here.
 */
function ClaimDialog({
  redis,
  name,
  group,
  entry,
  holder,
  consumers,
  onOpenChange,
  onClaimed,
}: {
  redis: KeyEditorProps["redis"]
  name: RedisBytes
  group: string
  entry: string
  holder: RedisBytes
  consumers: RedisBytes[]
  onOpenChange: (open: boolean) => void
  onClaimed: () => void
}) {
  const [to, setTo] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const others = consumers.filter(
    (consumer): consumer is string =>
      typeof consumer === "string" && consumer !== "" && bytesId(consumer) !== bytesId(holder),
  )
  const claim = async () => {
    setBusy(true)
    setRefused("")
    try {
      const reply = await redisRun(redis.target, "XCLAIM", [name, group, to, 0, entry, "JUSTID"])
      if (reply.type === "array" && reply.items.length === 0) {
        setRefused("The entry is not pending any more: it was acknowledged, or removed.")
        return
      }
      notify.success(`Entry ${entry} handed to ${to}`)
      onOpenChange(false)
      onClaimed()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <MemberDialog
      open
      onOpenChange={onOpenChange}
      onCancel={() => onOpenChange(false)}
      title="Claim a pending entry"
      name={name}
      type="stream"
      db={redis.db}
      command="Claim entry"
      onSubmit={claim}
      busy={busy}
      error={refused}
      disabled={to === ""}
    >
      <DetailList className="font-mono">
        <Detail label="Entry">{entry}</Detail>
        <Detail label="Group">{group}</Detail>
        <Detail label="Held by">{bytesLabel(holder) || "(empty name)"}</Detail>
      </DetailList>
      <Field
        label="Hand it to"
        htmlFor="redis-claim-consumer"
        hint="A consumer of this group. A name the group has not seen becomes a consumer of it."
      >
        <Input
          id="redis-claim-consumer"
          value={to}
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
          onChange={(event) => setTo(event.target.value)}
        />
      </Field>
      {others.length > 0 && (
        <div role="group" aria-label="The group's other consumers" className="flex flex-wrap gap-1">
          {others.slice(0, 12).map((consumer) => (
            <FilterChip
              key={consumer}
              selected={to === consumer}
              className="font-mono"
              onClick={() => setTo(consumer)}
            >
              {consumer}
            </FilterChip>
          ))}
        </div>
      )}
    </MemberDialog>
  )
}

/** A new group, or an existing one moved to another place in the stream. */
function GroupDialog({
  redis,
  name,
  group,
  onOpenChange,
  onSaved,
}: {
  redis: KeyEditorProps["redis"]
  name: RedisBytes
  /** The group being moved; absent for a new one. */
  group?: RedisStreamGroup
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [label, setLabel] = useState("")
  const [from, setFrom] = useState<"new" | "start" | "id">("new")
  const [id, setId] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const start = from === "new" ? "$" : from === "start" ? "0" : id.trim()
  const save = async () => {
    setBusy(true)
    setRefused("")
    try {
      await redisGroupSet(redis.target, {
        key: name,
        group: group ? group.name : label,
        id: start,
        ...(group ? { setId: true } : {}),
      })
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <MemberDialog
      open
      onOpenChange={onOpenChange}
      onCancel={() => onOpenChange(false)}
      title={group ? `Move ${bytesLabel(group.name)}` : "New consumer group"}
      name={name}
      type="stream"
      db={redis.db}
      command={group ? "Move group" : "Create group"}
      onSubmit={save}
      busy={busy}
      error={refused}
      disabled={(!group && label === "") || (from === "id" && start === "")}
    >
      {!group && (
        <Field label="Group name" htmlFor="redis-group-name">
          <Input
            id="redis-group-name"
            value={label}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            onChange={(event) => setLabel(event.target.value)}
          />
        </Field>
      )}
      <Field label={group ? "Read next from" : "Starts reading from"}>
        <Segments
          label="Where the group reads from"
          fill
          value={from}
          onChange={setFrom}
          options={[
            { value: "new", label: "New entries only" },
            { value: "start", label: "The whole stream" },
            { value: "id", label: "After an entry" },
          ]}
        />
      </Field>
      {from === "id" && (
        <Field
          label="Entry id"
          htmlFor="redis-group-id"
          hint="The group is handed the entries after this one."
        >
          <Input
            id="redis-group-id"
            value={id}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
            onChange={(event) => setId(event.target.value)}
          />
        </Field>
      )}
    </MemberDialog>
  )
}
