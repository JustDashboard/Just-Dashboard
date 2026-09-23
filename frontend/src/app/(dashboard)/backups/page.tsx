"use client"

import { useEffect, useMemo, useRef } from "react"
import { useRouter } from "next/navigation"
import { forgetMemoryState, useMemoryState } from "@/lib/view-state"
import { useSearchParams } from "next/navigation"
import { Archive, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { BackupJob, BackupResource, BackupResourceReport, Container } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList } from "@/components/flow"
import { FindingList, type Finding } from "@/components/finding-list"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { JobDialog, type JobPrefill } from "@/components/backups/job-form"
import { CoveragePanel } from "@/components/backups/coverage"
import { JobCard } from "@/components/backups/job-card"
import { resourceProducts } from "@/components/backups/marks"
import { scheduleLabel } from "@/components/backups/shared"
import { runJobNow, useJobVerbs } from "@/components/backups/job-verbs"

/**
 * Backups: what needs doing, the jobs, and what on this server is and is not
 * covered. A job is its own page; a new one starts from the form, or from a
 * thing on the server that has no backup yet.
 *
 * **Four readings used to open the page** — Jobs, Last backup, Next backup,
 * Stored — and took the top third of it to say what every job's own row says
 * again, so §15 pass 2 is dropped here the way `/git` drops it, by naming
 * where each went. The job count and the total stored are the Jobs header's.
 * The last backup is on each job's card beside its name, in the colour of how
 * it went, and the run before it and the thirteen before that are its strip.
 * The next backup and what a job keeps are under its name. What the tiles did
 * beyond the numbers — put a failure first — the Attention list does, and the
 * cards are ordered worst first under it.
 *
 * The things on the server are drawn as the products they are, and so are the
 * jobs (by what they cover) and where they write, which is why the page polls
 * the container list: a volume's coverage names the containers that mount it,
 * and it is their images that say what it holds.
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
  // Only for the marks: without Docker the volumes and stacks keep a glyph.
  const docker = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60000,
  )
  const containers = useMemo(() => docker.data ?? [], [docker.data])
  const resources = useMemo(() => coverage.data?.resources ?? [], [coverage.data])
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

  const findings = useMemo(
    () => attention(list, open, (job) => runJobNow(job, refresh)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [list],
  )
  const ordered = useMemo(() => [...list].sort((a, b) => rank(a) - rank(b)), [list])
  // What each job covers, as products: the resources whose coverage names it,
  // and the saved databases it dumps — a dump covers a connection, not a path.
  const productsFor = (job: BackupJob) => [
    ...new Set(
      resources
        .filter(
          (r) =>
            r.coveredBy.some((c) => c.jobId === job.id) ||
            (r.connectionId !== undefined && job.databaseDumps?.includes(r.connectionId)),
        )
        .flatMap((r) => resourceProducts(r, containers, resources)),
    ),
  ]
  const stored = list.reduce((sum, j) => sum + j.stored.bytes, 0)
  const archives = list.reduce((sum, j) => sum + j.stored.runs, 0)

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

      {/* Only what somebody has to act on: a run that failed, a job that has
          gone quiet. What is not covered is the Coverage list's to say — a
          finding repeating its count was the same sentence twice. */}
      {findings.length > 0 && (
        <Panel plain>
          <PanelHeader title="Attention" />
          <PanelBody className="py-0">
            <FindingList findings={findings} emptyLabel="" />
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
        <Panel plain>
          <PanelHeader
            title="Jobs"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(list.length, "job")}
                {archives > 0 && ` · ${bytes(stored)} in ${plural(archives, "archive")}`}
              </span>
            }
          />
          <PanelBody flush className="pt-3">
            <ChoiceList className="animate-rise">
              {ordered.map((job) => (
                <JobCard key={job.id} job={job} products={productsFor(job)} verbs={verbsFor(job)} />
              ))}
            </ChoiceList>
          </PanelBody>
        </Panel>
      )}
      {jobs.loading && !jobs.data && <LoadingRows rows={3} />}

      <CoveragePanel
        report={coverage.data}
        containers={containers}
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

/** The order the cards are read in: a failure, then a job gone quiet, then the rest by what runs next, paused last. */
function rank(job: BackupJob) {
  if (job.lastRun?.status === "failed") return 0
  if (job.overdue) return 1
  if (!job.enabled && job.schedule) return 3
  return 2
}

/**
 * What needs acting on, as findings rather than as a row of coloured boxes:
 * a job whose last run failed and a job that has gone quiet.
 */
function attention(
  list: BackupJob[],
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
  return out
}
