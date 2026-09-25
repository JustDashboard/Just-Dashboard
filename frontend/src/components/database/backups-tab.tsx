"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, CloudDownload, CloudUpload, Download, Trash } from "@/components/icons"
import { del, downloadUrl, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { DbBackupFile, DbBackups, DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { STALE_BACKUP_MS } from "@/components/database/fleet"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/** A missing dump, or one older than a week, is the warning on the page. */
function backupStale(takenAt: string | undefined) {
  return !takenAt || Date.now() - Date.parse(takenAt) > STALE_BACKUP_MS
}

/**
 * The dumps of this database that are on the server, and the two things done
 * with one: taking another, and putting one back.
 *
 * The Connection page had a Dump button and nothing that said what it had
 * produced before; the dumps sat in a directory only the file manager could
 * show. The files as a table, each downloadable, restorable (typed for: it
 * overwrites live data) and removable, under a header that says how many
 * there are, what they take, when the newest was taken and where they sit —
 * the four readings that were tiles over the table, said once in the head of
 * the thing they count (§15 pass 2). A scheduled job that covers this
 * database is the Backups section's business, and the header links there.
 */
export function BackupsTab({ conn, confirm }: { conn: DbConnection; confirm: ConfirmFn }) {
  const { can } = useAuth()
  const backups = usePoll(
    (signal) => get<DbBackups>(`/databases/${conn.id}/backups`, undefined, signal),
    30_000,
    [conn.id],
  )
  const [dumping, setDumping] = useState(false)

  const download = (file: string) => {
    const a = document.createElement("a")
    a.href = downloadUrl(`/databases/${conn.id}/backup/download`, { file })
    a.download = file
    a.click()
  }

  const dump = async () => {
    setDumping(true)
    try {
      const res = await post<{ file: string; size: number; duration: string; summary?: string }>(
        `/databases/${conn.id}/backup`,
        { database: conn.database },
      )
      notify.success("Dump complete", {
        description: [res.summary, `${bytes(res.size)} in ${res.duration}`]
          .filter(Boolean)
          .join(" · "),
        action: { label: "Download", onClick: () => download(res.file) },
      })
      backups.refresh()
    } catch (err) {
      notify.error("Dump failed", err)
    } finally {
      setDumping(false)
    }
  }

  const restore = (file: DbBackupFile) =>
    confirm({
      title: "Restore this dump",
      phrase: conn.database || file.file,
      confirmLabel: "Restore",
      description: (
        <p>
          Loads <b>{file.file}</b> from {timestamp(file.takenAt)} into{" "}
          <b>{conn.database || "this database"}</b>, replacing what is there now. Everything written
          since that dump is lost.
        </p>
      ),
      action: async (c) => {
        await post(
          `/databases/${conn.id}/restore`,
          { database: conn.database, dumpPath: `${backups.data?.dir}/${file.file}` },
          { confirm: c },
        )
        notify.success(`Restored ${file.file}`)
      },
    })

  const remove = (file: DbBackupFile) =>
    confirm({
      title: "Delete dump",
      confirmLabel: "Delete",
      description: (
        <p>
          Deletes <b>{file.file}</b> ({bytes(file.size)}) from the server. A copy downloaded earlier
          is not affected.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/backups`, { body: { file: file.file }, confirm: c })
        notify.success(`Deleted ${file.file}`)
        backups.refresh()
      },
    })

  if (backups.loading && !backups.data) return <LoadingPanel />
  if (backups.error) return <ErrorState error={backups.error} />
  if (!backups.data) return null

  const files = backups.data.files
  const newest = files[0]
  const total = files.reduce((sum, f) => sum + f.size, 0)
  const stale = backupStale(newest?.takenAt)

  return (
    <div className="flex min-w-0 animate-rise flex-col gap-6">
      {stale && (
        <Notice
          title={
            newest ? "The newest dump is over a week old" : "This database has never been dumped"
          }
          tone="warning"
        >
          A dump stays on this server and downloads to your machine as it finishes. For one that
          runs itself, add this database to a job under Backups.
        </Notice>
      )}

      <Panel plain>
        <PanelHeader
          title={
            files.length > 0
              ? `${plural(files.length, "dump")} · ${bytes(total)}`
              : plural(files.length, "dump")
          }
          eyebrow={
            newest ? (
              <span
                className={stale ? "text-warning" : undefined}
                title={timestamp(newest.takenAt)}
              >
                newest {relativeTime(newest.takenAt)} · in {backups.data.dir}
              </span>
            ) : (
              "no dump has been taken"
            )
          }
          actions={
            <div className="flex flex-wrap items-center gap-2">
              <Link
                href={`/backups?database=${conn.id}`}
                className="flex items-center gap-1 text-body text-muted-foreground hover:text-foreground"
              >
                Scheduled jobs <ArrowRight className="size-3.5" />
              </Link>
              {can("service.control") && (
                <Button size="sm" onClick={dump} pending={dumping}>
                  <CloudDownload className="size-3.5" />
                  Dump now
                </Button>
              )}
            </div>
          }
        />
        {files.length === 0 ? (
          <EmptyState
            icon={CloudDownload}
            title="No dumps yet"
            description="Dump now writes one on the server and downloads a copy as it finishes."
          />
        ) : (
          <PanelBody flush>
            <div className="min-w-0 overflow-x-auto group-data-[plain]/panel:-mx-4">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>File</TableHead>
                    <TableHead className="w-40">Taken</TableHead>
                    <TableHead className="w-28 text-right">Size</TableHead>
                    <TableHead className="w-32">Format</TableHead>
                    <TableHead className="w-24" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {files.map((file, index) => {
                    const verbs: Verb[] = [
                      {
                        key: "download",
                        label: "Download",
                        icon: Download,
                        inline: true,
                        run: () => download(file.file),
                      },
                      ...(can("destructive")
                        ? [
                            {
                              key: "restore",
                              label: "Restore",
                              icon: CloudUpload,
                              danger: true,
                              run: () => restore(file),
                            },
                            {
                              key: "delete",
                              label: "Delete",
                              icon: Trash,
                              danger: true,
                              run: () => remove(file),
                            },
                          ]
                        : []),
                    ]
                    return (
                      <TableRow key={file.file}>
                        <TableCell className="font-mono">
                          {file.file}
                          {index === 0 && <Tag className="ml-2">newest</Tag>}
                        </TableCell>
                        <TableCell
                          className="text-muted-foreground"
                          title={timestamp(file.takenAt)}
                        >
                          {relativeTime(file.takenAt)}
                        </TableCell>
                        <TableCell className="numeric text-right">{bytes(file.size)}</TableCell>
                        <TableCell className="text-muted-foreground">{file.format}</TableCell>
                        <TableCell className="text-right">
                          <VerbActions verbs={verbs} dim menuLabel={`Actions for ${file.file}`} />
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          </PanelBody>
        )}
      </Panel>
    </div>
  )
}
