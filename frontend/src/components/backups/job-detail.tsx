"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import {
  ArrowLeft,
  CloudDownload,
  Database,
  Download,
  Eye,
  FolderOpen,
  ShieldCheck,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { downloadUrl, get, post } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type {
  BackupArchiveEntry,
  BackupJob,
  BackupResourceReport,
  BackupRestoreResult,
  BackupRun,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import {
  Detail,
  DetailList,
  Metric,
  MetricStrip,
  Page,
  PageContext,
  SearchInput,
} from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Field, FormNote } from "@/components/form"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { retentionLabel, scheduleLabel, targetLabel } from "@/components/backups/shared"
import { useJobVerbs } from "@/components/backups/job-verbs"
import { JobDialog } from "@/components/backups/job-form"

type Confirm = ReturnType<typeof useConfirm>["confirm"]

/**
 * One job: what it is, its runs, and what can be done with a run — read its
 * log, look inside it, put files back, put a database back, prove it restores,
 * take it off the server.
 *
 * This was a sheet over the job table until 2026-09-21. A run carries a log,
 * an archive you browse, and two restores that each opened a dialog *on top
 * of* the sheet — three surfaces deep for a thing you are reading carefully,
 * because restoring from the wrong run is not recoverable. So a job is its own
 * destination, the dialogs are dialogs over a page, and `/deploy/[id]/runs`
 * already had the shape.
 */
