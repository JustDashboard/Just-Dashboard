"use client"

import { useState } from "react"
import { Archive, CloudDownload, CloudUpload, Play, Plus, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post, put } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import type { BackupJob, BackupRun, DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList, Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingPanel, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
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

export default function BackupsPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [historyFor, setHistoryFor] = useState<BackupJob | null>(null)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<BackupJob[]>("/backups/", undefined, signal),
    15000,
  )

  const runNow = async (job: BackupJob) => {
    try {
      await post(`/backups/${job.id}/run`)
      notify.success(`${job.name} started`, { description: "Progress appears in the run history." })
      refresh()
    } catch (err) {
      notify.error("Could not start", err)
    }
  }

  return (
    <Page>
      <PageHeader
        eyebrow="Operations"
        title="Backups"
        actions={can("system.admin") && <JobDialog onDone={refresh} />}
      />

      {loading && <LoadingPanel />}
      {error && <ErrorState error={error} />}
      {data?.length === 0 && (
        <EmptyState
          icon={Archive}
          title="No backup jobs"
          description="Create one to archive directories on a schedule. Provider credentials are encrypted with the dashboard's master key."
        />
      )}

      <div className="grid gap-4 lg:grid-cols-2 [&>*]:min-w-0">
        {data?.map((job) => (
          <Panel key={job.id}>
            <PanelHeader
              title={job.name}
              actions={
                <>
                  {job.hasCredentials && <Tag>keys stored</Tag>}
                  <Status
                    state={job.enabled ? "enabled" : "stopped"}
                    label={job.enabled ? "enabled" : "paused"}
                  />
                </>
              }
            />
            <PanelBody>
              <DetailList>
                <Detail label="Schedule">
                  <span className="font-mono">{job.schedule || "manual only"}</span>
                </Detail>
                <Detail label="Next run">{job.nextRun ? relativeTime(job.nextRun) : "—"}</Detail>
                <Detail label="Keep">
                  {job.retention > 0 ? `${job.retention} archives` : "everything"}
                </Detail>
                {(job.databaseDumps?.length ?? 0) > 0 && (
                  <Detail label="Databases">
                    {job.databaseDumps!.length === 1
                      ? "1 native dump"
                      : `${job.databaseDumps!.length} native dumps`}
                  </Detail>
                )}
                <Detail label="Last run">
                  {job.lastRun ? (
                    <span className="flex items-center gap-1.5">
                      <Status state={job.lastRun.status} />
                      {relativeTime(job.lastRun.startedAt)}
                    </span>
                  ) : (
                    "never"
                  )}
                </Detail>
              </DetailList>
            </PanelBody>
            <PanelFooter>
              <Button size="sm" variant="outline" onClick={() => setHistoryFor(job)}>
                History
              </Button>
              {can("service.control") && (
                <Button size="sm" onClick={() => runNow(job)}>
                  <Play className="size-3.5" />
                  Run now
                </Button>
              )}
              {can("system.admin") && (
                <>
                  <TestTargetButton job={job} />
                  <JobDialog job={job} onDone={refresh} />
                  <span className="flex-1" />
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-destructive"
                    onClick={() =>
                      confirm({
                        title: "Delete backup job",
                        confirmLabel: "Delete",
                        description: (
                          <p>
                            Removes the schedule for <b>{job.name}</b>. Archives already taken are
                            kept where they are.
                          </p>
                        ),
                        action: async (c) => {
                          await del(`/backups/${job.id}`, { confirm: c })
                          refresh()
                        },
                      })
                    }
                  >
                    <Trash className="size-3.5" />
                  </Button>
                </>
              )}
            </PanelFooter>
          </Panel>
        ))}
      </div>

      <HistorySheet job={historyFor} onOpenChange={(o) => !o && setHistoryFor(null)} />
      {dialog}
    </Page>
  )
}

