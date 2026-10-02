"use client"

import { useState } from "react"
import { Copy, Download, Layers, Pencil, RefreshClockwise, Trash } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, duration } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, InfoTip } from "@/components/form"
import { Metric, MetricStrip } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbActions, type Verb } from "@/components/verbs"
import { EngineMark } from "@/components/database/kit"
import { redisDelete, redisMeta, redisRawUrl } from "@/components/database/redis/api"
import { bytesId, bytesLabel, isBinary } from "@/components/database/redis/bytes"
import { KindMark, KindTag, kindLabel, kindOf, sizeOf } from "@/components/database/redis/kinds"
import { HashEditor } from "@/components/database/redis/keys/hash-editor"
import { JsonEditor } from "@/components/database/redis/keys/json-editor"
import { DuplicateDialog, RenameDialog } from "@/components/database/redis/keys/key-dialogs"
import { ListEditor } from "@/components/database/redis/keys/list-editor"
import { SetEditor } from "@/components/database/redis/keys/set-editor"
import { StreamEditor } from "@/components/database/redis/keys/stream-editor"
import { StringEditor } from "@/components/database/redis/keys/string-editor"
import { TtlFact } from "@/components/database/redis/keys/ttl-editor"
import { ZsetEditor } from "@/components/database/redis/keys/zset-editor"
import { lastRead, noteRead, ownEcho } from "@/components/database/redis/own-reads"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisBytes, RedisKeyMeta, RedisMetaFact } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** What every type's editor is handed: the key, what is known of it, and how to say it changed. */
export type KeyEditorProps = {
  redis: Redis
  name: RedisBytes
  meta: RedisKeyMeta
  /** Raised when the value should be read again from its start: the Refresh verb. */
  epoch: number
  /**
   * A write landed: read the key's facts again — its size, its memory. The
   * rows are the editor's own to keep: it lays the write over the ones it
   * holds, or reads them again where the write moved them.
   */
  onChanged: () => void
  confirm: (request: ConfirmRequest) => void
}

type ReadMeta = RedisKeyMeta & {
  readAt: number
  /** When this page had last read the key before this reading, if it had. */
  seenAt: number | undefined
}

/**
 * One key: what it is, and the editor its type calls for.
 *
 * The head states the key as facts — its type in its type's hue, how long it
 * has left (a fact you press to change), how much it holds and what that
 * costs in memory, how the server stores it — and the verbs that act on the
 * whole key. Under it, each type gets its own editor: a string is text, JSON
 * or bytes; a hash is fields; a stream is entries and the groups reading
 * them. A fact the server could not give says why instead of reading as
 * zero.
 */
