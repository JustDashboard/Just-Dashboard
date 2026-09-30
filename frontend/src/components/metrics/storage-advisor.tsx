"use client"

import Link from "next/link"
import { useState } from "react"
import { get, post } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import {
  storageCandidates,
  advisorFileHref,
  type StorageDirectory,
  type StorageFile,
  type StorageReport,
  type StorageCleanupResult,
  type StorageSelection,
} from "@/lib/server-advisor"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { SidePanelFooter } from "@/components/side-panel"

export function StorageAdvisor({
  path: initialPath,
  inodes = false,
  onChanged,
}: {
  path: string
  inodes?: boolean
  onChanged: () => void
}) {
  const [path, setPath] = useState(initialPath)
  const [copies, setCopies] = useState(false)
  const [outcome, setOutcome] = useState<StorageCleanupResult>()
  const report = usePoll(
    (signal) => get<StorageReport>("/system/advisor/storage", { path, duplicates: copies }, signal),
    0,
    [path, copies],
  )
  return (
    <div className="space-y-5">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <code className="min-w-0 flex-1 text-body break-all">{path}</code>
        <Button variant="outline" size="sm" onClick={report.refresh}>
          Scan again
        </Button>
        {report.data && (
          <Button variant="outline" size="sm" asChild>
            <Link href={`/files?path=${encodeURIComponent(report.data.path)}`}>Open in Files</Link>
          </Button>
        )}
      </div>
      {path !== initialPath && (
        <Button variant="ghost" size="sm" onClick={() => setPath(initialPath)}>
          Back to {initialPath}
        </Button>
      )}
      {report.loading && (
        <p role="status" className="text-body text-muted-foreground">
          Measuring allocated space and directory entries…
        </p>
      )}
      {report.error && (
        <p role="alert" className="text-body text-destructive">
          {report.error.message} Scan again before selecting cleanup.
        </p>
      )}
      {outcome && (
        <div aria-live="polite" className="space-y-1 text-body">
          {outcome.items.map((item) => (
            <p key={item.path} className="break-all">
              {item.path}: {item.removed ? "removed" : item.error}
            </p>
          ))}
        </div>
      )}
      {report.data && (
        <StorageEvidence
          key={`${report.data.checkedAt}-${path}-${copies}`}
          report={report.data}
          inodes={inodes}
          disabled={!!report.error}
          onScan={setPath}
          onOutcome={setOutcome}
          onChanged={() => {
            report.refresh()
            onChanged()
          }}
        />
      )}
      {!copies && (
        <Button
          variant="outline"
          size="sm"
          disabled={report.loading}
          onClick={() => setCopies(true)}
        >
          Check exact copies
        </Button>
      )}
      <p className="text-hint leading-relaxed text-muted-foreground">
        Scans stay on this filesystem and skip symlinks and special files. Copy checks read file
        contents locally with a fixed limit. Folder totals include their children and must not be
        added together.
      </p>
    </div>
  )
}