function TestTargetButton({ job }: { job: BackupJob }) {
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={busy}
      onClick={async () => {
        setBusy(true)
        try {
          const res = await post<{ ok: boolean; error?: string }>(`/backups/${job.id}/test`)
          if (res.ok) notify.success("Target is reachable and writable")
          else notify.error("Target unreachable", res.error)
        } finally {
          setBusy(false)
        }
      }}
    >
      {busy ? <Spinner /> : <CloudUpload className="size-3.5" />}
      Test
    </Button>
  )
}

function HistorySheet({
  job,
  onOpenChange,
}: {
  job: BackupJob | null
  onOpenChange: (open: boolean) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [selectedLog, setLogFor] = useState<BackupRun | null>(null)
  const { data, loading, refresh } = usePoll(
    (signal) =>
      job
        ? get<{ runs: BackupRun[]; running: boolean }>(`/backups/${job.id}/runs`, undefined, signal)
        : Promise.resolve({ runs: [], running: false }),
    5000,
    [job?.id],
  )
  const logFor = data?.runs.find((run) => run.id === selectedLog?.id) ?? selectedLog

  return (
    <>
      <SidePanel
        open={job !== null}
        onOpenChange={onOpenChange}
        title={job?.name ?? "Backup"}
        description={
          data?.running ? "A run is in progress right now." : "Run history, newest first"
        }
      >
        <div className="space-y-4">
          {loading && <LoadingPanel rows={4} />}
          <Panel>
            <PanelBody flush>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Started</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-right">Size</TableHead>
                    <TableHead className="w-full">Took</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data?.runs.map((run) => (
                    <TableRow key={run.id}>
                      <TableCell>
                        <div>{timestamp(run.startedAt)}</div>
                        <p className="text-hint text-muted-foreground">{run.trigger}</p>
                      </TableCell>
                      <TableCell>
                        <Status state={run.status} />
                        {run.restoreVerification && (
                          <p className="mt-1 text-hint text-muted-foreground">
                            Restore check: {run.restoreVerification.state.replaceAll("_", " ")}
                          </p>
                        )}
                      </TableCell>
                      <TableCell className="numeric text-right font-mono">
                        {run.sizeBytes ? bytes(run.sizeBytes) : "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {run.duration ?? "running"}
                      </TableCell>
                      <TableCell>
                        <div className="flex gap-1">
                          <Button size="xs" variant="ghost" onClick={() => setLogFor(run)}>
                            Log
                          </Button>
                          {run.status === "success" && can("destructive") && (
                            <RestoreButton run={run} confirm={confirm} onDone={refresh} />
                          )}
                          {run.status === "success" &&
                            can("destructive") &&
                            (run.manifest?.databaseDumps?.length ?? 0) > 0 && (
                              <RestoreDatabaseButton run={run} confirm={confirm} onDone={refresh} />
                            )}
                          {run.status === "success" && job?.recovery && can("system.admin") && (
                            <VerifyRestoreButton run={run} onDone={refresh} />
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                  {data?.runs.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="p-0">
                        <EmptyState icon={Archive} title="No runs yet" />
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </PanelBody>
          </Panel>

          {logFor && (
            <div className="space-y-1.5">
              <p className="eyebrow">Log · run {logFor.id}</p>
              <Well className="max-h-80 whitespace-pre-wrap">
                {logFor.log || "No output recorded."}
              </Well>
              {logFor.artifact && (
                <p className="font-mono text-hint break-all text-muted-foreground">
                  {logFor.artifact}
                </p>
              )}
              {logFor.restoreVerification && (
                <div className="space-y-2 rounded-md border p-3 text-body">
                  <p>{logFor.restoreVerification.detail}</p>
                  <DetailList>
                    <Detail label="Recovery check">#{logFor.restoreVerification.id}</Detail>
                    <Detail label="Schema">{logFor.restoreVerification.schemaVersion}</Detail>
                    <Detail label="Temporary resources">
                      {logFor.restoreVerification.cleanupComplete ? "Removed" : "Cleanup pending"}
                    </Detail>
                  </DetailList>
                  <p className="font-mono text-hint break-all text-muted-foreground">
                    {logFor.restoreVerification.applicationImage}
                  </p>
                </div>
              )}
            </div>
          )}
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}

/**
 * Loads one of the run's native database dumps back into its saved connection.
 * The default target is the dumped database itself; a drill names another
 * database on the same server so the live one is never touched. The operator
 * types the target database's name, because this overwrites data.
 */
function RestoreDatabaseButton({
  run,
  confirm,
  onDone,
}: {
  run: BackupRun
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onDone: () => void
}) {
  const dumps = run.manifest?.databaseDumps ?? []
  const [open, setOpen] = useState(false)
  const [connectionId, setConnectionId] = useState(String(dumps[0]?.connectionId ?? ""))
  const [database, setDatabase] = useState("")
  const selected = dumps.find((dump) => String(dump.connectionId) === connectionId)
  const target = database.trim() || selected?.database || ""
  return (
    <>
      <Button size="xs" variant="ghost" onClick={() => setOpen(true)}>
        Restore database
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={<>Restore a database from run {run.id}</>}
        footer={
          <Button
            variant="destructive"
            disabled={!selected || !target}
            onClick={() => {
              if (!selected) return
              setOpen(false)
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
        <div className="grid gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="restore-db-connection">Database dump</Label>
            <Select value={connectionId} onValueChange={setConnectionId}>
              <SelectTrigger id="restore-db-connection" className="w-full">
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
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="restore-db-target">Target database</Label>
            <Input
              id="restore-db-target"
              value={database}
              onChange={(e) => setDatabase(e.target.value)}
              placeholder={selected?.database ?? ""}
              className="font-mono text-body"
            />
            <p className="text-xs leading-relaxed text-muted-foreground">
              Leave blank to restore over the dumped database. Naming another database on the same
              server is the safe way to rehearse a recovery.
            </p>
          </div>
        </div>
      </Modal>
    </>
  )
}

function VerifyRestoreButton({ run, onDone }: { run: BackupRun; onDone: () => void }) {
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="xs"
      variant="ghost"
      disabled={busy}
      onClick={async () => {
        setBusy(true)
        try {
          await post(`/backups/runs/${run.id}/verify-restore`)
          notify.success("Application recovery verified", {
            description: "The restored canary passed and temporary resources were removed.",
          })
        } catch (err) {
          notify.error("Recovery check did not pass", err)
        } finally {
          setBusy(false)
          onDone()
        }
      }}
    >
      {busy && <Spinner className="size-3" />}
      Verify restore
    </Button>
  )
}

function RestoreButton({
  run,
  confirm,
  onDone,
}: {
  run: BackupRun
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onDone: () => void
}) {
  const [open, setOpen] = useState(false)
  const [destination, setDestination] = useState("/tmp/restore")

  return (
    <>
      <Button size="xs" variant="ghost" onClick={() => setOpen(true)}>
        <CloudDownload className="size-3" />
        Restore
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={<>Restore run {run.id}</>}
        footer={
          <>
            <Button
              onClick={() => {
                setOpen(false)
                confirm({
                  title: "Restore backup",
                  phrase: destination,
                  confirmLabel: "Restore",
                  description: (
                    <p className="text-destructive">
                      Unpacks the archive into <b>{destination}</b>, overwriting files that already
                      exist there.
                    </p>
                  ),
                  action: async (c) => {
                    const res = await post<{ entries: number; bytes: number }>(
                      `/backups/runs/${run.id}/restore`,
                      { destination },
                      { confirm: c },
                    )
                    notify.success(`Restored ${res.entries} entries (${bytes(res.bytes)})`)
                    onDone()
                  },
                })
              }}
            >
              Continue
            </Button>
          </>
        }
      >
        <div className="space-y-1.5">
          <Label htmlFor="restore-dest">Destination directory</Label>
          <Input
            id="restore-dest"
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
            className="font-mono text-body"
          />
          <p className="text-xs leading-relaxed text-muted-foreground">
            Files are unpacked here, overwriting anything with the same path. Restoring into a
            scratch directory first is usually the safer move.
          </p>
        </div>
      </Modal>
    </>
  )
}

function JobDialog({ job, onDone }: { job?: BackupJob; onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(job?.name ?? "")
  const [sources, setSources] = useState((job?.sources ?? []).join("\n"))
  const [excludes, setExcludes] = useState((job?.excludes ?? []).join("\n"))
  const [targetKind, setTargetKind] = useState(job?.targetKind ?? "local")
  const [path, setPath] = useState(job?.target.path ?? "/var/backups/just-dashboard")
  const [bucket, setBucket] = useState(job?.target.bucket ?? "")
  const [region, setRegion] = useState(job?.target.region ?? "")
  const [endpoint, setEndpoint] = useState(job?.target.endpoint ?? "")
  const [prefix, setPrefix] = useState(job?.target.prefix ?? "")
  const [accessKey, setAccessKey] = useState("")
  const [secretKey, setSecretKey] = useState("")
  const [schedule, setSchedule] = useState(job?.schedule ?? "0 3 * * *")
  const [retention, setRetention] = useState(job?.retention ?? 7)
  const [enabled, setEnabled] = useState(job?.enabled ?? true)
  const [sqlitePaths, setSQLitePaths] = useState((job?.sqlitePaths ?? []).join("\n"))
  const [databaseDumps, setDatabaseDumps] = useState<number[]>(job?.databaseDumps ?? [])
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const [recoveryEnabled, setRecoveryEnabled] = useState(Boolean(job?.recovery))
  const [recoveryImage, setRecoveryImage] = useState(job?.recovery?.image ?? "")
  const [recoveryCommand, setRecoveryCommand] = useState((job?.recovery?.command ?? []).join("\n"))
  const [recoverySchema, setRecoverySchema] = useState(job?.recovery?.schemaVersion ?? "")
  const [expectedOutput, setExpectedOutput] = useState("")
  const [recoveryAutomatic, setRecoveryAutomatic] = useState(job?.recovery?.automatic ?? false)

  const submit = async () => {
    const body = {
      name,
      sources: sources
        .split("\n")
        .map((s) => s.trim())
        .filter(Boolean),
      excludes: excludes
        .split("\n")
        .map((s) => s.trim())
        .filter(Boolean),
      targetKind,
      target: targetKind === "local" ? { path } : { bucket, region, endpoint, prefix },
      schedule,
      retention: Number(retention),
      enabled,
      sqlitePaths: sqlitePaths
        .split("\n")
        .map((path) => path.trim())
        .filter(Boolean),
      databaseDumps,
      recovery: recoveryEnabled
        ? {
            image: recoveryImage.trim(),
            command: recoveryCommand.split("\n").filter((arg) => arg.length > 0),
            schemaVersion: recoverySchema.trim(),
            expectedOutputDigest: job?.recovery?.expectedOutputDigest ?? "",
            timeoutSeconds: job?.recovery?.timeoutSeconds ?? 60,
            maxBytes: job?.recovery?.maxBytes ?? 16 * 1024 ** 3,
            automatic: recoveryAutomatic,
          }
        : null,
      expectedRecoveryOutput: expectedOutput.trim() || undefined,
      // Omitted when blank so editing a schedule does not wipe stored keys.
      secrets:
        accessKey || secretKey ? { accessKeyId: accessKey, secretAccessKey: secretKey } : undefined,
    }
    try {
      if (job) await put(`/backups/${job.id}`, body)
      else await post("/backups/", body)
      notify.success(job ? "Job updated" : "Job created")
      setOpen(false)
      onDone()
    } catch (err) {
      notify.error("Could not save", err)
    }
  }

  return (
    <>
      {job ? (
        <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
          Edit
        </Button>
      ) : (
        <Button size="sm" onClick={() => setOpen(true)}>
          <Plus className="size-4" />
          New job
        </Button>
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        size="lg"
        title={job ? `Edit ${job.name}` : "New backup job"}
        footer={
          <>
            <Button onClick={submit} disabled={!name || !sources.trim()}>
              {job ? "Save" : "Create"}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="job-name">Name</Label>
            <Input id="job-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="job-sources">Sources (one per line)</Label>
              <Textarea
                id="job-sources"
                value={sources}
                onChange={(e) => setSources(e.target.value)}
                rows={4}
                className="font-mono text-xs"
                placeholder="/srv/app&#10;/etc/nginx"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="job-excludes">Excludes (glob, one per line)</Label>
              <Textarea
                id="job-excludes"
                value={excludes}
                onChange={(e) => setExcludes(e.target.value)}
                rows={4}
                className="font-mono text-xs"
                placeholder="node_modules&#10;*.log"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label>Destination</Label>
            <Select value={targetKind} onValueChange={(v) => setTargetKind(v as typeof targetKind)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="local">Local directory</SelectItem>
                <SelectItem value="s3">Amazon S3</SelectItem>
                <SelectItem value="b2">Backblaze B2</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {targetKind === "local" ? (
            <div className="space-y-1.5">
              <Label htmlFor="job-path">Directory</Label>
              <Input
                id="job-path"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                className="font-mono text-body"
              />
            </div>
          ) : (
            <div className="grid gap-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="job-bucket">Bucket</Label>
                  <Input
                    id="job-bucket"
                    value={bucket}
                    onChange={(e) => setBucket(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="job-region">Region</Label>
                  <Input
                    id="job-region"
                    value={region}
                    onChange={(e) => setRegion(e.target.value)}
                    placeholder="us-east-1"
                  />
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="job-endpoint">Endpoint</Label>
                  <Input
                    id="job-endpoint"
                    value={endpoint}
                    onChange={(e) => setEndpoint(e.target.value)}
                    placeholder={
                      targetKind === "b2" ? "s3.us-west-004.backblazeb2.com" : "leave blank for AWS"
                    }
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="job-prefix">Prefix</Label>
                  <Input
                    id="job-prefix"
                    value={prefix}
                    onChange={(e) => setPrefix(e.target.value)}
                  />
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="job-access">Access key ID</Label>
                  <Input
                    id="job-access"
                    value={accessKey}
                    onChange={(e) => setAccessKey(e.target.value)}
                    placeholder={job?.hasCredentials ? "unchanged" : ""}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="job-secret">Secret access key</Label>
                  <Input
                    id="job-secret"
                    type="password"
                    value={secretKey}
                    onChange={(e) => setSecretKey(e.target.value)}
                    placeholder={job?.hasCredentials ? "unchanged" : ""}
                  />
                </div>
              </div>
            </div>
          )}

          <div className="grid items-end gap-3 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="job-schedule">Schedule (cron)</Label>
              <Input
                id="job-schedule"
                value={schedule}
                onChange={(e) => setSchedule(e.target.value)}
                className="font-mono text-body"
                placeholder="0 3 * * *"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="job-retention">Keep</Label>
              <Input
                id="job-retention"
                type="number"
                min={0}
                value={retention}
                onChange={(e) => setRetention(Number(e.target.value))}
              />
            </div>
            <label className="flex items-center gap-2 pb-2 text-body">
              <Switch checked={enabled} onCheckedChange={setEnabled} />
              Enabled
            </label>
          </div>
          <details className="rounded-md border p-3">
            <summary className="cursor-pointer text-body font-medium">
              Consistency and recovery checks
            </summary>
            <div className="mt-4 space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="job-sqlite">SQLite files to snapshot (one per line)</Label>
                <Textarea
                  id="job-sqlite"
                  value={sqlitePaths}
                  onChange={(e) => setSQLitePaths(e.target.value)}
                  rows={3}
                  className="font-mono text-xs"
                />
                <p className="text-hint text-muted-foreground">
                  Files must be inside the sources above. Each database gets a native consistent
                  snapshot; its live journals are replaced by that snapshot in the archive. Other
                  files use ordinary filesystem capture.
                </p>
              </div>
              <fieldset className="space-y-1.5">
                <legend className="text-body">Database dumps</legend>
                <p className="text-hint text-muted-foreground">
                  Each saved connection is dumped with its engine&apos;s own tool (pg_dump,
                  mysqldump, mongodump, a Redis snapshot) or the built-in dump, and the file is
                  stored in the archive. A deployment linked to the database accepts this as its
                  backup coverage.
                </p>
                {connections.error && <ErrorState error={connections.error} />}
                {(connections.data?.length ?? 0) === 0 && !connections.loading && (
                  <p className="text-hint text-muted-foreground">
                    No saved database connections yet. Add one on the Databases page first.
                  </p>
                )}
                <div className="grid gap-1.5 sm:grid-cols-2">
                  {connections.data?.map((connection) => (
                    <Label
                      key={connection.id}
                      className="flex min-h-9 items-center gap-2 text-xs font-normal"
                    >
                      <Checkbox
                        checked={databaseDumps.includes(connection.id)}
                        onCheckedChange={(checked) =>
                          setDatabaseDumps((current) =>
                            checked
                              ? [...current, connection.id].sort((a, b) => a - b)
                              : current.filter((id) => id !== connection.id),
                          )
                        }
                        aria-label={`Dump ${connection.name}`}
                      />
                      <span className="min-w-0 truncate">
                        {connection.name}
                        <span className="text-muted-foreground">
                          {" "}
                          · {connection.driver}
                          {connection.database ? ` · ${connection.database}` : ""}
                        </span>
                      </span>
                    </Label>
                  ))}
                </div>
              </fieldset>
              <label className="flex items-center gap-2 text-body">
                <Switch checked={recoveryEnabled} onCheckedChange={setRecoveryEnabled} />
                Application recovery check
              </label>
              {recoveryEnabled && (
                <div className="space-y-3">
                  <p className="text-hint text-muted-foreground">
                    Restore a temporary copy and run your application’s checker without network
                    access. Restored sources are available at /restore/source-0001,
                    /restore/source-0002, in source order. The checker must open the restored data,
                    verify its schema and canary record, then print the expected result and exit 0.
                  </p>
                  <div className="space-y-1.5">
                    <Label htmlFor="job-recovery-image">Application image digest</Label>
                    <Input
                      id="job-recovery-image"
                      value={recoveryImage}
                      onChange={(e) => setRecoveryImage(e.target.value)}
                      placeholder="sha256:… or registry/app@sha256:…"
                      className="font-mono text-xs"
                    />
                    <p className="text-hint text-muted-foreground">
                      Retain or pull this exact image on the server before checking.
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="job-recovery-command">
                      Checker executable and arguments (one per line)
                    </Label>
                    <Textarea
                      id="job-recovery-command"
                      value={recoveryCommand}
                      onChange={(e) => setRecoveryCommand(e.target.value)}
                      rows={4}
                      className="font-mono text-xs"
                      placeholder="/app/check-recovery&#10;/restore/source-0001"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="job-recovery-schema">Expected schema version</Label>
                    <Input
                      id="job-recovery-schema"
                      value={recoverySchema}
                      onChange={(e) => setRecoverySchema(e.target.value)}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="job-recovery-output">Expected canary output</Label>
                    <Input
                      id="job-recovery-output"
                      value={expectedOutput}
                      onChange={(e) => setExpectedOutput(e.target.value)}
                      placeholder={
                        job?.recovery
                          ? "Leave blank to keep the saved fingerprint"
                          : "schema-v1:canary-present"
                      }
                    />
                    <p className="text-hint text-muted-foreground">
                      Only its SHA-256 fingerprint is saved. Leading and trailing whitespace are
                      ignored.
                    </p>
                  </div>
                  <label className="flex items-center gap-2 text-body">
                    <Switch checked={recoveryAutomatic} onCheckedChange={setRecoveryAutomatic} />
                    Verify after every successful backup
                  </label>
                  <p className="text-hint text-muted-foreground">
                    Each check is limited to {job?.recovery?.timeoutSeconds ?? 60} seconds and{" "}
                    {bytes(job?.recovery?.maxBytes ?? 16 * 1024 ** 3)} of restored data. Deployment
                    policies requiring restore evidence also run a missing check before activation.
                  </p>
                </div>
              )}
            </div>
          </details>
        </div>
      </Modal>
    </>
  )
}
