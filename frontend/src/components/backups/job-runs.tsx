"use client"

import { bytes, calendarDate, clockMinute, plural } from "@/lib/format"
import type { BackupArchiveEncryption, BackupManifest, BackupRun } from "@/lib/types"
import { cn } from "@/lib/utils"
import { FolderOpen, LockClosed } from "@/components/icons"
import { Detail, DetailList } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { engineProduct } from "@/components/backups/marks"

/**
 * A job's runs as a rail beside the run it shows, the shape the deployment run
 * page draws a release's steps in: each run its outcome, when it started, what
 * started it and how long it took, with its archive's size as a figure in a
 * column of its own, so a night of empty archives, or the night one doubled,
 * is read down the column.
 *
 * The size was a bar the length of the archive against the largest here. A bar
 * to a row is the shape the standard retired: it made every row a second line
 * tall to say what the figure beside it already said. A failed run took no
 * archive, so its figure is a dash.
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
  return (
    <ol aria-label="Runs" className="min-w-0 space-y-0.5">
      {runs.map((run) => {
        const selected = run.id === selectedId
        const encrypted = Boolean(run.manifest?.encryption)
        return (
          <li key={run.id} className="min-w-0">
            <button
              type="button"
              data-workspace-item={`run:${run.id}`}
              data-workspace-name={`Run ${run.id}`}
              aria-current={selected || undefined}
              aria-label={`Run ${run.id}, ${run.status}, ${calendarDate(run.startedAt)} ${clockMinute(run.startedAt)}${encrypted ? ", encrypted" : ""}`}
              onClick={() => onSelect(run)}
              className={cn(
                "flex w-full min-w-0 items-center gap-2.5 rounded-lg px-3 py-2 text-left focus-ring-inset transition-colors",
                selected ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              <StatusDot state={run.status} tone={run.status === "failed" ? "danger" : undefined} />
              <span className="block min-w-0 flex-1">
                <span className="block truncate text-body">
                  {calendarDate(run.startedAt)}{" "}
                  <span className="numeric text-muted-foreground">
                    {clockMinute(run.startedAt)}
                  </span>
                </span>
                <span className="numeric block truncate text-hint text-muted-foreground">
                  {/* An imported run was taken elsewhere; how long it took is that
                      server's to have recorded, and it is not still running. */}
                  {run.imported
                    ? "imported"
                    : `${run.trigger.replaceAll("_", " ")} · ${run.duration ?? "running"}`}
                </span>
              </span>
              {/* A lock where the archive needs the key to be opened anywhere
                  but here: what a copy of it is worth is decided by that. */}
              {encrypted && (
                <LockClosed aria-hidden className="size-3 shrink-0 text-muted-foreground" />
              )}
              <span
                data-slot="run-size"
                className={cn(
                  "numeric w-16 shrink-0 text-right text-xs",
                  run.sizeBytes ? "text-foreground" : "text-muted-foreground",
                )}
              >
                {run.sizeBytes ? bytes(run.sizeBytes) : "—"}
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

/**
 * How a run's archive was sealed, from its manifest: the key by its public
 * half — the line an exported key file prints as "public key", so the file
 * that opens it can be found — and the digest of the archive inside, which is
 * what a copy decrypted away from the dashboard is checked against.
 */
export function RunEncryption({ encryption }: { encryption: BackupArchiveEncryption }) {
  return (
    <section aria-label="Encryption" className="min-w-0 space-y-2">
      <p className="eyebrow">Encryption</p>
      <p className="text-body">
        Sealed with this server&apos;s backup key before it left the server. Restores, checks and
        downloads here decrypt it; anywhere else, the exported key and{" "}
        <code className="font-mono text-xs">age --decrypt</code> open it.
      </p>
      <DetailList>
        <Detail label="Public key" className="font-mono break-all">
          {encryption.recipient}
        </Detail>
        <Detail label="Archive inside" className="font-mono break-all">
          {encryption.plaintextDigest} ·{" "}
          <span className="numeric">{bytes(encryption.plaintextBytes)}</span>
        </Detail>
      </DetailList>
    </section>
  )
}
