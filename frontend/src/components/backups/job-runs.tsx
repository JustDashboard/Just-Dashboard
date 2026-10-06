"use client"

import { bytes, calendarDate, clockMinute, plural } from "@/lib/format"
import type { BackupManifest, BackupRun } from "@/lib/types"
import { cn } from "@/lib/utils"
import { FolderOpen } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { engineProduct } from "@/components/backups/marks"

/** A run's archive size bar, in how the run went — the run page's step bars' colours. */
const FILL: Record<BackupRun["status"], string> = {
  success: "bg-success",
  failed: "bg-destructive",
  running: "bg-brand",
}

/**
 * A job's runs as a rail beside the run it shows, the shape the deployment run
 * page draws a release's steps in: each run its outcome, when it started, what
 * started it, how long it took, and its archive as a bar the length of its
 * size against the largest here — so a run of empty archives, or the night
 * one doubled, is seen down the column before a figure is read.
 *
 * A failed run took no archive, so its bar is the narrowest red mark rather
 * than nothing: the column still says something happened that night.
 */
export function RunRail({
  runs,
  selectedId,
  onSelect,
}: {
  runs: BackupRun[]
  selectedId: number | undefined
  onSelect: (run: BackupRun) => void
}) {
  const largest = Math.max(1, ...runs.map((run) => run.sizeBytes))
  return (
    <ol aria-label="Runs" className="min-w-0 space-y-0.5">
      {runs.map((run) => {
        const selected = run.id === selectedId
        return (
          <li key={run.id} className="min-w-0">
            <button
              type="button"
              data-workspace-item={`run:${run.id}`}
              data-workspace-name={`Run ${run.id}`}
              aria-current={selected || undefined}
              aria-label={`Run ${run.id}, ${run.status}, ${calendarDate(run.startedAt)} ${clockMinute(run.startedAt)}`}
              onClick={() => onSelect(run)}
              className={cn(
                "flex w-full min-w-0 flex-col gap-1.5 rounded-lg px-3 py-2 text-left focus-ring-inset transition-colors",
                selected ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              <span className="flex w-full min-w-0 items-center gap-2.5">
                <StatusDot
                  state={run.status}
                  tone={run.status === "failed" ? "danger" : undefined}
                />
                <span className="min-w-0 flex-1 truncate text-body">
                  {calendarDate(run.startedAt)}{" "}
                  <span className="numeric text-muted-foreground">
                    {clockMinute(run.startedAt)}
                  </span>
                </span>
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  {run.sizeBytes ? bytes(run.sizeBytes) : "—"}
                </span>
              </span>
              <span className="flex w-full min-w-0 items-center gap-2.5 pl-4.5">
                <span
                  aria-hidden
                  className="relative block h-1 min-w-0 flex-1 overflow-hidden rounded-full bg-meter-track"
                >
                  <span
                    className={cn(
                      "absolute inset-y-0 left-0 min-w-1 rounded-full",
                      FILL[run.status],
                    )}
                    style={{ width: `${(run.sizeBytes / largest) * 100}%` }}
                  />
                </span>
                <span className="numeric w-24 shrink-0 truncate text-right text-hint text-muted-foreground">
                  {run.trigger.replaceAll("_", " ")} · {run.duration ?? "running"}
                </span>
              </span>
            </button>
          </li>
        )
      })}
    </ol>
  )
}

/**
 * A run's log, line by line, with the line that says why it failed washed in
 * the colour of a failure — the build console's treatment of a failing step —
 * so a log of forty lines is read from the one that matters.
 */
export function RunLog({ log }: { log: string }) {
  const lines = log.replace(/\n+$/, "").split("\n")
  if (!log.trim()) {
    return (
      <p className="rounded-lg border border-hairline bg-surface-sunken p-3 text-xs text-muted-foreground">
        No output recorded.
      </p>
    )
  }
  return (
    <figure className="min-w-0 overflow-hidden rounded-lg border border-hairline bg-surface-sunken">
      <figcaption className="flex min-h-8 items-center gap-2 border-b border-hairline px-3 py-1 text-hint text-muted-foreground">
        Log
        <span className="numeric">{plural(lines.length, "line")}</span>
      </figcaption>
      <ol className="max-h-72 overflow-auto py-1.5 font-mono text-xs leading-relaxed">
        {lines.map((line, index) => {
          const failed = /^(FAILED|ERROR)\b|^error:/i.test(line.trim())
          return (
            <li
              key={index}
              className={cn(
                "px-3 break-words whitespace-pre-wrap",
                failed && "bg-wash-danger text-destructive",
              )}
            >
              {line || " "}
            </li>
          )
        })}
      </ol>
    </figure>
  )
}

/**
 * What a run's archive holds, from its manifest: each directory and where it
 * sits in the archive, each database as its engine with the dump's method and
 * size, and the containers it paused. A run written before manifests existed
 * has none, and says nothing rather than guess.
 */
export function RunContents({ manifest }: { manifest: BackupManifest }) {
  const sources = manifest.sources ?? []
  const dumps = manifest.databaseDumps ?? []
  const paused = manifest.pausedContainers ?? []
  if (sources.length + dumps.length + paused.length === 0) return null
  return (
    <section aria-label="What the archive holds" className="min-w-0 space-y-2">
      <p className="eyebrow">In the archive</p>
      <ul className="min-w-0 divide-y divide-hairline">
        {dumps.map((dump) => (
          <li key={dump.archivePath} className="flex min-w-0 items-center gap-3 py-2">
            <ProductLogo id={engineProduct(dump.driver)} size="sm" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body font-medium">
                {dump.name}
                <span className="font-normal text-muted-foreground"> · {dump.database}</span>
              </span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {dump.archivePath}
              </span>
            </span>
            <Tag>{dump.method}</Tag>
            <span className="numeric w-16 shrink-0 text-right text-hint text-muted-foreground">
              {bytes(dump.bytes)}
            </span>
          </li>
        ))}
        {sources.map((source) => (
          <li key={source.archivePath} className="flex min-w-0 items-center gap-3 py-2">
            <ProductLogo size="sm" fallback={FolderOpen} />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-xs">{source.path}</span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {source.archivePath}
              </span>
            </span>
          </li>
        ))}
      </ul>
      {paused.length > 0 && (
        <p className="text-hint text-muted-foreground">
          Paused while it archived: <span className="text-foreground">{paused.join(", ")}</span>
        </p>
      )}
    </section>
  )
}
