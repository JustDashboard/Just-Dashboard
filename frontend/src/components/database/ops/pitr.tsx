"use client"

import { useCallback, useId, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useConfirm } from "@/components/confirm-dialog"
import { sectionHref } from "@/components/database/engine"
import {
  CouldNotRead,
  FactRow,
  FactRows,
  NotUpdating,
  staleOf,
} from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineMark } from "@/components/database/kit"
import type { WatchedJob } from "@/components/database/ops/backups-job"
import {
  AfterAction,
  TransferProgress,
  useTransferJob,
} from "@/components/database/ops/backups-job"
import {
  archiveHealth,
  keepsWords,
  locationWords,
  momentWords,
  pitrJobPrefix,
  RESTORE_POINT,
  RESTORE_POINT_RULE,
  targetWords,
  windowWords,
} from "@/components/database/ops/pitr-model"
import { RestorePitr } from "@/components/database/ops/pitr-restore"
import { SetupPitr } from "@/components/database/ops/pitr-setup"
import type {
  DbPitr,
  PitrArchive,
  PitrJobResult,
  PitrRestore,
  PitrRestorePoint,
  WalWindow,
} from "@/components/database/ops/pitr-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Disclosure, Field, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  ArrowLeftRight,
  ClockRewind,
  Database,
  Pencil,
  Pin,
  RotateCounterClockwise,
  StopCircle,
  Trash,
  Warning,
} from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ScheduleBuilder, scheduleExpression, scheduleFields } from "@/components/schedule-builder"
import { EmptyState, LoadingPanel, Notice } from "@/components/state"
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
import { VerbActions } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, del, errorMessage, post, put } from "@/lib/api"
import { describeCron } from "@/lib/cron"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupJob, Job } from "@/lib/types"

/** The width of the panel from which a base backup is a row of a table rather than a block of lines. */
const TABLE_FROM = 600

/** The width from which the panel's commands stand beside its title. */
const COMMANDS_FROM = 640

type Asking =
  | { kind: "setup" }
  | { kind: "restore"; archive: PitrArchive; covers: WalWindow }
  | { kind: "point" }
  | { kind: "schedule"; archive: PitrArchive }
  | { kind: "off"; archive: PitrArchive }

function shaped(answer: DbPitr): boolean {
  return record(answer) && Array.isArray(answer.earlier) && Array.isArray(answer.restores)
}

/**
 * A PostgreSQL server's point-in-time recovery, under its dumps: whether its
 * write-ahead log is archived and how well, the moments it can be recovered
 * to, the base backups and restore points those moments rest on, and the
 * copies recovered from it — each kept beside the server or switched in for
 * it.
 *
 * Everything that changes the server is a job on the server, watched here as
 * a dump is: setting archiving up and down, a base backup, a recovery and a
 * switch. The job running for the server when the page opens is the one on
 * screen.
 */
