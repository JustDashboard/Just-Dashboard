"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import {
  Archive,
  ArrowLeft,
  CloudDownload,
  Database,
  Download,
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
  Container,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList, Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { TileTrend } from "@/components/metrics/sparkline"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Status } from "@/components/status-dot"
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
  contentsLabel,
  retentionLabel,
  scheduleLabel,
  targetLabel,
} from "@/components/backups/shared"
import { LastRun } from "@/components/backups/job-card"
import { RunContents, RunLog, RunRail } from "@/components/backups/job-runs"
import { destinationProduct, jobProducts } from "@/components/backups/marks"
import { JobMap } from "@/components/backups/protection-map"
import { FileIcon } from "@/components/files/file-icon"
import { useJobVerbs } from "@/components/backups/job-verbs"
import { JobDialog } from "@/components/backups/job-form"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { rangeSelection } from "@/components/workspace/selection"

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
 *
 * It opens the way the dashboard's other things open: an identity line — the
 * products it covers as its mark, its name, what it takes and where it writes
 * as facts, its last outcome at the far end — then its own picture, the list
 * page's narrowed to one job (`JobMap`), then four readings, the archive's
 * size over the runs as the Holding figure's trend.
 *
 * Its runs are a rail beside the run they show, the deployment run page's
 * Details shape: each run its outcome, its archive's size as a bar, and the
 * inspector of the picked one — its log with the failing line washed, what
 * the archive holds by its manifest, its recovery check and its files. With
 * none picked it shows the newest, because a page about a job is opened to
 * see how last night went. A table of runs with the chosen one's log in a
 * panel two screens below it was the shape before, and a reader comparing
 * three runs scrolled between them.
 */
