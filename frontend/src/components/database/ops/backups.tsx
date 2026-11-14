"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { sectionHref, type Engine } from "@/components/database/engine"
import { CouldNotRead, NotUpdating, staleOf } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { EngineMark, ProtectedTag, SectionFrame } from "@/components/database/kit"
import { CopyDatabase } from "@/components/database/ops/backups-copy"
import type { WatchedJob } from "@/components/database/ops/backups-job"
import {
  AfterAction,
  TransferProgress,
  useTransferJob,
} from "@/components/database/ops/backups-job"
import {
  holdsWords,
  nameParts,
  originWord,
  scheduledDump,
  summaryWords,
  tookWords,
  transferKind,
  transferPrefix,
  transferWord,
} from "@/components/database/ops/backups-model"
import { DumpsPicture } from "@/components/database/ops/backups-picture"
import { DumpFacts, RestoreDump } from "@/components/database/ops/backups-restore"
import { PointInTimeRecovery } from "@/components/database/ops/pitr"
import { TakeDump } from "@/components/database/ops/backups-take"
import type { DbBackupFile, DbBackups } from "@/components/database/ops/backups-types"
import { UploadDump } from "@/components/database/ops/backups-upload"
import { useAddressStep } from "@/components/database/ops/settings-step"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormNote } from "@/components/form"
import {
  Archive,
  CloudUpload,
  Copy,
  Download,
  RotateCounterClockwise,
  Trash,
} from "@/components/icons"
import { Panel, PanelHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { VerbActions, VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, downloadUrl, get, post } from "@/lib/api"
import { bytes, clockMinute, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupJob, DbConnection, Job } from "@/lib/types"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"

/** The width of the list itself from which a dump is a row of a table rather than a block of lines. */
const TABLE_FROM = 600

/** The key of the address that names the transfer on screen. */
const JOB_KEY = ["job"] as const

type Asking =
  { kind: "take" } | { kind: "upload" } | { kind: "copy" } | { kind: "restore"; file: DbBackupFile }

/**
 * A database's dumps: when it was last backed up and what is kept, the dump,
 * restore or copy running now with its own output, and every dump on one
 * line with the three things done to one — download it, restore it, delete
 * it. A dump's name opens the rest of what is known about it.
 *
 * The page reads files and jobs, never the database: it is the same page for
 * a server that is stopped, minus the command that needs it to answer. A
 * transfer is a job on the server, so the page attaches to it by id (the
 * address carries `?job=`) and a reload or a second window shows the same
 * progress.
 *
 * The schedule action leads to the server's backup jobs, where these dumps
 * are scheduled.
 */
export function Backups() {
  const { id, conn, engine, readOnly, status, select, param } = useDatabase()
  const { refresh: refreshConnections } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [asking, setAsking] = useState<Asking | null>(null)
  const [busy, setBusy] = useState(false)
  const [connecting, setConnecting] = useState(false)
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const step = useAddressStep("backups", JOB_KEY)
  const command = useRef<HTMLButtonElement>(null)
  // The dialogs of this page are opened by state: this hands the keyboard
  // back to the button or the row that opened one.
  useFocusReturn()

  // Every thirty seconds, and every two while a transfer runs: a dump in
  // flight is not in the list, and the list is where its end shows.
  const backups = usePoll(
    async (signal) => {
      const answer = await read<DbBackups>(
        `/databases/${id}/backups`,
        (listed) => Array.isArray(listed.files),
        undefined,
        signal,
      )
      setBusy(Boolean(answer.job))
      return answer
    },
    busy ? 2_000 : 30_000,
    [id],
  )
  // What the server still remembers of this connection's transfers: the ones
  // that failed leave no file, and are only here.
  const prefix = transferPrefix(id)
  const jobs = usePoll(
    async (signal) =>
      (await get<Job[]>("/jobs/", undefined, signal)).filter((job) => job.kind.startsWith(prefix)),
    30_000,
    [id],
  )
  const scheduled = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 120_000, [
    id,
  ])

  // The job on screen: the one the address names, else the one running.
  const named = param("job")
  const running = backups.data?.job
  const [began, setBegan] = useState<string[]>([])
  const [dismissed, setDismissed] = useState<string[]>([])
  const watchedId = named || (running && !dismissed.includes(running.id) ? running.id : "") || ""
  const live = began.includes(watchedId) || running?.id === watchedId
  // A job found running is put in the address, so that it stays on screen
  // when it ends and the list stops carrying it.
  useEffect(() => {
    if (!named && watchedId) select({ job: watchedId })
  }, [named, watchedId, select])

  const refreshBackups = backups.refresh
  const refreshJobs = jobs.refresh
  const check = status.refresh
  const name = conn.name
  const onEnd = useCallback(
    ({ job, result }: WatchedJob) => {
      refreshBackups()
      refreshJobs()
      check()
      const kind = transferKind(job)
      const what = kind === "backup" || !kind ? "Backup" : transferWord(kind)
      if (job.status === "succeeded") {
        notify.success(`${what} of ${name} finished`, {
          description: result?.summary ?? result?.file ?? result?.database,
        })
      } else if (job.status === "failed") {
        notify.error(`${what} of ${name} failed`, job.error ?? "The job did not finish.")
      } else {
        notify.info(`${what} of ${name} was stopped`)
      }
    },
    [refreshBackups, refreshJobs, check, name],
  )
  const transfer = useTransferJob(watchedId, prefix, live, onEnd)

  const watch = (job: Job | string) => {
    const jobId = typeof job === "string" ? job : job.id
    setBegan((held) => [...held, jobId])
    setAsking(null)
    setBusy(true)
    select({ job: jobId })
    refreshBackups()
  }
  const dismiss = () => {
    if (watchedId) setDismissed((held) => [...held, watchedId])
    step.close()
    // The console goes, and its Dismiss with it: the keyboard is put on the
    // page's command, or on the list where the role has none.
    requestAnimationFrame(() => {
      const next = command.current && !command.current.disabled ? command.current : null
      ;(next ?? document.querySelector<HTMLElement>("[data-dumps]"))?.focus({
        preventScroll: true,
      })
    })
  }
  // A transfer opened from the recent ones is a step the reader took; with
  // one already on screen it takes that one's place.
  const openJob = (jobId: string) => (named ? select({ job: jobId }) : step.open({ job: jobId }))

  const data = backups.data
  const files = useMemo(() => data?.files ?? [], [data])
  const answering = status.state === "running"
  const mayDump = can("service.control")
  const mayRestore = can("destructive") && !readOnly
  const mayDelete = can("system.admin") && can("destructive")
  const mayCopy =
    can("system.admin") && !readOnly && engine.can("copy") && Boolean(data?.options.newDatabase)
  const mayUpload = mayDump && engine.can("dumpUpload")
  const idle = !running

  const download = (file: DbBackupFile) => {
    const link = document.createElement("a")
    link.href = downloadUrl(`/databases/${id}/backup/download`, { file: file.file })
    link.download = file.file
    link.click()
  }

  const remove = (file: DbBackupFile) =>
    confirm({
      title: "Delete dump",
      confirmLabel: "Delete",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{file.file}</span>,
        facts: <DumpFacts file={file} />,
      },
      description: (
        <p>
          Deletes the file from this server. A copy that was downloaded is not affected; nothing
          else keeps one.
        </p>
      ),
      action: async () => {
        await del(`/databases/${id}/backups`, { body: { file: file.file } })
        notify.success(`Deleted ${file.file}`)
        refreshBackups()
        check()
        return "reported"
      },
    })

  const verbsFor = (file: DbBackupFile): Verb[] => [
    ...(mayDump
      ? [
          {
            key: "download",
            label: `Download ${file.file}`,
            icon: Download,
            inline: true,
            run: () => download(file),
          },
        ]
      : []),
    ...(mayRestore
      ? [
          {
            key: "restore",
            label: `Restore ${file.file}`,
            icon: RotateCounterClockwise,
            inline: true,
            disabled: !idle || !answering,
            run: () => setAsking({ kind: "restore", file }),
          },
        ]
      : []),
    ...(mayDelete
      ? [
          {
            key: "delete",
            label: `Delete ${file.file}`,
            icon: Trash,
            inline: true,
            danger: true,
            run: () => remove(file),
          },
        ]
      : []),
  ]

  // A restore or a copy that made a database: the way into it. A server that
  // numbers its databases names several when a dump went back to each; only
  // one new number is a place to go. A copy to another server made it there,
  // and the way in is through that server's connection.
  const result = transfer.current?.result
  const loaded = result?.database
  const elsewhere = result?.connection && result.connection !== id ? result : undefined
  const made =
    transfer.current?.job.status === "succeeded" &&
    transferKind(transfer.current.job) !== "backup" &&
    loaded &&
    (elsewhere || loaded !== conn.database) &&
    (!data?.options.databases || /^\d+$/.test(loaded))
      ? loaded
      : undefined
  const via = elsewhere?.connection ?? id
  const connect = async (database: string) => {
    setConnecting(true)
    try {
      const saved = await post<DbConnection>(`/databases/${via}/server/databases/connect`, {
        database,
      })
      notify.success(`Connected ${saved.name}`)
      refreshConnections()
      router.push(sectionHref(saved.id))
    } catch (err) {
      notify.error(`Could not connect to ${database}`, err)
    } finally {
      setConnecting(false)
    }
  }
  const after = made ? (
    data?.options.databases ? (
      <AfterAction onClick={() => router.push(sectionHref(via, "data", { db: made }))}>
        Browse db {made}
        {elsewhere?.connectionName ? ` on ${elsewhere.connectionName}` : ""}
      </AfterAction>
    ) : engine.can("serverDatabaseConnect") && can("system.admin") ? (
      <AfterAction pending={connecting} onClick={() => void connect(made)}>
        Connect {made}
        {elsewhere?.connectionName ? ` on ${elsewhere.connectionName}` : ""}
      </AfterAction>
    ) : undefined
  ) : undefined

  // One command on the title line; the two that are not pressed daily are
  // words in a menu beside it, which is also what a phone has room for.
  const more: Verb[] = [
    ...(mayUpload
      ? [
          {
            key: "upload",
            label: "Upload a dump",
            icon: CloudUpload,
            run: () => setAsking({ kind: "upload" }),
          },
        ]
      : []),
    ...(mayCopy
      ? [
          {
            key: "copy",
            label: "Copy database",
            icon: Copy,
            disabled: !idle || !answering,
            run: () => setAsking({ kind: "copy" }),
          },
        ]
      : []),
  ]

  const schedule = scheduledDump(scheduled.data ?? [], id)
  return (
    <SectionFrame section="backups">
      <DumpsPicture data={data} schedule={schedule} scheduleRead={Boolean(scheduled.data)} />

      {transfer.current && (
        <TransferProgress
          watched={transfer.current}
          canStop={mayDump}
          onStop={() => void transfer.stop()}
          onDismiss={dismiss}
          after={after}
        />
      )}
      {transfer.missing && named && (
        <FormNote role="status">
          No dump, restore or copy of {conn.name} is kept under that id: the server remembers its
          last fifty operations, and shows a database only its own.{" "}
          <button type="button" className="rounded-sm underline focus-ring" onClick={dismiss}>
            Dismiss
          </button>
        </FormNote>
      )}

      <Panel
        plain
        aria-label="Dumps"
        ref={frame}
        data-dumps=""
        tabIndex={-1}
        className="outline-none"
      >
        <PanelHeader
          title={
            <span className="flex items-baseline gap-2">
              Dumps
              {data && (
                <span className="numeric text-hint font-normal text-muted-foreground">
                  {files.length}
                </span>
              )}
            </span>
          }
          actions={
            <>
              {staleOf(backups) && <NotUpdating error={staleOf(backups)!} />}
              {readOnly && <ProtectedTag />}
              {mayDump && (
                <Button
                  ref={command}
                  size="sm"
                  disabled={!idle || !answering || !data}
                  onClick={() => setAsking({ kind: "take" })}
                >
                  <Archive />
                  Back up now
                </Button>
              )}
              {more.length > 0 && <VerbMenu verbs={more} label="More backup actions" />}
            </>
          }
        />
        {!data ? (
          backups.error ? (
            <div className="pt-4">
              <CouldNotRead what="the dumps" error={backups.error} onRetry={backups.refresh} />
            </div>
          ) : (
            <DumpsSilhouette table={width === 0 || width >= TABLE_FROM} />
          )
        ) : (
          <div className="animate-rise space-y-3 pt-3">
            {!answering && mayDump && (
              <FormNote>
                The server is not answering, so no dump can be taken or restored now. The dumps
                already kept can be downloaded.
              </FormNote>
            )}
            {readOnly && (
              <FormNote>
                This connection is protected: a dump can be taken and kept, and nothing is restored
                or copied from this page.
              </FormNote>
            )}
            <RecentTransfers jobs={jobs.data ?? []} current={watchedId} onOpen={openJob} />
            {files.length === 0 ? (
              <EmptyState
                mark={<EngineMark engine={engine} size="md" />}
                title={`No dump of ${conn.name} is kept here`}
                description="Take one now, or upload one made somewhere else."
                action={
                  mayDump && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!idle || !answering}
                      onClick={() => setAsking({ kind: "take" })}
                    >
                      <Archive />
                      Back up now
                    </Button>
                  )
                }
              />
            ) : width >= TABLE_FROM ? (
              // Framed, because it is a table (§2), and one line to a dump:
              // who took it, the tool that wrote it and how long it took are
              // behind its name.
              <Panel>
                <Table className="table-fixed">
                  <colgroup>
                    <col className="w-28" />
                    <col />
                    <col className="w-[24%]" />
                    <col className="w-24" />
                    <col className="w-28" />
                  </colgroup>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Taken</TableHead>
                      <TableHead className="px-2">Dump</TableHead>
                      <TableHead className="px-2">Holds</TableHead>
                      <TableHead className="px-2 text-right">Size</TableHead>
                      <TableHead className="px-2">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {files.map((file) => (
                      <TableRow key={file.file} className="group h-11">
                        <TableCell className="truncate py-0" title={timestamp(file.takenAt)}>
                          {relativeTime(file.takenAt)}
                        </TableCell>
                        <TableCell className="px-2 py-0">
                          <DumpName file={file} />
                        </TableCell>
                        <TableCell className="px-2 py-0">
                          <DumpHolds file={file} />
                        </TableCell>
                        <TableCell className="numeric px-2 py-0 text-right">
                          {bytes(file.size)}
                        </TableCell>
                        <TableCell className="px-2 py-0">
                          <VerbActions dim verbs={verbsFor(file)} className="justify-end" />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Panel>
            ) : (
              // Too narrow for columns: a dump is a card of two lines, its
              // name with its commands over when, how big and what it holds.
              // Every card is those two lines, so the list is one height a row.
              <ul className="space-y-2">
                {files.map((file) => (
                  <li
                    key={file.file}
                    data-slot="dump-card"
                    className="group min-w-0 rounded-xl border bg-choice-surface px-3 py-2 text-xs"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <div className="min-w-0 flex-1">
                        <DumpName file={file} bare />
                      </div>
                      <VerbActions dim verbs={verbsFor(file)} />
                    </div>
                    <p className="numeric truncate text-hint text-muted-foreground">
                      <span title={timestamp(file.takenAt)}>{relativeTime(file.takenAt)}</span>
                      {" · "}
                      {bytes(file.size)}
                      {holdsLine(file, engine) && (
                        <>
                          {" · "}
                          <span className="text-foreground">{holdsLine(file, engine)}</span>
                        </>
                      )}
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </Panel>

      {engine.can("pitr") && <PointInTimeRecovery jobs={scheduled.data ?? []} />}

      {asking?.kind === "take" && data && (
        <TakeDump
          options={data.options}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "upload" && (
        <UploadDump
          taken={files.map((file) => file.file)}
          onUploaded={(file) => {
            setAsking(null)
            notify.success(`Uploaded ${file.file}`, { description: `${bytes(file.size)} kept.` })
            refreshBackups()
          }}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "copy" && data && (
        <CopyDatabase
          options={data.options}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "restore" && data && (
        <RestoreDump
          file={asking.file}
          options={data.options}
          confirm={confirm}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}

/**
 * The list before it has come, in the shape it will have: the framed table's
 * head and four lines, or four cards where there is no room for columns. It
 * was rows on hairlines, a different height from the table that took their
 * place, so everything under the dumps moved when they landed.
 */
function DumpsSilhouette({ table }: { table: boolean }) {
  const rows = [0, 1, 2, 3]
  return (
    <div role="status" aria-label="Reading the dumps" className="pt-3">
      {table ? (
        <Panel aria-hidden>
          <div className="flex h-10 items-center border-b border-hairline px-4">
            <Skeleton className="h-2.5 w-12" />
          </div>
          {rows.map((row) => (
            <div
              key={row}
              className="flex h-11 items-center gap-8 border-b border-hairline px-4 last:border-0"
            >
              <Skeleton className="h-3 w-16 shrink-0" />
              <Skeleton className={row % 2 ? "h-3 w-56" : "h-3 w-72"} />
              <Skeleton className="ml-auto h-3 w-14 shrink-0" />
            </div>
          ))}
        </Panel>
      ) : (
        <ul aria-hidden className="space-y-2">
          {rows.map((row) => (
            <li key={row} className="rounded-xl border bg-choice-surface px-3 py-2 text-xs">
              <div className="flex h-8 items-center">
                <Skeleton className={row % 2 ? "h-3 w-40" : "h-3 w-52"} />
              </div>
              <p className="text-hint">&nbsp;</p>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** How a finished transfer ended, as the mark its chip leads with. */
const ENDED: Record<Job["status"], { tone: DotTone; word: string }> = {
  running: { tone: "notice", word: "running" },
  succeeded: { tone: "running", word: "done" },
  failed: { tone: "danger", word: "failed" },
  cancelled: { tone: "stopped", word: "stopped" },
}

/**
 * The transfers the server still remembers, newest first, each a way back to
 * its output: a restore's last lines are worth reading after the toast has
 * gone, and a job outlives the console that showed it.
 */
function RecentTransfers({
  jobs,
  current,
  onOpen,
}: {
  jobs: Job[]
  /** The one on screen now. */
  current: string
  onOpen: (jobId: string) => void
}) {
  const shown = jobs.filter((job) => job.status !== "running").slice(0, 6)
  if (shown.length === 0) return null
  return (
    <div
      role="group"
      aria-label="Recent operations"
      className="flex flex-wrap items-center gap-1.5"
    >
      <span className="eyebrow">recent</span>
      {shown.map((job) => {
        const kind = transferKind(job)
        const word = transferWord(kind)
        return (
          <FilterChip
            key={job.id}
            selected={job.id === current}
            title={`${job.title} · ${ENDED[job.status].word}${job.startedBy ? ` · by ${job.startedBy}` : ""}`}
            onClick={() => onOpen(job.id)}
          >
            <StatusDot tone={ENDED[job.status].tone} />
            {word}
            <span className="numeric text-micro opacity-60">{clockMinute(job.startedAt)}</span>
            <span className="sr-only">, {ENDED[job.status].word}</span>
          </FilterChip>
        )
      })}
    </div>
  )
}

/** The tool that wrote a dump, with its version where the dump says it. */
function toolWords(file: DbBackupFile): string {
  return [file.tool ?? file.format, file.toolVersion].filter(Boolean).join(" ")
}

/** What a dump holds, as its words in a line: what it was asked for, and what the tool said it wrote. */
function holdsLine(file: DbBackupFile, engine: Engine): string {
  const numbered = engine.can("dumpDatabases")
  return [holdsWords(file, engine.nouns, numbered), summaryWords(file, numbered)]
    .filter(Boolean)
    .join(" · ")
}

/**
 * A dump's own name, where it came from when that is worth saying, and its
 * note beside it. A name too long for its column is cut in the middle: the
 * end of it — when it was taken, what kind of file it is — is what tells
 * dumps apart.
 *
 * The name is the button (§7): it opens what else is known about the dump —
 * the moment it was taken and by whom, the tool that wrote it and how long
 * that took, its note in full. Those were a second line under every cell of
 * the row.
 */
function DumpName({ file, bare }: { file: DbBackupFile; bare?: boolean }) {
  const { engine } = useDatabase()
  const origin = originWord(file.origin)
  const { head, tail } = nameParts(file.file)
  const took = tookWords(file.durationMs)
  const holds = holdsLine(file, engine)
  return (
    <span className="flex min-w-0 items-center gap-2">
      <Popover>
        <PopoverTrigger
          aria-label={`About ${file.file}`}
          title={file.file}
          className="flex min-w-0 rounded-sm font-mono underline-offset-2 focus-ring hover:underline"
        >
          <span className="min-w-0 truncate">{head}</span>
          {tail && <span className="shrink-0">{tail}</span>}
        </PopoverTrigger>
        <PopoverContent align="start" className="w-80 space-y-2 p-3 text-xs">
          <p className="font-mono break-all">{file.file}</p>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-hint">
            <dt className="text-muted-foreground">Taken</dt>
            <dd>
              {timestamp(file.takenAt)}
              {file.by && ` by ${file.by}`}
            </dd>
            <dt className="text-muted-foreground">Written by</dt>
            <dd>
              {toolWords(file)}
              {took && `, in ${took}`}
            </dd>
            {holds && (
              <>
                <dt className="text-muted-foreground">Holds</dt>
                <dd>{holds}</dd>
              </>
            )}
            {file.note && (
              <>
                <dt className="text-muted-foreground">Note</dt>
                <dd className="break-words">{file.note}</dd>
              </>
            )}
          </dl>
        </PopoverContent>
      </Popover>
      {origin && <Tag className="shrink-0">{origin}</Tag>}
      {!bare && file.note && (
        <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">{file.note}</span>
      )}
    </span>
  )
}

/** What a dump holds, on its line of the table. */
function DumpHolds({ file }: { file: DbBackupFile }) {
  const { engine } = useDatabase()
  const holds = holdsLine(file, engine)
  if (!holds) return <span className="block truncate text-muted-foreground">Not described</span>
  return (
    <span className="block truncate" title={holds}>
      {holds}
    </span>
  )
}
