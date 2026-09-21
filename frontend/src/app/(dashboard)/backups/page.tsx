"use client"

import { useEffect, useMemo, useRef } from "react"
import { useRouter } from "next/navigation"
import { forgetMemoryState, useMemoryState } from "@/lib/view-state"
import { useSearchParams } from "next/navigation"
import { Archive, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { BackupJob, BackupResource, BackupResourceReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, RowLink } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ROW_BLEED } from "@/components/row-list"
import { FindingList, type Finding } from "@/components/finding-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { JobDialog, type JobPrefill } from "@/components/backups/job-form"
import { CoveragePanel } from "@/components/backups/coverage"
import { contentsLabel, scheduleLabel, targetLabel } from "@/components/backups/shared"
import { runJobNow, useJobVerbs } from "@/components/backups/job-verbs"

/**
 * Backups, drawn the way the host Overview is: four readings, what needs
 * attention, the jobs as a plain table, and what on this server is and is
 * not covered. A job is its own page; a new one starts from the form, or
 * from a thing on the server that has no backup yet.
 */
export default function BackupsPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const search = useSearchParams()
  const router = useRouter()
  /** One job, as a destination. */
  const open = (job: BackupJob) => router.push(`/backups/${job.id}`)
  // Another page can send a path here to have it backed up: the form opens
  // with the path already in it, and the address is cleaned so a reload does
  // not open it again.
  const incoming = search.get("source")
  // A deployment's Databases settings sends a linked connection here when no
  // job dumps it: the form opens on the coverage entry for that database,
  // which already carries the native dump as its suggestion.
  const incomingDatabase = search.get("database")
  // `?job=` opened a sheet here until 2026-09-21, and a deployment's Databases
  // settings still links a job that way from anywhere it is deployed.
  const incomingJob = search.get("job")
  useEffect(() => {
    if (incomingJob) router.replace(`/backups/${encodeURIComponent(incomingJob)}`)
  }, [incomingJob, router])
  // The open form, and its fields in `JobDialog`, are kept in memory for the
  // tab — memory, because a destination's keys are typed into it — so a
  // look at the volume it should cover does not mean filling it in again.
  const [form, setForm] = useMemoryState<{ job?: BackupJob; prefill?: JobPrefill } | null>(
    "backups.form",
    null,
  )
  // A form opened *for* something — a path sent here, a resource to protect —
  // starts from that thing, not from whatever a form left open was holding.
  const openFor = (prefill: JobPrefill) => {
    forgetMemoryState("backups.job.new")
    setForm({ prefill })
  }
  useEffect(() => {
    if (!incoming) return
    forgetMemoryState("backups.job.new")
    setForm({ prefill: { sources: [incoming] } })
  }, [incoming, setForm])
  const jobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 15000)
  const coverage = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    60000,
  )
  const admin = can("system.admin")
  const list = useMemo(() => jobs.data ?? [], [jobs.data])

  // The coverage report is what knows how to back a database up — its paths,
  // its native dump, the containers to freeze — so the form waits for it
  // rather than being filled from the id alone. Opened once: the report keeps
  // polling, and a second arrival must not reopen a form being typed into.
  const openedForDatabase = useRef(false)
  useEffect(() => {
    if (!incomingDatabase || openedForDatabase.current) return
    const resource = coverage.data?.resources?.find(
      (item) => item.kind === "database" && String(item.connectionId) === incomingDatabase,
    )
    if (!resource) return
    openedForDatabase.current = true
    forgetMemoryState("backups.job.new")
    setForm({ prefill: prefillFor(resource) })
  }, [incomingDatabase, coverage.data, setForm])

  useEffect(() => {
    if (!incoming && !incomingDatabase) return
    const url = new URL(window.location.href)
    url.searchParams.delete("source")
    url.searchParams.delete("database")
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
  }, [incoming, incomingDatabase])

  const refresh = () => {
    jobs.refresh()
    coverage.refresh()
  }

  const verbsFor = useJobVerbs({
    confirm,
    refresh,
    onEdit: (job) => setForm({ job }),
    onDeleted: refresh,
  })

  const readings = useMemo(() => summarise(list), [list])
  const findings = useMemo(
    () => attention(list, coverage.data, open, (job) => runJobNow(job, refresh)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [list, coverage.data],
  )

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Protection"
        title="Backups"
        actions={
          admin && (
            <Button size="sm" onClick={() => setForm({})}>
              <Plus className="size-4" />
              New backup
            </Button>
          )
        }
      />

      <StatGrid columns={4}>
        <StatTile
          label="Jobs"
          value={jobs.data ? String(list.length) : "—"}
          hint={
            list.length === 0
              ? "None yet"
              : `${readings.scheduled} on a schedule · ${readings.paused} paused`
          }
        />
        <StatTile
          label="Last backup"
          value={
            readings.latest
              ? readings.latest.status === "running"
                ? "Running"
                : relativeTime(readings.latest.startedAt)
              : jobs.data
                ? "Never"
                : "—"
          }
          tone={
            readings.latest?.status === "failed"
              ? "danger"
              : readings.latest?.status === "success"
                ? "success"
                : "default"
          }
          hint={
            readings.latest
              ? `${readings.latestJob?.name ?? ""} · ${readings.latest.status}`
              : undefined
          }
        />
        <StatTile
          label="Next backup"
          value={readings.next ? relativeTime(readings.next.nextRun!) : jobs.data ? "None" : "—"}
          tone={readings.overdue > 0 ? "warning" : "default"}
          hint={
            readings.overdue > 0
              ? `${plural(readings.overdue, "job")} overdue`
              : readings.next
                ? `${readings.next.name} · ${scheduleLabel(readings.next.schedule).toLowerCase()}`
                : list.length > 0
                  ? "Nothing scheduled"
                  : undefined
          }
        />
        <StatTile
          label="Stored"
          value={jobs.data ? bytes(readings.storedBytes) : "—"}
          hint={
            readings.storedRuns > 0
              ? `${plural(readings.storedRuns, "archive")} across ${plural(readings.destinations, "destination")}`
              : "No archives yet"
          }
        />
      </StatGrid>

      {(findings.length > 0 || list.length > 0) && (
        <Panel plain>
          <PanelHeader title="Attention" />
          <PanelBody className="py-0">
            <FindingList
              findings={findings}
              emptyLabel="Every job's last run succeeded, nothing is overdue, and everything the dashboard knows about is covered"
            />
          </PanelBody>
        </Panel>
      )}

      {jobs.error && !jobs.data && <ErrorState error={jobs.error} onRetry={jobs.refresh} />}
      {jobs.data && list.length === 0 && (
        <EmptyState
          icon={Archive}
          title="No backup jobs"
          description="A job archives directories, native database dumps and consistent SQLite snapshots on a schedule, to this server or to a bucket. Start from what the server has below, or write one from scratch."
          action={
            admin && (
              <Button size="sm" onClick={() => setForm({})}>
                <Plus className="size-4" />
                New backup
              </Button>
            )
          }
        />
      )}

      {list.length > 0 && (
        <Panel>
          <PanelHeader
            title="Jobs"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(list.length, "job")}
              </span>
            }
          />
          <PanelBody flush>
            {/* Six columns keep three on a phone, which is the remains of a
                table rather than a table — so below 2xl the same rows are
                drawn down the row instead of across it. 2xl, not xl: with
                the sidebar taking 256px, at 1280 the Stored column and the
                verbs were already past the right edge. */}
            <ul className="animate-rise divide-y divide-hairline 2xl:hidden">
              {list.map((job) => (
                <li
                  key={job.id}
                  className={`group flex min-w-0 items-center gap-3 py-3 ${ROW_BLEED}`}
                >
                  <div className="min-w-0 flex-1 space-y-0.5">
                    <div className="flex min-w-0 items-center gap-2">
                      <RowLink onClick={() => open(job)}>{job.name}</RowLink>
                      {!job.enabled && job.schedule && <Status state="stopped" label="paused" />}
                    </div>
                    <p className="truncate text-hint text-muted-foreground">
                      {contentsLabel(job)} · {targetLabel(job)}
                    </p>
                    <p className="flex min-w-0 flex-wrap items-center gap-x-2 text-hint text-muted-foreground">
                      <LastRun job={job} />
                      <span aria-hidden>·</span>
                      <span className="truncate">{nextLabel(job)}</span>
                    </p>
                  </div>
                  <VerbActions
                    dim
                    verbs={verbsFor(job)}
                    menuLabel={`More actions for ${job.name}`}
                  />
                </li>
              ))}
            </ul>
            <div className="hidden 2xl:block">
              <Table containerClassName="group-data-[plain]/panel:-mx-4 w-auto">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-full">Job</TableHead>
                    <TableHead>Destination</TableHead>
                    <TableHead>Schedule</TableHead>
                    <TableHead>Last run</TableHead>
                    <TableHead>Next</TableHead>
                    <TableHead className="text-right">Stored</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody className="animate-rise">
                  {list.map((job) => (
                    <TableRow key={job.id} onActivate={() => open(job)}>
                      <TableCell>
                        <div className="flex min-w-0 items-center gap-2">
                          <RowLink onClick={() => open(job)}>{job.name}</RowLink>
                          {!job.enabled && job.schedule && (
                            <Status state="stopped" label="paused" />
                          )}
                        </div>
                        <p className="truncate text-hint text-muted-foreground">
                          {contentsLabel(job)}
                        </p>
                      </TableCell>
                      <TableCell className="max-w-56 truncate font-mono text-xs">
                        {targetLabel(job)}
                      </TableCell>
                      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                        {scheduleLabel(job.schedule)}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        <LastRun job={job} />
                      </TableCell>
                      <TableCell
                        className={`text-xs whitespace-nowrap ${job.overdue ? "text-warning" : "text-muted-foreground"}`}
                      >
                        {nextLabel(job)}
                      </TableCell>
                      <TableCell className="numeric text-right text-xs whitespace-nowrap">
                        {job.stored.runs > 0 ? (
                          <>
                            {bytes(job.stored.bytes)}
                            <span className="text-muted-foreground"> · {job.stored.runs}</span>
                          </>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <VerbActions
                          dim
                          verbs={verbsFor(job)}
                          menuLabel={`More actions for ${job.name}`}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </PanelBody>
        </Panel>
      )}
      {jobs.loading && !jobs.data && <LoadingRows rows={3} />}

      <CoveragePanel
        report={coverage.data}
        loading={coverage.loading}
        canCreate={admin}
        onProtect={(res) => openFor(prefillFor(res))}
        onOpenJob={(id) => router.push(`/backups/${id}`)}
      />

      {form && admin && (
        <JobDialog
          job={form.job}
          prefill={form.prefill}
          resources={coverage.data?.resources ?? []}
          onOpenChange={(open) => {
            if (open) return
            setForm(null)
            forgetMemoryState("backups.job.")
          }}
          onDone={refresh}
        />
      )}
      {dialog}
    </Page>
  )
}

function LastRun({ job }: { job: BackupJob }) {
  if (!job.lastRun) return <span className="text-xs text-muted-foreground">never run</span>
  return (
    <Status
      state={job.lastRun.status}
      label={
        job.lastRun.status === "running"
          ? "running"
          : `${job.lastRun.status} ${relativeTime(job.lastRun.startedAt)}`
      }
    />
  )
}

function nextLabel(job: BackupJob): string {
  if (!job.schedule) return "manual only"
  if (!job.enabled) return "paused"
  if (job.overdue) return `overdue · next ${job.nextRun ? relativeTime(job.nextRun) : "unknown"}`
  return job.nextRun ? `next ${relativeTime(job.nextRun)}` : "not scheduled"
}

function prefillFor(res: BackupResource): JobPrefill {
  return {
    name: res.suggest.name,
    sources: res.suggest.sources,
    excludes: res.suggest.excludes,
    sqlitePaths: res.suggest.sqlitePaths,
    databaseDumps: res.suggest.databaseDumps,
    pauseContainers: res.suggest.pauseContainers,
  }
}

/** The four figures at the top, from the list alone. */
function summarise(list: BackupJob[]) {
  const withRuns = list.filter((j) => j.lastRun)
  const latestJob = withRuns.sort((a, b) =>
    b.lastRun!.startedAt.localeCompare(a.lastRun!.startedAt),
  )[0]
  const next = list
    .filter((j) => j.enabled && j.nextRun)
    .sort((a, b) => a.nextRun!.localeCompare(b.nextRun!))[0]
  const destinations = new Set(list.map((j) => `${j.targetKind}:${targetLabel(j)}`))
  return {
    scheduled: list.filter((j) => j.enabled && j.schedule).length,
    paused: list.filter((j) => !j.enabled).length,
    overdue: list.filter((j) => j.overdue).length,
    latest: latestJob?.lastRun,
    latestJob,
    next,
    storedBytes: list.reduce((sum, j) => sum + j.stored.bytes, 0),
    storedRuns: list.reduce((sum, j) => sum + j.stored.runs, 0),
    destinations: destinations.size,
  }
}

/**
 * What needs acting on, as findings rather than as a row of coloured boxes:
 * a job whose last run failed, a job that has gone quiet, and the things on
 * this server no job covers.
 */
function attention(
  list: BackupJob[],
  coverage: BackupResourceReport | undefined,
  open: (job: BackupJob) => void,
  runNow: (job: BackupJob) => void,
): Finding[] {
  const out: Finding[] = []
  for (const job of list) {
    if (job.lastRun?.status === "failed") {
      const lastLine = job.lastRun.log.trim().split("\n").filter(Boolean).at(-1)
      out.push({
        id: `failed-${job.id}`,
        level: "warning",
        title: `${job.name} failed ${relativeTime(job.lastRun.startedAt)}`,
        detail: lastLine?.replace(/^FAILED:\s*/, "") ?? "The run wrote no output.",
        advice: job.lastSuccessAt
          ? `Its last good backup is from ${relativeTime(job.lastSuccessAt)}. Read the run log, fix the cause, then run it again.`
          : "It has never succeeded. Read the run log and check the sources and destination.",
        meta: "run failed",
        action: { label: "Open history", onClick: () => open(job) },
      })
    }
    if (job.overdue) {
      out.push({
        id: `overdue-${job.id}`,
        level: "warning",
        title: `${job.name} is overdue`,
        detail: job.lastSuccessAt
          ? `Last good backup ${relativeTime(job.lastSuccessAt)}; it should run ${scheduleLabel(job.schedule).toLowerCase()}.`
          : `It has never succeeded and should run ${scheduleLabel(job.schedule).toLowerCase()}.`,
        advice:
          "Run it now and watch the log — a run that keeps failing is what usually makes a job go quiet.",
        meta: "overdue",
        action: { label: "Run now", onClick: () => runNow(job) },
      })
    }
  }
  const unprotected = coverage?.resources?.filter((r) => !r.protected) ?? []
  if (unprotected.length > 0) {
    const names = unprotected.slice(0, 4).map((r) => r.name)
    out.push({
      id: "unprotected",
      level: "notice",
      title:
        unprotected.length === 1
          ? `${names[0]} has no backup`
          : `${unprotected.length} things on this server have no backup`,
      detail: `${names.join(", ")}${unprotected.length > 4 ? ` and ${unprotected.length - 4} more` : ""}`,
      advice: "Each one under Coverage carries a Back up button that writes a job for it.",
      meta: "coverage",
    })
  }
  return out
}