export function JobPage() {
  const { job: jobId } = useParams<{ job: string }>()
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<BackupJob | null>(null)
  const [selectedRun, selectRun] = useQuerySelection("run")
  const selectedId = selectedRun && /^\d+$/.test(selectedRun) ? Number(selectedRun) : null
  const setSelectedId = (id: number | null) => selectRun(id === null ? null : String(id))
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
  // The edit form suggests what on this server a job could also cover, and
  // the picture draws what it does cover, from the report the list page reads.
  const resources = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    60000,
  )
  // Only for the marks: a volume is drawn as the image of what mounts it.
  const docker = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60000,
  )
  const containers = useMemo(() => docker.data ?? [], [docker.data])
  const resourceList = useMemo(() => resources.data?.resources ?? [], [resources.data])
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
  const runList = runs.data?.runs ?? []
  const shown = runList.find((run) => run.id === selectedId) ?? runList[0]

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

  const browse = (run: BackupRun) => {
    setSelectedId(run.id)
    setBrowsing({ ...run })
  }

  const runVerbs = (run: BackupRun): Verb[] => {
    if (run.status !== "success") return []
    const out: Verb[] = []
    if (can("destructive")) {
      out.push({
        key: "restore",
        label: "Restore files…",
        icon: CloudDownload,
        run: () => setRestore({ run, paths: [] }),
      })
      if ((run.manifest?.databaseDumps?.length ?? 0) > 0) {
        out.push({
          key: "restore-db",
          label: "Restore database…",
          icon: Database,
          run: () => setRestoreDatabase(run),
        })
      }
    }
    if (job?.recovery && can("system.admin")) {
      out.push({
        key: "verify",
        label: "Verify restore",
        icon: ShieldCheck,
        disabled: verifying === run.id,
        run: () => void verify(run),
      })
    }
    if (can("system.admin")) {
      out.push({
        key: "download",
        label: "Download archive",
        icon: Download,
        run: () => window.open(downloadUrl(`/backups/runs/${run.id}/download`), "_blank"),
      })
    }
    return out
  }

  // Oldest first, as a trend reads into the present; failed runs took no archive.
  const sizes = [...runList]
    .reverse()
    .filter((run) => run.status === "success")
    .map((run) => run.sizeBytes)
  const failed = runList.filter((run) => run.status === "failed").length
  const products = job ? jobProducts(job, resourceList, containers) : []

  return (
    <Workspace
      name="Backups"
      stateKey={`backups.${jobId}`}
      refresh={() => {
        jobPoll.refresh()
        runs.refresh()
      }}
      escape={() => {
        if (selectedId === null) return false
        setSelectedId(null)
        return true
      }}
    >
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
                <VerbBar verbs={verbs} menuLabel={`More actions for ${job.name}`} />
                <WorkspaceHelp />
              </>
            )
          }
        />

        {jobPoll.error && !job && <ErrorState error={jobPoll.error} />}
        {jobPoll.loading && !job && <LoadingRows />}

        {job && (
          <>
            <HostIdentity
              className="animate-rise"
              logo={
                products.length > 1 ? (
                  <ProductLogos ids={products} size="md" />
                ) : (
                  <ProductLogo
                    id={products[0]}
                    fallback={Archive}
                    className="size-12 rounded-xl [&_img]:size-7"
                  />
                )
              }
              title={job.name}
              facts={
                <>
                  <span>{contentsLabel(job)}</span>
                  <FactDot />
                  <HostFact product={destinationProduct(job)}>
                    <span className={cn(job.targetKind === "local" && "font-mono")}>
                      {targetLabel(job)}
                    </span>
                  </HostFact>
                  {job.recovery && (
                    <>
                      <FactDot />
                      <span>
                        recovery check {job.recovery.automatic ? "after every run" : "on request"} ·
                        schema {job.recovery.schemaVersion}
                      </span>
                    </>
                  )}
                </>
              }
              aside={<LastRun job={job} />}
            />

            {/* The page's own ground under the dot grid, as the list page's
                picture stands: the grid gives it a middle without a frame. */}
            {(resources.data || resources.error) && (
              <div className="relative animate-rise py-2 lg:py-4">
                <div
                  aria-hidden
                  className="wire-grid pointer-events-none absolute inset-x-0 -inset-y-4"
                />
                <JobMap job={job} resources={resourceList} containers={containers} />
              </div>
            )}

            <StatGrid className="animate-rise">
              <StatTile
                label="Archives"
                value={String(job.stored.runs)}
                hint={retentionLabel(job)}
              />
              <StatTile
                label="Holding"
                value={bytes(job.stored.bytes)}
                trend={<TileTrend values={sizes} label="Archive size, oldest run to newest" />}
                hint={sizes.length > 0 ? `last archive ${bytes(sizes.at(-1))}` : undefined}
              />
              <StatTile
                label="Last run"
                value={job.lastRun ? relativeTime(job.lastRun.startedAt) : "never"}
                tone={job.lastRun?.status === "failed" ? "danger" : undefined}
                hint={
                  job.lastRun
                    ? `${job.lastRun.status}${job.lastRun.duration ? ` · took ${job.lastRun.duration}` : ""}`
                    : "no run yet"
                }
              />
              <StatTile
                label="Next run"
                value={job.enabled && job.nextRun ? relativeTime(job.nextRun) : "—"}
                tone={!job.enabled && job.schedule ? "warning" : undefined}
                hint={
                  !job.schedule
                    ? "runs by hand"
                    : job.enabled
                      ? scheduleLabel(job.schedule)
                      : "schedule paused"
                }
              />
            </StatGrid>

            {job.excludes.length > 0 && (
              <DetailList className="text-body">
                <Detail label="Excludes">
                  <ul className="min-w-0 space-y-0.5 font-mono text-xs">
                    {job.excludes.map((path) => (
                      <li key={path} className="truncate">
                        {path}
                      </li>
                    ))}
                  </ul>
                </Detail>
              </DetailList>
            )}

            <Panel plain>
              <PanelHeader
                title="Runs"
                actions={
                  runs.data?.running ? (
                    <Status state="running" label={<TextShimmer>running now</TextShimmer>} />
                  ) : (
                    runList.length > 0 && (
                      <span className="numeric text-hint text-muted-foreground">
                        {plural(runList.length, "run")}
                        {failed > 0 && (
                          <>
                            {" · "}
                            <span className="text-destructive">{failed} failed</span>
                          </>
                        )}
                      </span>
                    )
                  )
                }
              />
              <PanelBody flush className="pt-3">
                {runs.loading && !runs.data ? (
                  <LoadingRows rows={3} />
                ) : runList.length === 0 ? (
                  <EmptyNote>No runs yet. Run now takes the first one.</EmptyNote>
                ) : (
                  // One frame round the rail and the run it shows, the run
                  // page's Details: the rail decides what the inspector holds.
                  <div className="grid min-w-0 overflow-hidden rounded-xl border bg-card lg:h-[min(80vh,48rem)] lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
                    <div className="min-h-0 overflow-y-auto p-2 max-lg:max-h-80 max-lg:border-b lg:border-r">
                      <RunRail
                        runs={runList}
                        selectedId={shown?.id}
                        onSelect={(run) => setSelectedId(run.id)}
                      />
                    </div>
                    <div className="min-h-0 min-w-0 overflow-y-auto">
                      {shown && (
                        <RunInspector
                          key={shown.id}
                          run={shown}
                          verbs={runVerbs(shown)}
                          browsing={browsing}
                          onBrowse={() => browse(shown)}
                          onRestorePaths={(paths) => setRestore({ run: shown, paths })}
                          canRestore={can("destructive") && shown.status === "success"}
                        />
                      )}
                    </div>
                  </div>
                )}
              </PanelBody>
            </Panel>
          </>
        )}
        {editing && (
          <JobDialog
            job={editing}
            resources={resourceList}
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
    </Workspace>
  )
}