export function KeyPane({
  redis,
  name,
  leading,
  confirm,
  onRail,
  onOpen,
  onClose,
}: {
  redis: Redis
  name: RedisBytes
  /** The control that brings the rail back, when it is hidden. */
  leading?: React.ReactNode
  confirm: (request: ConfirmRequest) => void
  /** The keys changed: the rail should read them again. */
  onRail: () => void
  onOpen: (key: RedisBytes) => void
  /** The key is gone or was removed: nothing is open. */
  onClose: () => void
}) {
  const { target, db, engine, conn, canWrite, canDestroy } = redis
  const label = bytesLabel(name)
  const meta = usePoll<ReadMeta>(
    (signal) => {
      // A request counts as a read from the moment it is sent: the server
      // reads the key whether or not the answer is kept, and a request that
      // was dropped for a newer one has still reset the key's idle clock.
      const sent = Date.now()
      const earlier = db === undefined ? undefined : lastRead(target.id, db, name)
      if (db !== undefined) noteRead(target.id, db, name, sent)
      return redisMeta(target, name, signal).then((read) => {
        // What this page had read of the key before this answer, judged when
        // the answer lands, so a listing made while it was on its way counts.
        // Then this read is noted itself: the idle time in the answer was
        // taken first, and the size read after it is what resets the clock
        // for the next.
        const readAt = Date.now()
        const latest = lastRead(target.id, read.db, name)
        const seenAt = latest !== undefined && latest !== sent ? latest : earlier
        noteRead(target.id, read.db, name, readAt)
        return { ...read, readAt, seenAt }
      })
    },
    15_000,
    [target.id, target.db, bytesId(name)],
  )
  const [epoch, setEpoch] = useState(0)
  const [renaming, setRenaming] = useState(false)
  const [duplicating, setDuplicating] = useState(false)
  // What the server said the first time this page asked. How long a key has
  // gone unread is true only then: reading its size counts as a read, so
  // every later answer is the time since this page last looked. And not
  // always then either — a key the list drew a moment ago was read by the
  // list, and its idle time is that moment.
  const [opened, setOpened] = useState<ReadMeta>()
  if (!opened && meta.data) setOpened(meta.data)
  const echoed =
    opened?.idleSeconds !== undefined && ownEcho(opened.idleSeconds, opened.seenAt, opened.readAt)

  // The whole key again, rows and all. A write does not come through here:
  // its editor has already laid it over the rows on screen, and reading them
  // again from the start would throw a reader three pages into a hash back
  // to its first.
  const reread = () => {
    setEpoch((n) => n + 1)
    meta.refresh()
  }

  const gone = meta.error instanceof ApiError && meta.error.code === "key_not_found"
  const read = meta.data

  const remove = () =>
    confirm({
      title: "Delete key",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{label}</span>,
        facts: (
          <>
            <FormFact label="In">
              {conn.name} · db{db}
            </FormFact>
            {read && <FormFact label="Type">{kindLabel(read.type)}</FormFact>}
            {read && sizeOf(read.type, read.length) && (
              <FormFact label="Holds">{sizeOf(read.type, read.length)}</FormFact>
            )}
          </>
        ),
      },
      description: "The key and everything it holds are removed. This cannot be undone.",
      confirmLabel: "Delete key",
      action: async () => {
        await redisDelete(target, { key: name })
      },
      onDone: onClose,
    })

  const verbs: Verb[] = [
    {
      key: "refresh",
      label: "Refresh",
      icon: RefreshClockwise,
      inline: true,
      run: reread,
    },
    ...(canWrite
      ? [
          {
            key: "rename",
            label: "Rename",
            icon: Pencil,
            inline: true,
            run: () => setRenaming(true),
          },
        ]
      : []),
    ...(canDestroy
      ? [
          {
            key: "delete",
            label: "Delete key",
            icon: Trash,
            inline: true,
            danger: true,
            run: remove,
          },
        ]
      : []),
    ...(canWrite
      ? [{ key: "duplicate", label: "Duplicate…", icon: Layers, run: () => setDuplicating(true) }]
      : []),
    {
      key: "copy",
      label: "Copy name",
      icon: Copy,
      run: () => void copyText(label, "Key name copied"),
    },
    ...(read?.type === "string" && engine.can("valueDownload")
      ? [
          {
            key: "download",
            label: "Download value",
            icon: Download,
            run: () => window.open(redisRawUrl(target, name), "_blank", "noopener"),
          },
        ]
      : []),
  ]

  if (gone) {
    return (
      <Blank leading={leading}>
        <EmptyState
          mark={<EngineMark engine={engine} />}
          className="border-0"
          title="This key is gone"
          description={
            <>
              <span className="font-mono">{label}</span> is not in db{db} any more: it expired, or
              something removed it.
            </>
          }
          action={
            <Button size="sm" variant="outline" onClick={onClose}>
              Back to the keys
            </Button>
          }
        />
      </Blank>
    )
  }
  if (meta.error && !read) {
    return (
      <Blank leading={leading}>
        <ReadError error={meta.error} onRetry={meta.refresh} className="m-4" />
      </Blank>
    )
  }

  const editor = read && {
    redis,
    name,
    meta: read,
    epoch,
    onChanged: meta.refresh,
    confirm,
  }
  const kind = read ? kindOf(read.type).id : undefined

  return (
    <div data-slot="redis-key" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="shrink-0 space-y-2.5 border-b border-hairline px-3 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          {leading}
          {read ? (
            <KindMark type={read.type} className="size-4" />
          ) : (
            <Skeleton className="size-4" />
          )}
          <h2 title={label} className="min-w-0 truncate font-mono text-sm leading-6 font-medium">
            {label || <span className="text-muted-foreground">(empty name)</span>}
          </h2>
          {read && <KindTag type={read.type} />}
          {isBinary(name) && <Tag>binary name</Tag>}
          <span className="min-w-0 flex-1" />
          <VerbActions verbs={verbs} menuLabel={`More actions for ${label}`} />
        </div>
        {read ? (
          <MetricStrip key="facts" className="animate-rise">
            <Metric
              label="Expires"
              value={
                <TtlFact
                  target={target}
                  name={name}
                  pttl={read.pttl}
                  readAt={read.readAt}
                  editable={canWrite}
                  onChanged={meta.refresh}
                />
              }
            />
            {sizeOf(read.type, read.length) && (
              <Metric
                label="Holds"
                value={read.type === "string" ? bytes(read.length) : sizeOf(read.type, read.length)}
              />
            )}
            <Fact
              label="Memory"
              fact="memory"
              meta={read}
              value={read.memory === undefined ? undefined : bytes(read.memory)}
            />
            <Fact label="Encoding" fact="encoding" meta={read} value={read.encoding} mono />
            {read.frequency !== undefined ? (
              <Metric label="Access frequency" value={read.frequency.toLocaleString()} />
            ) : (
              <Fact
                label="Idle when opened"
                fact="idleSeconds"
                meta={
                  echoed
                    ? {
                        ...read,
                        idleSeconds: undefined,
                        unavailable: {
                          idleSeconds:
                            "This page had read the key itself just before opening it — the key list reads each key it shows — and a read resets the server's idle clock.",
                        },
                      }
                    : (opened ?? read)
                }
                value={
                  echoed || opened?.idleSeconds === undefined
                    ? undefined
                    : duration(opened.idleSeconds)
                }
                tip="How long nothing had read or written the key when this page opened it."
              />
            )}
          </MetricStrip>
        ) : (
          <div className="flex gap-6" aria-hidden>
            {[16, 14, 14, 20].map((width, i) => (
              <div key={i} className="space-y-1.5">
                <Skeleton className="h-2.5 w-12" />
                <Skeleton className="h-4" style={{ width: `${width * 4}px` }} />
              </div>
            ))}
          </div>
        )}
      </div>

      {meta.error && read && (
        <p
          role="status"
          className="shrink-0 border-b border-hairline px-3 py-1.5 text-hint text-warning"
        >
          The key could not be read again just now: {meta.error.message}
        </p>
      )}

      {!editor ? (
        <div className="space-y-2 p-3" aria-hidden>
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-5" style={{ width: `${88 - ((i * 13) % 40)}%` }} />
          ))}
        </div>
      ) : kind === "string" ? (
        <StringEditor {...editor} />
      ) : kind === "hash" ? (
        <HashEditor {...editor} />
      ) : kind === "list" ? (
        <ListEditor {...editor} />
      ) : kind === "set" ? (
        <SetEditor {...editor} />
      ) : kind === "zset" ? (
        <ZsetEditor {...editor} />
      ) : kind === "stream" ? (
        <StreamEditor {...editor} />
      ) : kind === "json" ? (
        <JsonEditor {...editor} />
      ) : (
        <EmptyState
          mark={<EngineMark engine={engine} />}
          className="min-h-0 flex-1 border-0"
          title={`${read.type} is a module's own type`}
          description="The dashboard has no reader for it. Its module's commands read it from the Console."
          action={
            <Button size="sm" variant="outline" onClick={() => redis.goto("query")}>
              Open the Console
            </Button>
          }
        />
      )}

      {canWrite && renaming && (
        <RenameDialog
          redis={redis}
          name={name}
          type={read?.type}
          onOpenChange={setRenaming}
          onRenamed={(to) => {
            onRail()
            onOpen(to)
          }}
        />
      )}
      {canWrite && duplicating && (
        <DuplicateDialog
          redis={redis}
          name={name}
          type={read?.type}
          onOpenChange={setDuplicating}
          onCopied={(to, toDb) => {
            onRail()
            if (toDb === undefined || toDb === db) onOpen(to)
          }}
        />
      )}
    </div>
  )
}