function StorageEvidence({
  report,
  inodes,
  disabled,
  onScan,
  onChanged,
  onOutcome,
}: {
  report: StorageReport
  inodes: boolean
  disabled: boolean
  onScan: (path: string) => void
  onChanged: () => void
  onOutcome: (outcome: StorageCleanupResult) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [selected, setSelected] = useState<string[]>([])
  const [outcome, setOutcome] = useState<StorageCleanupResult>()
  const candidates = storageCandidates(report)
  const selection = candidates.filter((candidate) => selected.includes(candidate.file.path))
  const toggle = (path: string, checked: boolean) =>
    setSelected((current) =>
      checked ? [...current, path] : current.filter((item) => item !== path),
    )
  const canClean = can("file.write") && can("destructive") && !disabled && !outcome
  const clean = () =>
    confirm({
      title: "Remove selected files",
      confirmLabel: "Remove files",
      description: (
        <>
          <p>
            These {selection.length} files will be permanently removed. Old temporary files can
            still be needed; review their owners and purpose. Equal contents do not mean an
            application can work without both paths.
          </p>
          <ul className="space-y-2">
            {selection.map((item) => (
              <li key={item.file.path} className="break-all">
                <code>{item.file.path}</code>
                {item.keeper && <p className="text-muted-foreground">Keep: {item.keeper.path}</p>}
              </li>
            ))}
          </ul>
          <p>
            The server rechecks file identity, open descriptors and retained copies. Changed or
            unreadable files stay in place. A mapped file may no longer have an open descriptor.
          </p>
        </>
      ),
      action: async () => {
        const result = await post<StorageCleanupResult>("/system/advisor/storage/cleanup", {
          selections: selection,
        })
        setOutcome(result)
        onOutcome(result)
        setSelected([])
        const removed = result.items.filter((item) => item.removed).length
        const refused = result.items.length - removed
        if (removed)
          notify.success(`${removed} files removed; ${bytes(result.removedBytes)} unlinked`)
        if (refused)
          notify.error(
            `${refused} files were kept`,
            result.items
              .filter((item) => !item.removed)
              .map((item) => `${item.path}: ${item.error}`)
              .join("\n"),
          )
        if (removed) onChanged()
        return "reported"
      },
    })
  return (
    <>
      {report.requestedPath && report.requestedPath !== report.path && (
        <p className="text-body text-muted-foreground">
          Host filesystem mounted at {report.path}. File inspection and selected cleanup use these
          verified host paths; the backend image filesystem is excluded.
        </p>
      )}
      <StatGrid columns={4}>
        <StatTile
          label="Filesystem available"
          value={bytes(report.filesystem?.available)}
          hint={
            report.filesystem
              ? `${report.filesystem.freeInodes.toLocaleString()} free inodes`
              : "Unavailable"
          }
        />
        <StatTile
          label="Allocated in scan"
          value={bytes(report.allocated)}
          hint={report.complete ? "Measured filesystem entries" : "Partial scan"}
        />
        <StatTile
          label="Entries inspected"
          value={report.entries.toLocaleString()}
          hint={`${report.skippedMounts} mounted paths skipped`}
        />
        <StatTile
          label="Checked"
          value={relativeTime(report.checkedAt)}
          hint={timestamp(report.checkedAt)}
        />
      </StatGrid>
      {!report.complete && (
        <div role="status" className="space-y-1 text-body text-muted-foreground">
          <p>This scan is partial. Unread or unvisited paths can contain more data.</p>
          {report.silences.map((item) => (
            <p key={`${item.path}-${item.reason}`} className="break-all">
              {item.path}: {item.reason}
            </p>
          ))}
        </div>
      )}
      <DirectoryEvidence
        title={inodes ? "Directories with the most entries" : "Largest directories"}
        directories={inodes ? report.inodeDirectories : report.directories}
        inodes={inodes}
        onScan={onScan}
      />
      {inodes && (
        <DirectoryEvidence
          title="Largest directories"
          directories={report.directories}
          onScan={onScan}
        />
      )}
      <Panel plain>
        <PanelHeader title="Largest files" />
        <PanelBody>
          <div className="divide-y divide-hairline">
            {report.largeFiles.map((file) => (
              <FileEvidence key={file.path} file={file} />
            ))}
            {!report.largeFiles.length && (
              <p className="text-body text-muted-foreground">No regular files measured.</p>
            )}
          </div>
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader
          title="Cleanup candidates"
          actions={
            <span className="text-hint text-muted-foreground">
              {candidates.length} files to review
            </span>
          }
        />
        <PanelBody className="space-y-3">
          <p className="text-body leading-relaxed text-muted-foreground">
            Temporary candidates are regular files in /tmp or /var/tmp last modified at least seven
            days ago. Exact copies share a full checksum and retain one path. Neither check
            establishes that a file is unused.
          </p>
          {report.duplicateScanRequested && (
            <p className="text-hint text-muted-foreground">
              {report.duplicateScope} {!report.duplicateScanComplete && "Copy coverage is partial."}
            </p>
          )}
          {report.duplicates.map((group) => (
            <p key={group.sha256} className="text-hint break-all text-muted-foreground">
              Retain {group.files[0]?.path} · {group.files.length - 1} copies · up to{" "}
              {bytes(group.reclaimable)} allocated
            </p>
          ))}
          <div className="divide-y divide-hairline">
            {candidates.map((item) => (
              <Candidate
                key={item.file.path}
                item={item}
                checked={selected.includes(item.file.path)}
                disabled={
                  !canClean || (!selected.includes(item.file.path) && selected.length >= 50)
                }
                onToggle={toggle}
              />
            ))}
          </div>
          {!candidates.length && (
            <p className="text-body text-muted-foreground">
              No cleanup candidates in the inspected entries.
              {!report.duplicateScanRequested && " Check exact copies for additional evidence."}
            </p>
          )}
          {!can("file.write") || !can("destructive") ? (
            <p className="text-hint text-muted-foreground">
              File write and destructive permissions are required to remove files.
            </p>
          ) : null}
        </PanelBody>
      </Panel>
      <SidePanelFooter>
        <span className="mr-auto text-hint text-muted-foreground">
          {selection.length} selected · up to{" "}
          {bytes(selection.reduce((sum, item) => sum + item.file.allocated, 0))}
        </span>
        <Button variant="destructive" disabled={!canClean || !selection.length} onClick={clean}>
          Remove selected files
        </Button>
      </SidePanelFooter>
      {dialog}
    </>
  )
}

function DirectoryEvidence({
  title,
  directories,
  inodes,
  onScan,
}: {
  title: string
  directories: StorageDirectory[]
  inodes?: boolean
  onScan: (path: string) => void
}) {
  return (
    <Panel plain>
      <PanelHeader title={title} />
      <PanelBody>
        <div className="divide-y divide-hairline">
          {directories.map((dir) => (
            <div key={dir.path} className="flex min-w-0 flex-wrap items-center gap-2 py-2">
              <code className="min-w-0 flex-1 text-body break-all">{dir.path}</code>
              <span className="numeric text-body text-muted-foreground">
                {inodes ? `${dir.entries.toLocaleString()} entries` : bytes(dir.allocated)}
              </span>
              <Button variant="ghost" size="xs" onClick={() => onScan(dir.path)}>
                Scan here
              </Button>
            </div>
          ))}
        </div>
      </PanelBody>
    </Panel>
  )
}

function FileEvidence({ file }: { file: StorageFile }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2 py-2">
      <Link
        className="min-w-0 flex-1 text-body break-all hover:underline"
        href={advisorFileHref(file.path)}
      >
        {file.path}
      </Link>
      <span className="numeric text-body text-muted-foreground">
        {bytes(file.allocated)} allocated
        {file.size !== file.allocated && ` · ${bytes(file.size)} length`}
      </span>
    </div>
  )
}

function Candidate({
  item,
  checked,
  disabled,
  onToggle,
}: {
  item: StorageSelection
  checked: boolean
  disabled: boolean
  onToggle: (path: string, checked: boolean) => void
}) {
  return (
    <div className="flex min-w-0 items-start gap-3 py-2">
      <Checkbox
        aria-label={`Select ${item.file.path}`}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(value) => onToggle(item.file.path, value === true)}
      />
      <div className="min-w-0 flex-1 space-y-1">
        <p className="text-body break-all">{item.file.path}</p>
        <p className="text-hint text-muted-foreground">
          {bytes(item.file.allocated)} allocated ·{" "}
          {item.kind === "duplicate"
            ? "Verified copy"
            : `Modified ${relativeTime(item.file.modified)}`}
        </p>
        {item.keeper && (
          <p className="text-hint break-all text-muted-foreground">Keep {item.keeper.path}</p>
        )}
      </div>
      <Button variant="ghost" size="xs" asChild>
        <Link href={advisorFileHref(item.file.path)}>Inspect</Link>
      </Button>
    </div>
  )
}
