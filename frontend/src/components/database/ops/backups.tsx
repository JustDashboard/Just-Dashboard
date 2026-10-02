"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Archive,
  CloudUpload,
  Copy,
  Download,
  RotateCounterClockwise,
  Trash,
} from "@/components/icons"
import { del, downloadUrl, get, post } from "@/lib/api"
import { describeCron } from "@/lib/cron"
import { bytes, clockMinute, duration, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupJob, DbConnection, Job } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormNote } from "@/components/form"
import { OutcomeStrip } from "@/components/outcome-strip"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingPanel } from "@/components/state"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
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
import { VerbActions, type Verb } from "@/components/verbs"
import { sectionHref } from "@/components/database/engine"
import { CouldNotRead, NotUpdating, staleOf } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { EngineMark, ProtectedTag, SectionFrame } from "@/components/database/kit"
import { CopyDatabase } from "@/components/database/ops/backups-copy"
import {
  AfterAction,
  TransferProgress,
  useTransferJob,
} from "@/components/database/ops/backups-job"
import type { WatchedJob } from "@/components/database/ops/backups-job"
import {
  backupAge,
  holdsWords,
  keptBytes,
  lastBackup,
  originWord,
  outcomesLabel,
  scheduledDump,
  tookWords,
  transferKind,
  transferOutcomes,
  transferPrefix,
} from "@/components/database/ops/backups-model"
import { DumpFacts, RestoreDump } from "@/components/database/ops/backups-restore"
import { TakeDump } from "@/components/database/ops/backups-take"
import type { DbBackupFile, DbBackups } from "@/components/database/ops/backups-types"
import { UploadDump } from "@/components/database/ops/backups-upload"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

/** The width of the list itself from which a dump is a row of a table rather than a block of lines. */
const TABLE_FROM = 760

/** The width from which the page's three commands stand beside the panel's title. */
const COMMANDS_FROM = 600

type Asking =
  { kind: "take" } | { kind: "upload" } | { kind: "copy" } | { kind: "restore"; file: DbBackupFile }

/**
 * A database's dumps: when it was last backed up and what is kept, the dump,
 * restore or copy running now with its own output, and every dump with the
 * three things done to one — download it, restore it, delete it.
 *
 * The page reads files and jobs, never the database: it is the same page for
 * a server that is stopped, minus the command that needs it to answer. A
 * transfer is a job on the server, so the page attaches to it by id (the
 * address carries `?job=`) and a reload or a second window shows the same
 * progress.
 *
 * Reading register. The four figures are tiles; the fourth is a way to the
 * server's scheduled backup jobs, which is where a schedule for these dumps
 * is kept.
 */