export function JobPage() {
  const { job: jobId } = useParams<{ job: string }>()
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<BackupJob | null>(null)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [restore, setRestore] = useState<{ run: BackupRun; paths: string[] } | null>(null)
  const [restoreDatabase, setRestoreDatabase] = useState<BackupRun | null>(null)
  const [browsing, setBrowsing] = useState<BackupRun | null>(null)
  const [verifying, setVerifying] = useState<number | null>(null)
  const jobPoll = usePoll(
    (signal) => get<BackupJob>(`/backups/${jobId}`, undefined, signal),
    15000,
    [jobId],
  )
  const job = jobPoll.data ?? null
  const runs = usePoll(
    (signal) =>
      get<{ runs: BackupRun[]; running: boolean }>(`/backups/${jobId}/runs`, undefined, signal),
    5000,
    [jobId],
  )
  // The edit form suggests what on this server a job could also cover, so it
  // wants the same coverage report the list page reads.
  const resources = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    60000,
  )
  const verbsFor = useJobVerbs({
    confirm,
    refresh: () => {
      jobPoll.refresh()
      runs.refresh()
    },
    onEdit: setEditing,
    // Deleting a job from its own page has to leave it: what the page is about
    // is gone, and the list is where the reader was.
    onDeleted: () => router.replace("/backups"),
  })
  const verbs = job ? verbsFor(job) : []
  const selected = runs.data?.runs.find((run) => run.id === selectedId) ?? null

  const verify = async (run: BackupRun) => {
    setVerifying(run.id)
    try {
      await post(`/backups/runs/${run.id}/verify-restore`)
      notify.success("Application recovery verified", {
        description: "The restored canary passed and temporary resources were removed.",
      })
    } catch (err) {
      notify.error("Recovery check did not pass", err)
    } finally {
      setVerifying(null)
      runs.refresh()
    }
  }

  const runVerbs = (run: BackupRun): Verb[] => {
    const ok = run.status === "success"
    const out: Verb[] = [
      {
        key: "log",
        label: "Log",
        detail: "What the run wrote, and the evidence it left.",
        icon: Eye,
        inline: true,
        run: () => setSelectedId(run.id),
      },
    ]
    if (!ok) return out
    out.push({
      key: "browse",
      label: "Browse files",
      detail: "List what the archive holds without unpacking it.",
      icon: FolderOpen,
      run: () => {
        setSelectedId(run.id)
        setBrowsing(run)
      },
    })
    if (can("destructive")) {
      out.push({
        key: "restore",
        label: "Restore files…",
        detail: "Unpack into a directory, or put everything back where it came from.",
        icon: CloudDownload,
        run: () => setRestore({ run, paths: [] }),
      })
      if ((run.manifest?.databaseDumps?.length ?? 0) > 0) {
        out.push({
          key: "restore-db",
          label: "Restore database…",
          detail: "Load a native dump back into its connection, or into a drill database.",
          icon: Database,
          run: () => setRestoreDatabase(run),
        })
      }
    }
    if (job?.recovery && can("system.admin")) {
      out.push({
        key: "verify",
        label: "Verify restore",
        detail: "Restore a temporary copy and run the application's checker against it.",
        icon: ShieldCheck,
        disabled: verifying === run.id,
        run: () => void verify(run),
      })
    }
    if (can("system.admin")) {
      out.push({
        key: "download",
        label: "Download archive",
        detail: "Save the verified .tar.gz to this computer.",
        icon: Download,
        run: () => window.open(downloadUrl(`/backups/runs/${run.id}/download`), "_blank"),
      })
    }
    return out
  }

  return (
    <>
      <Page>
        <PageContext
          eyebrow={
            <Link
              href="/backups"
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <ArrowLeft className="size-3" /> Backups
            </Link>
          }
          title={job?.name ?? "Backup"}
          actions={
            job && (
              <>
                {!job.enabled && <Tag>paused</Tag>}
                <VerbBar verbs={verbs} menuLabel={`More actions for ${job.name}`} />
              </>
            )
          }
        />

        {jobPoll.error && !job && <ErrorState error={jobPoll.error} />}
        {jobPoll.loading && !job && <LoadingRows />}

        {job && (
          <>
            <MetricStrip className="animate-rise">
              <Metric label="Job" value={job.name} />
              <Metric label="Schedule" value={scheduleLabel(job.schedule)} />
            </MetricStrip>
            {/*
              What the job holds and when it next fires, as figures (§15 pass 2).
              The sheet had all four as clauses inside two `Detail` rows — a
              reader asking "is this thing actually keeping anything" had to
              read a sentence to find out, on a page whose entire subject is
              whether the answer is yes.
            */}
            <StatGrid className="animate-rise">
              <StatTile label="Stored" value={String(job.stored.runs)} hint="archives kept" />
              <StatTile
                label="Holding"
                value={bytes(job.stored.bytes)}
                hint={retentionLabel(job)}
              />
              <StatTile
                label="Last run"
                value={job.lastRun ? relativeTime(job.lastRun.startedAt) : "never"}
                tone={job.lastRun?.status === "failed" ? "danger" : undefined}
                hint={job.lastRun?.status ?? "no run yet"}
              />
              <StatTile
                label="Next run"
                value={job.enabled && job.nextRun ? relativeTime(job.nextRun) : "—"}
                tone={!job.enabled ? "warning" : undefined}
                hint={job.enabled ? scheduleLabel(job.schedule) : "schedule paused"}
              />
            </StatGrid>

            <DetailList className="text-body">
              <Detail label="Sources">
                <ul className="min-w-0 space-y-0.5 font-mono text-xs">
                  {job.sources.map((s) => (
                    <li key={s} className="truncate">
                      {s}
                    </li>
                  ))}
                </ul>
              </Detail>
              {job.excludes.length > 0 && (
                <Detail label="Excludes" className="font-mono">
                  {job.excludes.join(", ")}
                </Detail>
              )}
              <Detail label="Destination" className="font-mono">
                {targetLabel(job)}
              </Detail>
              {job.sqlitePaths?.length ||
              job.databaseDumps?.length ||
              job.pauseContainers?.length ? (
                <Detail label="Consistency">
                  {[
                    job.sqlitePaths?.length
                      ? plural(job.sqlitePaths.length, "SQLite snapshot")
                      : "",
                    job.databaseDumps?.length
                      ? plural(job.databaseDumps.length, "native dump")
                      : "",
                    job.pauseContainers?.length ? `pauses ${job.pauseContainers.join(", ")}` : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </Detail>
              ) : null}
              {job.recovery && (
                <Detail label="Recovery check">
                  {job.recovery.automatic ? "After every successful run" : "On request"}
                  <span className="text-muted-foreground">
                    {" "}
                    · schema {job.recovery.schemaVersion}
                  </span>
                </Detail>
              )}
            </DetailList>

            <Panel>
              <PanelHeader
                title="Runs"
                actions={runs.data?.running && <Status state="running" label="running now" />}
              />
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {runs.loading && !runs.data ? (
                  <LoadingRows rows={3} className="px-4" />
                ) : runs.data?.runs.length === 0 ? (
                  <EmptyNote>No runs yet. Run now takes the first one.</EmptyNote>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-full">Started</TableHead>
                        <TableHead>Result</TableHead>
                        <TableHead className="text-right">Size</TableHead>
                        <TableHead>Took</TableHead>
                        <TableHead className="w-px" />
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {runs.data?.runs.map((run) => (
                        <TableRow
                          key={run.id}
                          data-state={run.id === selectedId ? "selected" : undefined}
                          onActivate={() => setSelectedId(run.id)}
                        >
                          <TableCell>
                            <div className="text-body">{timestamp(run.startedAt)}</div>
                            <p className="text-hint text-muted-foreground">
                              {run.trigger.replaceAll("_", " ")} · run {run.id}
                            </p>
                          </TableCell>
                          <TableCell>
                            <Status state={run.status} />
                            {run.restoreVerification && (
                              <p className="mt-1 text-hint text-muted-foreground">
                                Restore check: {run.restoreVerification.state.replaceAll("_", " ")}
                              </p>
                            )}
                          </TableCell>
                          <TableCell className="numeric text-right">
                            {run.sizeBytes ? bytes(run.sizeBytes) : "—"}
                          </TableCell>
                          <TableCell className="text-muted-foreground">
                            {run.duration ?? "running"}
                          </TableCell>
                          <TableCell>
                            <VerbActions
                              dim
                              verbs={runVerbs(run)}
                              menuLabel={`More actions for run ${run.id}`}
                            />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
            </Panel>

            {selected && (
              <RunDetail
                key={selected.id}
                run={selected}
                browsing={browsing?.id === selected.id}
                onRestorePaths={(paths) => setRestore({ run: selected, paths })}
                canRestore={can("destructive") && selected.status === "success"}
              />
            )}
          </>
        )}
        {editing && (
          <JobDialog
            job={editing}
            resources={resources.data?.resources ?? []}
            onOpenChange={(open: boolean) => !open && setEditing(null)}
            onDone={jobPoll.refresh}
          />
        )}
        {dialog}
      </Page>

      {restore && (
        <RestoreFilesDialog
          run={restore.run}
          paths={restore.paths}
          confirm={confirm}
          onOpenChange={(open) => !open && setRestore(null)}
          onDone={runs.refresh}
        />
      )}
      {restoreDatabase && (
        <RestoreDatabaseDialog
          run={restoreDatabase}
          confirm={confirm}
          onOpenChange={(open) => !open && setRestoreDatabase(null)}
          onDone={runs.refresh}
        />
      )}
    </>
  )
}

/** The chosen run: its log, its evidence, and — on request — its contents. */
function RunDetail({
  run,
  browsing,
  canRestore,
  onRestorePaths,
}: {
  run: BackupRun
  browsing: boolean
  canRestore: boolean
  onRestorePaths: (paths: string[]) => void
}) {
  const [showFiles, setShowFiles] = useSessionState(`backups.run.${run.id}.files.open`, browsing)
  const verification = run.restoreVerification
  return (
    <div className="animate-rise space-y-4">
      <Panel plain>
        <PanelHeader
          title={`Run ${run.id}`}
          actions={
            run.status === "success" && (
              <Button size="xs" variant="outline" onClick={() => setShowFiles((v) => !v)}>
                <FolderOpen />
                {showFiles ? "Hide files" : "Browse files"}
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          <Well className="max-h-72 whitespace-pre-wrap">{run.log || "No output recorded."}</Well>
          {run.artifact && (
            <p className="font-mono text-hint break-all text-muted-foreground">{run.artifact}</p>
          )}
          {run.manifest?.pausedContainers && run.manifest.pausedContainers.length > 0 && (
            <FormNote>
              Paused during the archive: {run.manifest.pausedContainers.join(", ")}
            </FormNote>
          )}
          {verification && (
            <div className="space-y-2 text-body">
              <p>{verification.detail}</p>
              <DetailList>
                <Detail label="Recovery check">#{verification.id}</Detail>
                <Detail label="Schema">{verification.schemaVersion}</Detail>
                <Detail label="Temporary resources">
                  {verification.cleanupComplete ? "Removed" : "Cleanup pending"}
                </Detail>
              </DetailList>
              {verification.applicationImage && (
                <p className="font-mono text-hint break-all text-muted-foreground">
                  {verification.applicationImage}
                </p>
              )}
            </div>
          )}
        </PanelBody>
      </Panel>
      {showFiles && (
        <ArchiveBrowser run={run} canRestore={canRestore} onRestorePaths={onRestorePaths} />
      )}
    </div>
  )
}

/**
 * What the archive holds, listed without unpacking it. Entries can be ticked
 * and restored on their own — the way back for the one file somebody
 * deleted, without touching the rest of the tree.
 */
function ArchiveBrowser({
  run,
  canRestore,
  onRestorePaths,
}: {
  run: BackupRun
  canRestore: boolean
  onRestorePaths: (paths: string[]) => void
}) {
  const [filter, setFilter] = useSessionState(`backups.run.${run.id}.files.filter`, "")
  const [chosen, setChosen] = useState<string[]>([])
  const entries = usePoll(
    (signal) =>
      get<BackupArchiveEntry[]>(`/backups/runs/${run.id}/contents`, { limit: 5000 }, signal),
    0,
    [run.id],
  )
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const all = entries.data ?? []
    return needle ? all.filter((e) => e.name.toLowerCase().includes(needle)) : all
  }, [entries.data, filter])

  return (
    <Panel plain>
      <PanelHeader
        title="Files"
        actions={
          <>
            {entries.data && (
              <span className="numeric text-hint text-muted-foreground">
                {entries.data.length} entries
                {entries.data.length >= 5000 && " (first 5000)"}
              </span>
            )}
            {canRestore && chosen.length > 0 && (
              <Button size="xs" onClick={() => onRestorePaths(chosen)}>
                <CloudDownload />
                Restore {chosen.length} selected…
              </Button>
            )}
          </>
        }
      />
      <PanelBody className="space-y-2">
        <SearchInput
          dense
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by path"
          aria-label="Filter archive entries"
          containerClassName="sm:w-full"
        />
        {entries.loading && !entries.data && <LoadingRows rows={4} />}
        {entries.error && <FormNote tone="danger">{entries.error.message}</FormNote>}
        {entries.data && (
          <ul className="-mx-3 max-h-80 divide-y divide-hairline overflow-y-auto px-3 font-mono text-xs">
            {visible.slice(0, 1000).map((entry) => (
              <li key={entry.name} className="flex min-w-0 items-center gap-2.5 py-1">
                {canRestore ? (
                  <Checkbox
                    checked={chosen.includes(entry.name)}
                    onCheckedChange={(checked) =>
                      setChosen((current) =>
                        checked
                          ? [...current, entry.name]
                          : current.filter((name) => name !== entry.name),
                      )
                    }
                    aria-label={`Select ${entry.name}`}
                  />
                ) : null}
                <span className="min-w-0 flex-1 truncate">
                  {entry.name}
                  {entry.isDir && "/"}
                </span>
                <span className="numeric shrink-0 text-muted-foreground">
                  {entry.isDir ? "" : bytes(entry.size)}
                </span>
              </li>
            ))}
            {visible.length === 0 && (
              <li className="py-3 text-center text-muted-foreground">Nothing matches.</li>
            )}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Where the files go back to. A directory of the operator's choosing is the
 * safe default — the archive unpacks under it with each source in its own
 * tree. Putting everything back where it came from is the recovery, and it
 * overwrites the live trees, so it is typed.
 */
function RestoreFilesDialog({
  run,
  paths,
  confirm,
  onOpenChange,
  onDone,
}: {
  run: BackupRun
  paths: string[]
  confirm: Confirm
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const sources = run.manifest?.sources ?? []
  const canInPlace = Boolean(run.manifest?.complete) && sources.length > 0
  const [mode, setMode] = useState<"directory" | "in-place">("directory")
  const [destination, setDestination] = useState("")
  const inPlace = mode === "in-place"
  const ready = inPlace ? canInPlace : destination.trim().length > 0

  const submit = () => {
    onOpenChange(false)
    const phrase = inPlace ? "restore in place" : destination.trim()
    void confirm({
      title: inPlace ? "Put the files back where they came from" : "Restore into a directory",
      phrase,
      confirmLabel: "Restore",
      description: inPlace ? (
        <div className="space-y-2">
          <p className="text-destructive">
            Overwrites the live files under {sources.length === 1 ? "this path" : "these paths"}
            {paths.length > 0 ? ` for the ${paths.length} selected entries` : ""}:
          </p>
          <ul className="space-y-0.5 font-mono text-xs">
            {sources.map((s) => (
              <li key={s.archivePath} className="truncate">
                {s.path}
              </li>
            ))}
          </ul>
          <p>Type the phrase to continue.</p>
        </div>
      ) : (
        <p>
          Unpacks {paths.length > 0 ? `${paths.length} selected entries` : "the archive"} into{" "}
          <span className="font-mono">{destination.trim()}</span>, overwriting files that already
          exist there. Type the destination to continue.
        </p>
      ),
      action: async (confirmation) => {
        const res = await post<BackupRestoreResult>(
          `/backups/runs/${run.id}/restore`,
          { destination: inPlace ? undefined : destination.trim(), inPlace, paths },
          { confirm: confirmation },
        )
        notify.success(`Restored ${res.entries} entries (${bytes(res.bytes)})`, {
          description: (res.targets ?? [res.destination]).join(", "),
        })
        onDone()
      },
    })
  }

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`Restore files from run ${run.id}`}
      description="Choose where the files go: a directory of your choosing, or their original paths."
      footer={
        <Button onClick={submit} disabled={!ready}>
          Continue
        </Button>
      }
    >
      <div className="space-y-4">
        {paths.length > 0 && (
          <div className="space-y-1">
            <p className="eyebrow">Restoring {paths.length} selected</p>
            <ul className="max-h-24 overflow-y-auto font-mono text-xs text-muted-foreground">
              {paths.map((p) => (
                <li key={p} className="truncate">
                  {p}
                </li>
              ))}
            </ul>
          </div>
        )}
        <Field label="Where" htmlFor="restore-mode">
          <Select value={mode} onValueChange={(v) => setMode(v as typeof mode)}>
            <SelectTrigger id="restore-mode" size="sm" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="directory">Into a directory I choose</SelectItem>
              <SelectItem value="in-place" disabled={!canInPlace}>
                Back where the files came from
              </SelectItem>
            </SelectContent>
          </Select>
        </Field>
        {inPlace ? (
          <div className="space-y-1.5">
            <FormNote tone="warning">
              Each source tree is written back over its original path. Anything changed there since
              the backup is replaced.
            </FormNote>
            <ul className="space-y-0.5 font-mono text-xs">
              {sources.map((s) => (
                <li key={s.archivePath} className="truncate">
                  {s.archivePath} → {s.path}
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <Field
            label="Destination directory"
            htmlFor="restore-dest"
            hint="Each source unpacks under its own source-NNNN folder here. A scratch directory is the safe first move."
          >
            <Input
              id="restore-dest"
              value={destination}
              onChange={(e) => setDestination(e.target.value)}
              placeholder="/srv/restore"
              className="font-mono text-body"
            />
          </Field>
        )}
        {!canInPlace && (
          <FormNote>
            This archive has no manifest, so it can only be restored into a directory.
          </FormNote>
        )}
      </div>
    </Modal>
  )
}

/**
 * Loads one of the run's native database dumps back into its saved
 * connection. The default target is the dumped database itself; a drill
 * names another database on the same server so the live one is never
 * touched. The operator types the target database's name, because this
 * overwrites data.
 */
function RestoreDatabaseDialog({
  run,
  confirm,
  onOpenChange,
  onDone,
}: {
  run: BackupRun
  confirm: Confirm
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const dumps = run.manifest?.databaseDumps ?? []
  const [connectionId, setConnectionId] = useState(String(dumps[0]?.connectionId ?? ""))
  const [database, setDatabase] = useState("")
  const selected = dumps.find((dump) => String(dump.connectionId) === connectionId)
  const target = database.trim() || selected?.database || ""
  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`Restore a database from run ${run.id}`}
      description="Pick the dump and the database it goes back into."
      footer={
        <Button
          variant="destructive"
          disabled={!selected || !target}
          onClick={() => {
            if (!selected) return
            onOpenChange(false)
            void confirm({
              title: `Restore ${selected.name} into ${target}`,
              confirmLabel: "Restore database",
              phrase: target,
              description: (
                <p>
                  The <span className="font-mono">{selected.method}</span> dump of{" "}
                  <span className="font-mono">{selected.database}</span> ({bytes(selected.bytes)})
                  replaces the contents of <span className="font-mono">{target}</span> on{" "}
                  {selected.name}. Type the target database name to continue.
                </p>
              ),
              action: async (confirmation) => {
                const res = await post<{ output: string }>(
                  `/backups/runs/${run.id}/restore-database`,
                  { connectionId: selected.connectionId, database: database.trim() || undefined },
                  { confirm: confirmation },
                )
                notify.success(`Restored ${selected.name} into ${target}`, {
                  description: res.output || undefined,
                })
                onDone()
              },
            })
          }}
        >
          Continue
        </Button>
      }
    >
      <div className="space-y-4">
        <Field label="Database dump" htmlFor="restore-db-connection">
          <Select value={connectionId} onValueChange={setConnectionId}>
            <SelectTrigger id="restore-db-connection" size="sm" className="w-full">
              <SelectValue placeholder="Choose a dump" />
            </SelectTrigger>
            <SelectContent>
              {dumps.map((dump) => (
                <SelectItem key={dump.connectionId} value={String(dump.connectionId)}>
                  {dump.name} · {dump.driver} · {dump.database} · {bytes(dump.bytes)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Target database"
          htmlFor="restore-db-target"
          hint="Blank restores over the dumped database. Naming another database on the same server is the safe way to rehearse a recovery."
        >
          <Input
            id="restore-db-target"
            value={database}
            onChange={(e) => setDatabase(e.target.value)}
            placeholder={selected?.database ?? ""}
            className="font-mono text-body"
          />
        </Field>
      </div>
    </Modal>
  )
}