/**
 * The run the rail has picked: what it was, its recovery check, its log, what
 * the archive holds, where the archive is, and — on request — its files.
 */
function RunInspector({
  run,
  verbs,
  browsing,
  canRestore,
  onBrowse,
  onRestorePaths,
}: {
  run: BackupRun
  verbs: Verb[]
  browsing: BackupRun | null
  canRestore: boolean
  onBrowse: () => void
  onRestorePaths: (paths: string[]) => void
}) {
  const [showFiles, setShowFiles] = useSessionState(`backups.run.${run.id}.files.open`, false)
  useEffect(() => {
    if (browsing?.id === run.id) setShowFiles(true)
  }, [browsing, run.id, setShowFiles])
  const verification = run.restoreVerification
  return (
    <section aria-label={`Run ${run.id}`} className="min-w-0 animate-rise space-y-5 p-4 sm:p-5">
      <header className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1 space-y-1">
          <h3 className="flex min-w-0 items-center gap-3 text-title font-semibold tracking-tight">
            Run {run.id}
            <Status state={run.status} tone={run.status === "failed" ? "danger" : undefined} />
          </h3>
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span className="numeric">{timestamp(run.startedAt)}</span>
            <FactDot />
            <span>{run.trigger.replaceAll("_", " ")}</span>
            <FactDot />
            <span>
              took <span className="numeric text-foreground">{run.duration ?? "—"}</span>
            </span>
            {run.sizeBytes > 0 && (
              <>
                <FactDot />
                <span className="numeric text-foreground">{bytes(run.sizeBytes)}</span>
              </>
            )}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {run.status === "success" && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (showFiles) setShowFiles(false)
                else onBrowse()
              }}
            >
              <FolderOpen />
              {showFiles ? "Hide files" : "Browse files"}
            </Button>
          )}
          <VerbActions verbs={verbs} menuLabel={`More actions for run ${run.id}`} />
        </div>
      </header>

      {verification && (
        <div className="space-y-2 text-body">
          <Status
            tone={
              verification.state === "passed"
                ? "running"
                : verification.state === "running"
                  ? "notice"
                  : "danger"
            }
            label={`Restore check: ${verification.state.replaceAll("_", " ")}`}
          />
          {verification.detail && <p>{verification.detail}</p>}
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

      <RunLog log={run.log} />
      {run.manifest && <RunContents manifest={run.manifest} />}
      {run.artifact && (
        <p className="font-mono text-hint break-all text-muted-foreground">{run.artifact}</p>
      )}
      {showFiles && (
        <ArchiveBrowser run={run} canRestore={canRestore} onRestorePaths={onRestorePaths} />
      )}
    </section>
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
  const [chosen, setChosen] = useSessionState<string[]>(`backups.run.${run.id}.files.chosen`, [])
  const [anchor, setAnchor] = useState<string | null>(null)
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
              <Button size="xs" variant="ghost" onClick={() => setChosen([])}>
                Clear selection
              </Button>
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
          data-workspace-search
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by path"
          aria-label="Filter archive entries"
          containerClassName="sm:w-full"
        />
        {canRestore && (
          <p className="text-hint text-muted-foreground">
            Shift+click or Shift+↑/↓ selects a range. Ctrl/⌘+A selects visible entries; Escape
            clears selection.
          </p>
        )}
        {entries.loading && !entries.data && <LoadingRows rows={4} />}
        {entries.error && <FormNote tone="danger">{entries.error.message}</FormNote>}
        {entries.data && (
          <ul
            data-workspace-scroll
            onKeyDown={(event) => {
              if (!canRestore) return
              const names = visible.slice(0, 1000).map((entry) => entry.name)
              const at = names.indexOf((event.target as HTMLElement).dataset.workspaceItem ?? "")
              if (event.key === "Escape" && chosen.length) {
                event.preventDefault()
                setChosen([])
              } else if (
                (event.ctrlKey || event.metaKey) &&
                event.key.toLowerCase() === "a" &&
                at >= 0
              ) {
                event.preventDefault()
                setChosen(names)
              } else if (
                event.shiftKey &&
                ["ArrowDown", "ArrowUp"].includes(event.key) &&
                at >= 0
              ) {
                event.preventDefault()
                const next = Math.max(
                  0,
                  Math.min(names.length - 1, at + (event.key === "ArrowDown" ? 1 : -1)),
                )
                const from = anchor && names.includes(anchor) ? anchor : names[at]
                setAnchor(from)
                setChosen((current) => rangeSelection(names, current, from, names[next]))
                event.currentTarget
                  .querySelectorAll<HTMLElement>("[data-workspace-item]")
                  [next]?.focus()
              }
            }}
            className="-mx-3 max-h-80 divide-y divide-hairline overflow-y-auto px-3 font-mono text-xs"
          >
            {visible.slice(0, 1000).map((entry) => (
              <li
                key={entry.name}
                className="flex min-w-0 items-center gap-2.5 py-1"
                onClickCapture={(event) => {
                  if (!canRestore || !(event.target as HTMLElement).closest("[role='checkbox']"))
                    return
                  const names = visible.slice(0, 1000).map((entry) => entry.name)
                  if (event.shiftKey && anchor !== null && names.includes(anchor)) {
                    event.preventDefault()
                    event.stopPropagation()
                    setChosen((current) => rangeSelection(names, current, anchor, entry.name))
                  }
                  setAnchor(entry.name)
                }}
              >
                {canRestore ? (
                  <Checkbox
                    data-workspace-item={entry.name}
                    data-workspace-name={entry.name}
                    checked={chosen.includes(entry.name)}
                    onCheckedChange={(checked) =>
                      setChosen((current) =>
                        checked
                          ? [...new Set([...current, entry.name])]
                          : current.filter((name) => name !== entry.name),
                      )
                    }
                    aria-label={`Select ${entry.name}`}
                  />
                ) : null}
                {/* Drawn as what it is, the file manager's own marks: a folder
                    as a folder, a config file as its format. */}
                <FileIcon
                  entry={{
                    name: entry.name.split("/").pop() || entry.name,
                    isDir: entry.isDir,
                    isSymlink: false,
                  }}
                  className="size-4"
                />
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
 * overwrites the live trees, so it asks for ordinary confirmation.
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
    void confirm({
      title: inPlace ? "Put the files back where they came from" : "Restore into a directory",
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
        </div>
      ) : (
        <p>
          Unpacks {paths.length > 0 ? `${paths.length} selected entries` : "the archive"} into{" "}
          <span className="font-mono">{destination.trim()}</span>, overwriting files that already
          exist there.
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
              description: (
                <p>
                  The <span className="font-mono">{selected.method}</span> dump of{" "}
                  <span className="font-mono">{selected.database}</span> ({bytes(selected.bytes)})
                  replaces the contents of <span className="font-mono">{target}</span> on{" "}
                  {selected.name}. Review the target before restoring.
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