export function Backups() {
  const { id, conn, engine, readOnly, status, select, param, href } = useDatabase()
  const { refresh: refreshConnections } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [asking, setAsking] = useState<Asking | null>(null)
  const [busy, setBusy] = useState(false)
  const [connecting, setConnecting] = useState(false)
  const [frame, width] = useColumnWidth<HTMLDivElement>()

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
      const what = kind === "restore" ? "Restore" : kind === "copy" ? "Copy" : "Backup"
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
  const transfer = useTransferJob(watchedId, live, onEnd)

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
    select({ job: null })
  }

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

  const outcomes = useMemo(
    () => transferOutcomes(files, jobs.data ?? [], running, relativeTime),
    [files, jobs.data, running],
  )

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

  // A restore or a copy that made a database: the way into it. A server
  // that numbers its databases names several when a dump went back to each;
  // only one new number is a place to go.
  const loaded = transfer.current?.result?.database
  const made =
    transfer.current?.job.status === "succeeded" &&
    transferKind(transfer.current.job) !== "backup" &&
    loaded &&
    loaded !== conn.database &&
    (!data?.options.databases || /^\d+$/.test(loaded))
      ? loaded
      : undefined
  const connect = async (database: string) => {
    setConnecting(true)
    try {
      const saved = await post<DbConnection>(`/databases/${id}/server/databases/connect`, {
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
      <AfterAction onClick={() => router.push(href("data", { db: made }))}>
        Browse db {made}
      </AfterAction>
    ) : engine.can("serverDatabaseConnect") && can("system.admin") ? (
      <AfterAction pending={connecting} onClick={() => void connect(made)}>
        Connect {made}
      </AfterAction>
    ) : undefined
  ) : undefined

  const roomy = width === 0 || width >= COMMANDS_FROM
  const commands = (
    <>
      {mayUpload && (
        <Button size="sm" variant="outline" onClick={() => setAsking({ kind: "upload" })}>
          <CloudUpload />
          Upload a dump
        </Button>
      )}
      {mayCopy && (
        <Button
          size="sm"
          variant="outline"
          disabled={!idle || !answering}
          onClick={() => setAsking({ kind: "copy" })}
        >
          <Copy />
          Copy database
        </Button>
      )}
      {mayDump && (
        <Button
          size="sm"
          disabled={!idle || !answering || !data}
          onClick={() => setAsking({ kind: "take" })}
        >
          <Archive />
          Back up now
        </Button>
      )}
    </>
  )

  return (
    <SectionFrame section="backups">
      <BackupReadings
        files={data ? files : undefined}
        failed={!data && Boolean(backups.error)}
        outcomes={outcomes}
        schedule={scheduledDump(scheduled.data ?? [], id)}
        scheduleRead={scheduled.data !== undefined || Boolean(scheduled.error)}
      />

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
          The server no longer keeps the output of that operation: it remembers its last fifty.{" "}
          <button type="button" className="rounded-sm underline focus-ring" onClick={dismiss}>
            Dismiss
          </button>
        </FormNote>
      )}

      <Panel plain aria-label="Dumps" ref={frame}>
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
              {roomy && commands}
            </>
          }
        />
        {/* Three commands do not fit beside the title on a phone: they take a row of their own. */}
        {!roomy && <div className="flex flex-wrap gap-2 pt-3">{commands}</div>}
        {!data ? (
          backups.error ? (
            <div className="pt-4">
              <CouldNotRead what="the dumps" error={backups.error} onRetry={backups.refresh} />
            </div>
          ) : (
            <LoadingPanel plain rows={4} />
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
            <RecentTransfers
              jobs={jobs.data ?? []}
              current={watchedId}
              onOpen={(jobId) => select({ job: jobId })}
            />
            {files.length === 0 ? (
              <EmptyState
                mark={<EngineMark engine={engine} size="md" />}
                title={`No dump of ${conn.name} is kept here`}
                description="A dump is one file on this server that this page can restore from. Take one now, or upload one made somewhere else."
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
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                <Table className="table-fixed">
                  <colgroup>
                    <col className="w-32" />
                    <col />
                    <col className="w-44" />
                    <col className="w-32" />
                    <col className="w-20" />
                    <col className="w-24" />
                    <col className="w-28" />
                  </colgroup>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>Taken</TableHead>
                      <TableHead className="px-2">Dump</TableHead>
                      <TableHead className="px-2">Holds</TableHead>
                      <TableHead className="px-2">Written by</TableHead>
                      <TableHead className="px-2 text-right">Size</TableHead>
                      <TableHead className="px-2 text-right">Took</TableHead>
                      <TableHead className="px-2">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {files.map((file) => (
                      <TableRow key={file.file} className="group">
                        <TableCell className="py-2 align-top">
                          <span className="block" title={timestamp(file.takenAt)}>
                            {relativeTime(file.takenAt)}
                          </span>
                          {file.by && (
                            <span className="block truncate text-hint text-muted-foreground">
                              by {file.by}
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="px-2 py-2 align-top">
                          <DumpName file={file} />
                        </TableCell>
                        <TableCell className="px-2 py-2 align-top whitespace-normal">
                          <DumpHolds file={file} />
                        </TableCell>
                        <TableCell className="px-2 py-2 align-top">
                          <span className="block truncate">{file.tool ?? file.format}</span>
                          {file.toolVersion && (
                            <span className="numeric block truncate text-hint text-muted-foreground">
                              {file.toolVersion}
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right align-top">
                          {bytes(file.size)}
                        </TableCell>
                        <TableCell className="numeric px-2 py-2 text-right align-top text-muted-foreground">
                          {tookWords(file.durationMs) || "—"}
                        </TableCell>
                        <TableCell className="px-2 py-1 align-top">
                          <VerbActions dim verbs={verbsFor(file)} className="justify-end" />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </PanelBody>
            ) : (
              <ul className="-mx-4 divide-y divide-hairline border-y border-hairline">
                {files.map((file) => (
                  <li key={file.file} className="group space-y-1.5 px-4 py-2.5 text-xs">
                    <div className="flex min-w-0 items-start gap-2">
                      <div className="min-w-0 flex-1">
                        <DumpName file={file} />
                      </div>
                      <VerbActions dim verbs={verbsFor(file)} />
                    </div>
                    <p className="numeric flex flex-wrap gap-x-3 gap-y-0.5 text-muted-foreground">
                      <span title={timestamp(file.takenAt)}>{relativeTime(file.takenAt)}</span>
                      <span>{bytes(file.size)}</span>
                      <span>{file.tool ?? file.format}</span>
                      {tookWords(file.durationMs) && <span>{tookWords(file.durationMs)}</span>}
                    </p>
                    <DumpHolds file={file} />
                  </li>
                ))}
              </ul>
            )}
            <FormNote className="wrap-anywhere">
              Kept on this server in <span className="font-mono">{data.dir}</span>
            </FormNote>
          </div>
        )}
      </Panel>

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
        const word = kind === "restore" ? "Restore" : kind === "copy" ? "Copy" : "Dump"
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

/** A dump's own name, where it came from when that is worth saying, and its note. */
function DumpName({ file }: { file: DbBackupFile }) {
  const origin = originWord(file.origin)
  return (
    <>
      <span className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 truncate font-mono" title={file.file}>
          {file.file}
        </span>
        {origin && <Tag>{origin}</Tag>}
      </span>
      {file.note && (
        <span className="block truncate text-hint text-muted-foreground" title={file.note}>
          {file.note}
        </span>
      )}
    </>
  )
}

/** What a dump holds: what it was asked for, and what the tool said it wrote. */
function DumpHolds({ file }: { file: DbBackupFile }) {
  const { engine } = useDatabase()
  const contents = holdsWords(file, engine.nouns)
  if (!contents && !file.summary) {
    return <span className="text-muted-foreground">Not described</span>
  }
  return (
    <>
      {contents && <span className="block">{contents}</span>}
      {file.summary && (
        <span className="block text-hint text-muted-foreground">{file.summary}</span>
      )}
    </>
  )
}

/**
 * The four readings: when it was last backed up, how large that dump is, how
 * much is kept with how the last operations ended, and whether anything takes
 * these dumps on a schedule.
 */
function BackupReadings({
  files,
  failed,
  outcomes,
  schedule,
  scheduleRead,
}: {
  /** Undefined until the list has been read. */
  files: DbBackupFile[] | undefined
  /** The list could not be read: the figures are not known, which is not "none". */
  failed: boolean
  outcomes: ReturnType<typeof transferOutcomes>
  schedule: BackupJob | undefined
  scheduleRead: boolean
}) {
  const { id, conn } = useDatabase()
  const waiting = <Skeleton className="my-1 h-6 w-24" />
  const unread = <span className="text-muted-foreground">—</span>
  const last = files ? lastBackup(files) : undefined
  // The time the ages are read against: held, and moved on twice a minute,
  // so the tiles say the same thing however often the page draws.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const age = backupAge(last, now)
  const figure = (value: React.ReactNode) => (files ? value : failed ? unread : waiting)
  const next = schedule?.enabled && schedule.nextRun ? Date.parse(schedule.nextRun) : NaN
  return (
    <StatGrid columns={4} dense aria-label="Backups at a glance" role="group">
      <StatTile
        label="Last backup"
        tone={files && age.stale ? "warning" : "default"}
        value={figure(
          <span key="value" className="animate-rise">
            {last ? relativeTime(last.takenAt) : "Never"}
          </span>,
        )}
        hint={
          !files
            ? failed
              ? "could not be read"
              : undefined
            : last
              ? `${timestamp(last.takenAt)}${last.by ? ` · by ${last.by}` : ""}`
              : `no dump of ${conn.name} has been taken here`
        }
      />
      <StatTile
        label="Size"
        value={figure(
          <span key="value" className="animate-rise">
            {last ? bytes(last.size) : "—"}
          </span>,
        )}
        hint={
          last
            ? [last.tool ?? last.format, tookWords(last.durationMs)].filter(Boolean).join(" · ")
            : files
              ? "of the newest dump"
              : undefined
        }
      />
      <StatTile
        label="Kept"
        value={figure(
          <span key="value" className="animate-rise">
            {files?.length ?? 0}
          </span>,
        )}
        trailing={files && files.length > 0 ? bytes(keptBytes(files)) : undefined}
        hint={
          outcomes.length > 0 ? (
            <span className="flex items-center gap-2">
              <OutcomeStrip items={outcomes} label={outcomesLabel(outcomes)} />
              <span className="truncate">
                {outcomes.some((one) => one.tone === "danger")
                  ? `${plural(outcomes.filter((one) => one.tone === "danger").length, "failure")} among the last`
                  : "how the last ended"}
              </span>
            </span>
          ) : files ? (
            "nothing yet"
          ) : undefined
        }
      />
      <StatLink
        href={`/backups?database=${id}`}
        label={
          schedule
            ? `Open the backup job ${schedule.name}`
            : `Add ${conn.name} to a scheduled backup job`
        }
      >
        <StatTile
          className="h-full transition-colors group-hover:bg-row-hover"
          label="Schedule"
          value={
            !scheduleRead ? (
              waiting
            ) : (
              <span key="value" className="animate-rise">
                {schedule
                  ? schedule.enabled
                    ? Number.isFinite(next)
                      ? `in ${duration(Math.max(0, (next - now) / 1000))}`
                      : "On"
                    : "Paused"
                  : "None"}
              </span>
            )
          }
          hint={
            !scheduleRead
              ? undefined
              : schedule
                ? `${describeCron(schedule.schedule)} · ${schedule.name}`
                : "no backup job takes these dumps"
          }
        />
      </StatLink>
    </StatGrid>
  )
}