/** The pane with nothing of a key to show: the rail's toggle, and what stands in the key's place. */
function Blank({ leading, children }: { leading?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {leading && <div className="flex h-9 shrink-0 items-center px-2">{leading}</div>}
      <div className="flex min-h-0 flex-1 flex-col justify-center">{children}</div>
    </div>
  )
}

/** A fact the server may not have: its value, or a dash with the server's reason behind it. */
function Fact({
  label,
  fact,
  meta,
  value,
  mono,
  tip,
}: {
  label: string
  fact: RedisMetaFact
  meta: RedisKeyMeta
  value: string | undefined
  mono?: boolean
  /** What the figure is a reading of, where its name does not say it all. */
  tip?: string
}) {
  const why = meta.unavailable?.[fact]
  if (value === undefined && !why) return null
  return (
    <Metric
      label={label}
      value={
        value !== undefined ? (
          tip ? (
            <span className="flex items-center gap-1">
              {value}
              <InfoTip label={`About ${label.toLowerCase()}`}>{tip}</InfoTip>
            </span>
          ) : (
            <span className={mono ? "font-mono text-xs" : undefined}>{value}</span>
          )
        ) : (
          <span className="flex items-center gap-1 text-muted-foreground">
            —<InfoTip label={`Why ${label.toLowerCase()} is not known`}>{why}</InfoTip>
          </span>
        )
      }
    />
  )
}