export function PointInTimeRecovery({ jobs }: { jobs: BackupJob[] }) {
  const { id, conn, engine, readOnly } = useDatabase()
  const { refresh: refreshConnections } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const [asking, setAsking] = useState<Asking | null>(null)
  const [began, setBegan] = useState<string[]>([])
  const [dismissed, setDismissed] = useState<string[]>([])
  const [shown, setShown] = useState("")
  const [busy, setBusy] = useState(false)

  const pitr = usePoll(
    async (signal) => {
      const answer = await read<DbPitr>(`/databases/${id}/pitr`, shaped, undefined, signal)
      setBusy(Boolean(answer.job))
      return answer
    },
    // Every two seconds while a job runs for the server, as the dumps are.
    busy ? 2_000 : 30_000,
    [id],
  )
  const data = pitr.data
  const container = data?.container ?? ""
  const prefix = pitrJobPrefix(container)
  const running = data?.job
  const watchedId = shown || (running && !dismissed.includes(running.id) ? running.id : "")
  const live = began.includes(watchedId) || running?.id === watchedId

  const refresh = pitr.refresh
  const name = conn.name
  const onEnd = useCallback(
    ({ job, result }: WatchedJob) => {
      refresh()
      const produced = result as PitrJobResult | undefined
      if (job.status === "succeeded") {
        notify.success(job.title, { description: produced?.summary })
        if (produced?.connectionId) refreshConnections()
      } else if (job.status === "failed") {
        notify.error(`${job.title} failed`, job.error ?? "The job did not finish.")
      } else {
        notify.info(`${job.title} was stopped`)
      }
    },
    [refresh, refreshConnections],
  )
  const transfer = useTransferJob(watchedId, prefix, live, onEnd)

  const watch = (job: Job | string) => {
    const jobId = typeof job === "string" ? job : job.id
    setBegan((held) => [...held, jobId])
    setShown(jobId)
    setAsking(null)
    setBusy(true)
    refresh()
  }
  const dismiss = () => {
    if (watchedId) setDismissed((held) => [...held, watchedId])
    setShown("")
  }

  const mayBackup = can("service.control")
  const mayAdmin = can("system.admin")
  const maySetup = mayAdmin && can("destructive")
  const archive = data?.archive
  const health = data ? archiveHealth(data) : undefined
  const recoverable = Boolean(data?.window.from)

  const start = async (path: string, body: unknown, method: "POST" | "DELETE" = "POST") => {
    try {
      const job =
        method === "DELETE"
          ? await del<Job>(`/databases/${id}${path}`, { body })
          : await post<Job>(`/databases/${id}${path}`, body)
      watch(job)
    } catch (err) {
      notify.error("It did not start", err)
    }
  }

  const switchIn = (copy: PitrRestore) =>
    confirm({
      title: `Switch to ${copy.container}`,
      confirmLabel: "Switch",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{copy.container}</span>,
        facts: (
          <>
            <FormFact label="Recovered to">{targetWords(copy.target, copy.recoveredTo)}</FormFact>
            <FormFact label="Runs at" mono>
              127.0.0.1:{copy.port}
            </FormFact>
          </>
        ),
      },
      description: (
        <>
          <p>
            <b>{container}</b> stops and is set aside under another name, kept with its data and not
            started again. <b>{copy.container}</b> takes its name, its port and its networks, so
            every application and saved connection reaches the copy without being told.
          </p>
          <p>
            Whatever was written to {container} after {targetWords(copy.target, copy.recoveredTo)}{" "}
            is not in the copy, and its accounts and passwords are the ones {container} had then.
            {archive
              ? " Archiving goes on into an archive of its own, and a base backup is taken at once."
              : ""}
          </p>
        </>
      ),
      action: async () => {
        await start(`/pitr/restores/${copy.id}/switch`, {})
        return "reported"
      },
    })

  const deleteArchive = (earlier: PitrArchive) =>
    confirm({
      title: "Delete archive",
      confirmLabel: "Delete",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono wrap-anywhere">{locationWords(earlier)}</span>,
        facts: earlier.reading ? (
          <FormFact label="Covers">{windowWords(earlier.reading.window)}</FormFact>
        ) : undefined,
      },
      description: (
        <p>
          Deletes everything this archive holds — its base backups and its log — from where it is
          kept. The moments it covers can no longer be recovered to.
        </p>
      ),
      action: async () => {
        await start(`/pitr/archives/${earlier.id}`, {}, "DELETE")
        return "reported"
      },
    })

  const produced = transfer.current?.result as PitrJobResult | undefined
  const after =
    transfer.current?.job.status === "succeeded" && produced?.connectionId ? (
      <AfterAction onClick={() => router.push(sectionHref(produced.connectionId!))}>
        Open {produced.container}
      </AfterAction>
    ) : undefined

  const roomy = width === 0 || width >= COMMANDS_FROM
  const offered = archive ? mayBackup || mayAdmin : Boolean(data?.available && maySetup)
  const commands = !offered ? null : archive ? (
    <>
      {mayBackup && (
        <Button
          size="sm"
          variant="outline"
          disabled={busy || !data?.running}
          onClick={() => setAsking({ kind: "point" })}
        >
          <Pin />
          Mark a restore point
        </Button>
      )}
      {mayBackup && (
        <Button
          size="sm"
          variant="outline"
          disabled={busy || !data?.running}
          onClick={() => void start("/pitr/backup", {})}
        >
          <Database />
          Take a base backup
        </Button>
      )}
      {mayAdmin && (
        <Button
          size="sm"
          disabled={busy || !recoverable}
          onClick={() => setAsking({ kind: "restore", archive, covers: data.window })}
        >
          <ClockRewind />
          Recover to a moment
        </Button>
      )}
      {(mayAdmin || maySetup) && (
        <VerbActions
          menuLabel="More point-in-time recovery actions"
          verbs={[
            ...(mayAdmin
              ? [
                  {
                    key: "schedule",
                    label: "Change schedule and retention",
                    icon: Pencil,
                    run: () => setAsking({ kind: "schedule", archive }),
                  },
                ]
              : []),
            ...(maySetup
              ? [
                  {
                    key: "off",
                    label: "Turn off",
                    icon: StopCircle,
                    danger: true,
                    disabled: busy || !data?.running,
                    run: () => setAsking({ kind: "off", archive }),
                  },
                ]
              : []),
          ]}
        />
      )}
    </>
  ) : (
    <Button size="sm" disabled={busy} onClick={() => setAsking({ kind: "setup" })}>
      <ClockRewind />
      Set up point-in-time recovery
    </Button>
  )

  return (
    <Panel
      plain
      aria-label="Point-in-time recovery"
      ref={frame}
      data-pitr=""
      className="outline-none"
    >
      <PanelHeader
        title={
          <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
            Point-in-time recovery
            {health && archive && <Status tone={health.tone} label={health.word} />}
          </span>
        }
        actions={
          <>
            {staleOf(pitr) && <NotUpdating error={staleOf(pitr)!} />}
            {roomy && commands}
          </>
        }
      />
      {!roomy && commands && (
        <div className="flex flex-wrap items-center gap-2 pt-3">{commands}</div>
      )}

      {!data ? (
        pitr.error ? (
          <div className="pt-4">
            <CouldNotRead what="point-in-time recovery" error={pitr.error} onRetry={pitr.refresh} />
          </div>
        ) : (
          <LoadingPanel plain rows={3} />
        )
      ) : (
        <div className="animate-rise space-y-4 pt-3">
          {transfer.current && (
            <TransferProgress
              watched={transfer.current}
              canStop={mayBackup}
              onStop={() => void transfer.stop()}
              onDismiss={dismiss}
              after={after}
            />
          )}

          {archive ? (
            <ArchiveReading pitr={data} archive={archive} width={width} />
          ) : data.available ? (
            <EmptyState
              icon={ClockRewind}
              title={`The log of ${data.container} is not archived`}
              description={`With its write-ahead log archived, ${name} can be recovered as it was at any second of the last days, not only at its last dump. The log and a nightly base backup go to a directory, S3 or Backblaze B2, encrypted.`}
              action={
                maySetup && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => setAsking({ kind: "setup" })}
                  >
                    <ClockRewind />
                    Set up point-in-time recovery
                  </Button>
                )
              }
            />
          ) : (
            <FormNote>{data.reason}</FormNote>
          )}

          <RestoredCopies
            restores={data.restores}
            maySwitch={maySetup && !readOnly}
            busy={busy}
            onSwitch={switchIn}
          />
          {readOnly && data.restores.some((copy) => copy.state === "ready") && (
            <FormNote>
              This connection is protected: a copy can be recovered beside the server, and none is
              switched in for it from this page.
            </FormNote>
          )}

          {data.earlier.length > 0 && (
            <Disclosure
              quiet
              summary={`Earlier archives (${data.earlier.length})`}
              facts="Kept until deleted: the moments before a switch, or before archiving was turned off."
            >
              <ul className="divide-y divide-hairline border-y border-hairline">
                {data.earlier.map((earlier) => (
                  <li
                    key={earlier.id}
                    className="group flex min-w-0 items-center gap-3 py-2 text-body"
                  >
                    <div className="min-w-0 flex-1">
                      <p className="truncate font-mono text-xs" title={locationWords(earlier)}>
                        {locationWords(earlier)}
                      </p>
                      <p className="text-hint text-muted-foreground">
                        {earlier.reading ? windowWords(earlier.reading.window) : "Never listed"} ·
                        ended {relativeTime(earlier.endedAt)}
                      </p>
                    </div>
                    <VerbActions
                      dim
                      verbs={[
                        ...(mayAdmin && earlier.reading?.window.from
                          ? [
                              {
                                key: "restore",
                                label: `Recover from ${earlier.stream}`,
                                icon: RotateCounterClockwise,
                                inline: true,
                                disabled: busy,
                                run: () =>
                                  setAsking({
                                    kind: "restore",
                                    archive: earlier,
                                    covers: earlier.reading!.window,
                                  }),
                              },
                            ]
                          : []),
                        ...(maySetup
                          ? [
                              {
                                key: "delete",
                                label: `Delete ${earlier.stream}`,
                                icon: Trash,
                                inline: true,
                                danger: true,
                                disabled: busy,
                                run: () => deleteArchive(earlier),
                              },
                            ]
                          : []),
                      ]}
                    />
                  </li>
                ))}
              </ul>
            </Disclosure>
          )}
        </div>
      )}

      {asking?.kind === "setup" && data && (
        <SetupPitr
          pitr={data}
          jobs={jobs}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "restore" && data && (
        <RestorePitr
          archive={asking.archive}
          covers={asking.covers}
          points={asking.archive.id === archive?.id ? data.restorePoints : []}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "point" && (
        <MarkRestorePoint
          onMarked={(point) => {
            setAsking(null)
            notify.success(`Restore point ${point.name} marked`, {
              description: `A recovery can now be asked to stop at ${momentWords(point.createdAt)}.`,
            })
            refresh()
          }}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "schedule" && (
        <PitrSchedule
          archive={asking.archive}
          onSaved={() => {
            setAsking(null)
            notify.success("Schedule and retention saved")
            refresh()
          }}
          onClose={() => setAsking(null)}
        />
      )}
      {asking?.kind === "off" && (
        <TurnOffPitr
          archive={asking.archive}
          onStarted={watch}
          onRunning={watch}
          onClose={() => setAsking(null)}
        />
      )}
      {dialog}
    </Panel>
  )
}

/**
 * The archive a server writes into: what it covers, how archiving is going,
 * and the base backups and restore points the moments rest on.
 */
function ArchiveReading({
  pitr,
  archive,
  width,
}: {
  pitr: DbPitr
  archive: PitrArchive
  width: number
}) {
  const health = archiveHealth(pitr)
  const archiver = pitr.archiver
  const backups = useMemo(() => [...(archive.reading?.backups ?? [])].reverse(), [archive.reading])
  const stored = backups.reduce((sum, one) => sum + one.size, 0)
  return (
    <div className="space-y-4">
      <p className="text-sm font-medium text-foreground" data-slot="pitr-window">
        {windowWords(pitr.window)}
      </p>
      {health.detail && health.tone === "danger" && (
        <Notice
          tone="danger"
          icon={Warning}
          title={health.word === "Failing" ? "Archiving is failing" : health.word}
        >
          <p className="wrap-anywhere">{health.detail}</p>
        </Notice>
      )}
      {health.detail && health.tone === "notice" && (
        <Notice tone="warning" icon={Warning} title="Archiving is behind">
          {health.detail}
        </Notice>
      )}
      {archive.lastBackupError && (
        <Notice tone="warning" icon={Warning} title="The last base backup failed">
          <p className="wrap-anywhere">{archive.lastBackupError}</p>
        </Notice>
      )}
      {pitr.readError && (
        <Notice tone="warning" icon={Warning} title="The destination could not be listed just now">
          <p className="wrap-anywhere">
            {pitr.readError}
            {archive.reading
              ? ` What is shown was read ${relativeTime(archive.reading.readAt)}.`
              : ""}
          </p>
        </Notice>
      )}

      <FactRows aria-label="The archive">
        <FactRow label="Archiving">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <Status tone={health.tone} label={health.word} />
            {archiver?.lastArchived && (
              <span className="text-muted-foreground">
                <span className="font-mono text-xs">{archiver.lastArchived}</span>{" "}
                <span title={timestamp(archiver.lastArchivedAt)}>
                  {relativeTime(archiver.lastArchivedAt)}
                </span>
              </span>
            )}
            {archiver && archiver.failedCount > 0 && (
              <span className="numeric text-muted-foreground">
                {plural(archiver.failedCount, "failure")} since the server&rsquo;s start
              </span>
            )}
          </span>
        </FactRow>
        <FactRow label="Base backups">
          <span className="numeric">
            {plural(backups.length, "base backup")} · {bytes(stored)} ·{" "}
            {archive.schedule ? (
              <>
                {describeCron(archive.schedule)}
                {pitr.nextBackup ? `, next ${momentWords(pitr.nextBackup)}` : ""}
              </>
            ) : (
              "taken by hand only"
            )}
          </span>
        </FactRow>
        <FactRow label="Keeps">{keepsWords(archive.retentionDays)}</FactRow>
        <FactRow label="Kept in" className="font-mono text-xs wrap-anywhere">
          {locationWords(archive)}
        </FactRow>
        <FactRow label="Encrypted">
          OpenPGP, to a key made for this archive{" "}
          <span className="font-mono text-xs text-muted-foreground" title={archive.keyFingerprint}>
            {archive.keyFingerprint.slice(-19)}
          </span>
        </FactRow>
      </FactRows>

      {backups.length === 0 ? (
        <FormNote>
          The first base backup has not finished; until it has, no moment can be recovered to.
        </FormNote>
      ) : width >= TABLE_FROM || width === 0 ? (
        <PanelBody flush className="group-data-[plain]/panel:-mx-4">
          <Table className="table-fixed" aria-label="Base backups">
            <colgroup>
              <col className="w-36" />
              <col />
              <col className="w-28" />
              <col className="w-28" />
            </colgroup>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Finished</TableHead>
                <TableHead className="px-2">Base backup</TableHead>
                <TableHead className="px-2 text-right">Stored</TableHead>
                <TableHead className="px-2 text-right">Unpacks to</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {backups.map((one) => (
                <TableRow key={one.name}>
                  <TableCell className="py-2" title={timestamp(one.finishedAt)}>
                    {relativeTime(one.finishedAt)}
                  </TableCell>
                  <TableCell className="truncate px-2 py-2 font-mono text-xs" title={one.name}>
                    {one.name}
                    {one.permanent && <Tag className="ml-2">permanent</Tag>}
                  </TableCell>
                  <TableCell className="numeric px-2 py-2 text-right">{bytes(one.size)}</TableCell>
                  <TableCell className="numeric px-2 py-2 text-right">
                    {bytes(one.dataSize)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </PanelBody>
      ) : (
        <ul
          aria-label="Base backups"
          className="-mx-4 divide-y divide-hairline border-y border-hairline"
        >
          {backups.map((one) => (
            <li key={one.name} className="px-4 py-2 text-xs">
              <p className="truncate font-mono">{one.name}</p>
              <p className="numeric flex flex-wrap gap-x-2.5 text-hint text-muted-foreground">
                <span title={timestamp(one.finishedAt)}>{relativeTime(one.finishedAt)}</span>
                <span>{bytes(one.size)} stored</span>
                <span>unpacks to {bytes(one.dataSize)}</span>
              </p>
            </li>
          ))}
        </ul>
      )}

      {pitr.restorePoints.length > 0 && <RestorePoints points={pitr.restorePoints} />}
    </div>
  )
}

/** The moments named in the archive's log from this page. */
function RestorePoints({ points }: { points: PitrRestorePoint[] }) {
  return (
    <div role="group" aria-label="Restore points" className="space-y-1.5">
      <p className="eyebrow">restore points</p>
      <ul className="flex flex-wrap gap-x-4 gap-y-1">
        {points.map((point) => (
          <li key={point.id} className="flex items-baseline gap-1.5 text-body">
            <Pin className="size-3 self-center text-muted-foreground" />
            <span className="font-mono text-xs">{point.name}</span>
            <span className="text-hint text-muted-foreground" title={timestamp(point.createdAt)}>
              {momentWords(point.createdAt)}
              {point.createdBy ? ` · ${point.createdBy}` : ""}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * The copies recovered from the server: kept beside it, each a database of
 * its own with its own page, or switched in for it.
 */
function RestoredCopies({
  restores,
  maySwitch,
  busy,
  onSwitch,
}: {
  restores: PitrRestore[]
  maySwitch: boolean
  busy: boolean
  onSwitch: (copy: PitrRestore) => void
}) {
  if (restores.length === 0) return null
  return (
    <div role="group" aria-label="Recovered copies" className="space-y-1.5">
      <p className="eyebrow">recovered copies</p>
      <ul className="divide-y divide-hairline border-y border-hairline">
        {restores.map((copy) => (
          <li key={copy.id} className="group flex min-w-0 items-center gap-3 py-2 text-body">
            <div className="min-w-0 flex-1">
              <p className="flex min-w-0 items-center gap-2">
                <span className="truncate font-mono text-xs" title={copy.container}>
                  {copy.container}
                </span>
                <Tag className="shrink-0">
                  {copy.state === "switched" ? "switched in" : "beside the server"}
                </Tag>
              </p>
              <p className="text-hint text-muted-foreground">
                Recovered to {targetWords(copy.target, copy.recoveredTo)} from{" "}
                <span className="font-mono">{copy.baseBackup}</span>
                {copy.state === "switched" && copy.replaced
                  ? ` · the server it replaced is kept, stopped, as ${copy.replaced}`
                  : ` · 127.0.0.1:${copy.port}`}
              </p>
            </div>
            {copy.state === "ready" && (
              <div className="flex shrink-0 items-center gap-1.5">
                {copy.connectionId ? (
                  <Button size="xs" variant="ghost" asChild>
                    <Link
                      href={sectionHref(copy.connectionId)}
                      aria-label={`Open ${copy.container}`}
                    >
                      Open
                    </Link>
                  </Button>
                ) : null}
                {maySwitch && (
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={busy}
                    onClick={() => onSwitch(copy)}
                  >
                    <ArrowLeftRight />
                    Switch in
                  </Button>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Name the present moment in the server's log, for a recovery to stop at. */
function MarkRestorePoint({
  onMarked,
  onClose,
}: {
  onMarked: (point: PitrRestorePoint) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const field = useId()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const valid = RESTORE_POINT.test(name.trim())
  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      onMarked(
        await post<PitrRestorePoint>(`/databases/${id}/pitr/restore-points`, { name: name.trim() }),
      )
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <TaskDialog
      title="Mark a restore point"
      description={`Name the present moment in the log of ${conn.name}`}
      subject={databaseSubject(conn, engine)}
      dirty={name.trim() !== ""}
      busy={busy}
      refusal={refusal}
      command="Mark"
      commandIcon={Pin}
      disabled={!valid}
      onRun={() => void run()}
      onClose={onClose}
    >
      <Field
        label="Name"
        htmlFor={field}
        hint={`Before a migration, a deploy or a bulk change: a recovery can then stop exactly here. ${RESTORE_POINT_RULE}`}
        error={name.trim() && !valid ? RESTORE_POINT_RULE : undefined}
      >
        <Input
          id={field}
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="before-migration"
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
        />
      </Field>
    </TaskDialog>
  )
}

/** When base backups are taken and how many days of moments are kept. */
function PitrSchedule({
  archive,
  onSaved,
  onClose,
}: {
  archive: PitrArchive
  onSaved: () => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const ids = useId()
  const [schedule, setSchedule] = useState(() => scheduleFields(archive.schedule))
  const [days, setDays] = useState(String(archive.retentionDays))
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const count = Number(days)
  const valid = Number.isInteger(count) && count >= 0 && count <= 3650
  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await put(`/databases/${id}/pitr`, {
        schedule: scheduleExpression(schedule),
        retentionDays: count,
      })
      onSaved()
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <TaskDialog
      title="Change schedule and retention"
      description={`When base backups of ${archive.container} are taken and how long moments are kept`}
      subject={databaseSubject(
        conn,
        engine,
        <FormFact label="Server" mono>
          {archive.container}
        </FormFact>,
      )}
      dirty={
        scheduleExpression(schedule) !== archive.schedule || days !== String(archive.retentionDays)
      }
      busy={busy}
      refusal={refusal}
      command="Save"
      disabled={!valid}
      onRun={() => void run()}
      onClose={onClose}
    >
      <ScheduleBuilder
        fields={schedule}
        onChange={setSchedule}
        idPrefix={`${ids}-schedule`}
        label="Base backups"
        manual
      />
      <Field
        label="Keep"
        htmlFor={`${ids}-days`}
        hint="Days of moments that can be recovered to; 0 keeps everything. Applied after the next base backup."
        error={valid ? undefined : "Keep between 0 and 3650 days."}
      >
        <Input
          id={`${ids}-days`}
          value={days}
          onChange={(event) => setDays(event.target.value)}
          inputMode="numeric"
          className="w-28"
        />
      </Field>
    </TaskDialog>
  )
}

/**
 * Stop archiving. The server restarts without the settings; the archive is
 * kept, with every moment it can still recover to, unless the reader also
 * asks for it to be deleted.
 */
function TurnOffPitr({
  archive,
  onStarted,
  onRunning,
  onClose,
}: {
  archive: PitrArchive
  onStarted: (job: Job) => void
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const [deleting, setDeleting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      onStarted(await del<Job>(`/databases/${id}/pitr`, { body: { deleteArchive: deleting } }))
    } catch (err) {
      if (err instanceof ApiError && err.code === "pitr_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <TaskDialog
      title="Turn point-in-time recovery off"
      description={`Stop archiving the log of ${archive.container}`}
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Server" mono>
            {archive.container}
          </FormFact>
          <FormFact label="Archive" mono>
            {locationWords(archive)}
          </FormFact>
        </>,
      )}
      dirty={deleting}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command={deleting ? "Turn off and delete" : "Turn off and restart"}
      commandIcon={StopCircle}
      destructive
      onRun={() => void run()}
      onClose={onClose}
    >
      <p className="text-body">
        {archive.container} restarts without archiving; connections are refused for a few seconds.
        From then on, nothing after the restart can be recovered to.
      </p>
      <OptionList>
        <OptionRow
          title="Also delete the archive"
          hint={
            archive.reading?.window.from
              ? `${windowWords(archive.reading.window)} could no longer be recovered to.`
              : "Its base backups and log are deleted from where they are kept."
          }
          tone="danger"
          checked={deleting}
          onCheckedChange={setDeleting}
        />
      </OptionList>
      {!deleting && (
        <FormNote>
          The archive is kept under Earlier archives, and a copy can still be recovered from it
          until it is deleted.
        </FormNote>
      )}
    </TaskDialog>
  )
}
