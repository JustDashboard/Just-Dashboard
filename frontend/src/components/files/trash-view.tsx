"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  FolderOpen,
  Information,
  Location,
  RefreshClockwise,
  RotateCounterClockwise,
  Trash,
} from "@/components/icons"
import { ApiError, del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { FileEntry, FilePlaces } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { PaneHeader, PaneSection } from "@/components/panel"
import { PaneBar, paneShown, type PaneStop } from "@/components/phone-panes"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Detail, DetailList } from "@/components/detail-list"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { FileIcon } from "@/components/file-icon"
import { FilesSidebar } from "@/components/files/files-sidebar"
import { useConflicts } from "@/components/files/conflict-dialog"
import { parentOf } from "@/components/files/media"
import {
  cameFrom,
  deletedWhen,
  itemSize,
  keepSentence,
  keptFor,
  keptShort,
  trashSentence,
  type Restored,
  type TrashItem,
  type TrashListing,
} from "@/components/files/trash"

type TrashPane = "places" | "trash" | "details"

const PANES: PaneStop<TrashPane>[] = [
  { key: "places", label: "Places", icon: <Location /> },
  { key: "trash", label: "Trash", icon: <Trash /> },
  { key: "details", label: "Details", icon: <Information /> },
]

/**
 * The columns a narrow list gives up, by the list's own width: where a thing
 * came from is what the Trash is read for, so it is the last to go.
 */
const COLUMN = {
  from: "hidden @lg:table-cell",
  kept: "hidden @3xl:table-cell",
  size: "hidden @2xl:table-cell",
} as const

/** A trashed item drawn as the entry it was, so its icon is the one the listing gave it. */
function asEntry(item: TrashItem): FileEntry {
  return {
    name: item.name,
    path: item.originalPath || item.name,
    size: item.size,
    mode: item.mode,
    modeOctal: item.modeOctal,
    isDir: item.isDir,
    isSymlink: item.isSymlink,
    linkTarget: item.linkTarget,
    owner: item.owner,
    group: item.group,
    uid: 0,
    gid: 0,
    modified: item.deletedAt,
  } as FileEntry
}

/** Ticks once a minute, for "2 hours ago" and "kept 29 more days". */
function useMinuteClock() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(id)
  }, [])
  return now
}

/**
 * The Trash: what was deleted from the Files page, where it came from, when
 * and by whom, and how long it is still kept, with Restore, Delete
 * permanently and Empty Trash. It is a place in the Files sidebar and in the
 * phone's Places pane, and a page of its own (`/files/trash`, `?item=` for
 * the one shown in the details), drawn in the Files workbench's frame so the
 * places stay beside it.
 */
export function TrashView() {
  const router = useRouter()
  const params = useSearchParams()
  const { can } = useAuth()
  const canWrite = can("file.write")
  const canDestruct = can("destructive")
  const { confirm, dialog } = useConfirm()
  const conflicts = useConflicts()
  const now = useMinuteClock()
  const [recent] = useViewState<string[]>("files.recent", [])
  const [pane, setPane] = useState<TrashPane>("trash")
  const places = usePoll<FilePlaces>((signal) => get("/files/places", undefined, signal), 0, [])
  const trash = usePoll<TrashListing>(
    (signal) => get("/files/trash", undefined, signal),
    20_000,
    [],
  )
  const items = useMemo(() => trash.data?.items ?? [], [trash.data])
  const chosenId = params.get("item")
  const chosen = items.find((item) => item.id === chosenId) ?? null

  const choose = useCallback(
    (item: TrashItem | null, openDetails = false) => {
      const next = new URLSearchParams(params.toString())
      if (item) next.set("item", item.id)
      else next.delete("item")
      const query = next.toString()
      router.replace(query ? `/files/trash?${query}` : "/files/trash", { scroll: false })
      if (openDetails) setPane("details")
    },
    [params, router],
  )

  const showFolder = (path: string) => router.push(`/files?path=${encodeURIComponent(path)}`)

  const restore = async (item: TrashItem) => {
    const run = (conflict?: "replace" | "keep") =>
      post<Restored>("/files/trash/restore", { id: item.id, ...(conflict ? { conflict } : {}) })
    try {
      let restored: Restored
      try {
        restored = await run()
      } catch (err) {
        if (!(err instanceof ApiError && err.status === 409 && err.code === "already_exists"))
          throw err
        const answer = await conflicts.ask([item.name], cameFrom(item), "restored")
        if (!answer || answer === "skip") return
        restored = await run(answer)
      }
      if (chosenId === item.id) choose(null)
      trash.refresh()
      notify.success(`Restored ${restored.name}`, {
        description: restored.replaced
          ? `What had its name is in the Trash now.`
          : `Back in ${parentOf(restored.path)}.`,
        action: { label: "Show", onClick: () => showFolder(parentOf(restored.path)) },
      })
    } catch (err) {
      notify.error(`Could not restore ${item.name}`, err)
    }
  }

  const deleteForGood = (item: TrashItem) =>
    confirm({
      title: "Delete permanently",
      confirmLabel: "Delete permanently",
      subject: {
        mark: <FileIcon entry={asEntry(item)} className="size-6" />,
        name: item.name,
        facts: <span className="font-mono break-all">{item.originalPath || item.folder}</span>,
      },
      description: (
        <p>
          It is deleted permanently{item.isDir ? ", along with everything inside it" : ""}, and
          cannot be restored.
        </p>
      ),
      action: async () => {
        await del("/files/trash/item", { query: { id: item.id } })
        if (chosenId === item.id) choose(null)
        trash.refresh()
        notify.success(`Deleted ${item.name} permanently`)
        return "reported" as const
      },
    })

  const empty = () =>
    confirm({
      title: "Empty Trash",
      confirmLabel: "Empty Trash",
      description: (
        <p>
          Everything in the Trash, {trashSentence(trash.data ?? { items, bytes: 0, partial: true })}
          , is deleted permanently. This cannot be undone.
        </p>
      ),
      action: async () => {
        await del("/files/trash")
        choose(null)
        trash.refresh()
        notify.success("Emptied the Trash")
        return "reported" as const
      },
    })

  return (
    <Page fill="workspace" data-files-workspace className="gap-2">
      <h1 className="sr-only">Trash</h1>
      <div
        style={{ "--panel-ground": "var(--card)" } as React.CSSProperties}
        className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-card lg:rounded-xl lg:border"
      >
        <PaneHeader className="gap-2 px-2 py-1.5">
          <IconAction label="Back to Files" variant="outline" className="size-8" asChild>
            <Link href="/files">
              <ArrowLeft />
            </Link>
          </IconAction>
          <Trash aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <span className="text-body font-medium">Trash</span>
          <span
            data-testid="trash-summary"
            className="min-w-0 truncate text-xs text-muted-foreground"
          >
            {trash.data
              ? `${trashSentence(trash.data)}${trash.data.items.length > 0 ? `, each ${keepSentence(trash.data.keepDays)}` : ""}`
              : ""}
          </span>
          <span className="flex-1" />
          <IconAction label="Refresh" className="size-8" onClick={() => trash.refresh()}>
            <RefreshClockwise />
          </IconAction>
          {canDestruct && items.length > 0 && (
            <Button size="sm" variant="outline" className="text-destructive" onClick={empty}>
              <Trash className="size-3.5" />
              <span className="max-sm:sr-only">Empty Trash</span>
            </Button>
          )}
        </PaneHeader>

        <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
          <div
            className={cn(
              "flex shrink-0 border-hairline max-lg:min-h-0 max-lg:flex-1 lg:w-66 lg:border-r",
              paneShown(pane === "places"),
            )}
          >
            <FilesSidebar
              places={places.data}
              path=""
              recent={recent}
              canWrite={false}
              trashActive
              onNavigate={showFolder}
              onBookmarksChange={() => {}}
              onDropPaths={() => {}}
              onDropFiles={() => {}}
            />
          </div>

          <div
            className={cn(
              "@container relative flex min-h-0 min-w-0 flex-1 flex-col",
              paneShown(pane === "trash"),
            )}
          >
            {trash.error && !trash.data ? (
              <ErrorState
                error={trash.error}
                onRetry={trash.refresh}
                what="the Trash"
                className="m-3"
              />
            ) : !trash.data ? (
              <LoadingRows rows={6} className="p-3" />
            ) : items.length === 0 ? (
              <EmptyState
                icon={Trash}
                title="The Trash is empty"
                description={`What you delete in Files comes here, ${keepSentence(trash.data.keepDays)}.`}
                action={
                  <Button size="sm" variant="outline" asChild>
                    <Link href="/files">
                      <FolderOpen className="size-3.5" />
                      Files
                    </Link>
                  </Button>
                }
                className="m-3 flex-1"
              />
            ) : (
              <div className="min-h-0 flex-1 overflow-auto">
                <Table aria-label="Trash">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead className={COLUMN.from}>From</TableHead>
                      <TableHead>Deleted</TableHead>
                      <TableHead className={COLUMN.kept}>Kept</TableHead>
                      <TableHead className={cn(COLUMN.size, "text-right")}>Size</TableHead>
                      <TableHead className="w-0">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((item) => (
                      <TrashRow
                        key={item.id}
                        item={item}
                        now={now}
                        active={item.id === chosenId}
                        canWrite={canWrite}
                        canDestruct={canDestruct}
                        onChoose={() => choose(item, true)}
                        onRestore={() => void restore(item)}
                        onDelete={() => deleteForGood(item)}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </div>

          <div
            className={cn(
              "flex shrink-0 flex-col border-hairline max-lg:min-h-0 max-lg:flex-1 lg:max-xl:hidden xl:w-80 xl:border-l",
              paneShown(pane === "details"),
            )}
          >
            <TrashDetails
              item={chosen}
              now={now}
              keepDays={trash.data?.keepDays}
              canWrite={canWrite}
              canDestruct={canDestruct}
              onRestore={(item) => void restore(item)}
              onDelete={deleteForGood}
              onShowFolder={showFolder}
            />
          </div>
        </div>
        <PaneBar label="Trash" stops={PANES} active={pane} onPick={setPane} />
      </div>
      {dialog}
      {conflicts.dialog}
    </Page>
  )
}

function TrashRow({
  item,
  now,
  active,
  canWrite,
  canDestruct,
  onChoose,
  onRestore,
  onDelete,
}: {
  item: TrashItem
  now: number
  active: boolean
  canWrite: boolean
  canDestruct: boolean
  onChoose: () => void
  onRestore: () => void
  onDelete: () => void
}) {
  return (
    <TableRow
      data-trash-item={item.id}
      data-state={active ? "selected" : undefined}
      className={cn("group/row h-11 [&>td]:py-0", active && "bg-accent hover:bg-accent")}
      onClick={onChoose}
    >
      <TableCell className="max-w-0 min-w-40">
        <button
          type="button"
          title={item.originalPath || item.name}
          aria-current={active || undefined}
          onClick={(event) => {
            event.stopPropagation()
            onChoose()
          }}
          className="flex w-full min-w-0 items-center gap-2.5 rounded-sm text-left focus-ring"
        >
          <FileIcon entry={asEntry(item)} className="size-5 shrink-0" />
          <span className="min-w-0 truncate text-body">{item.name}</span>
        </button>
      </TableCell>
      <TableCell className={cn(COLUMN.from, "max-w-0 min-w-32")}>
        <span
          className="block truncate font-mono text-xs text-muted-foreground"
          title={cameFrom(item)}
        >
          {cameFrom(item)}
        </span>
      </TableCell>
      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
        {deletedWhen(item, now)}
      </TableCell>
      <TableCell className={cn(COLUMN.kept, "text-xs whitespace-nowrap text-muted-foreground")}>
        {keptShort(item, now)}
      </TableCell>
      <TableCell
        className={cn(
          COLUMN.size,
          "numeric text-right text-xs whitespace-nowrap text-muted-foreground",
        )}
      >
        {itemSize(item)}
      </TableCell>
      <TableCell className="w-0 pr-2">
        <RowActions revealGroup="row">
          {canWrite && item.originalPath && (
            <IconAction
              label={`Restore ${item.name}`}
              tooltip="Restore"
              onClick={(e) => {
                e.stopPropagation()
                onRestore()
              }}
            >
              <RotateCounterClockwise />
            </IconAction>
          )}
          {canDestruct && (
            <IconAction
              label={`Delete ${item.name} permanently`}
              tooltip="Delete permanently"
              className="text-destructive"
              onClick={(e) => {
                e.stopPropagation()
                onDelete()
              }}
            >
              <Trash />
            </IconAction>
          )}
        </RowActions>
      </TableCell>
    </TableRow>
  )
}

function TrashDetails({
  item,
  now,
  keepDays,
  canWrite,
  canDestruct,
  onRestore,
  onDelete,
  onShowFolder,
}: {
  item: TrashItem | null
  now: number
  keepDays: number | undefined
  canWrite: boolean
  canDestruct: boolean
  onRestore: (item: TrashItem) => void
  onDelete: (item: TrashItem) => void
  onShowFolder: (path: string) => void
}) {
  if (!item) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
        <Trash aria-hidden className="size-8 text-faint" />
        <p className="text-body text-muted-foreground">
          Pick something to see where it came from. Everything here is {keepSentence(keepDays)}.
        </p>
      </div>
    )
  }
  return (
    <div
      className="flex min-h-0 flex-1 flex-col overflow-auto"
      aria-label={`${item.name} in the Trash`}
    >
      <div className="flex flex-col items-center gap-3 border-b border-hairline px-4 py-6">
        <FileIcon entry={asEntry(item)} className="size-14" />
        <p className="max-w-full truncate text-center text-body font-medium" title={item.name}>
          {item.name}
        </p>
        <p className="text-center text-xs text-muted-foreground">
          Deleted {deletedWhen(item, now)}, {keptFor(item, now)}
        </p>
        <div className="flex flex-wrap justify-center gap-2">
          {canWrite && item.originalPath && (
            <Button size="sm" onClick={() => onRestore(item)}>
              <RotateCounterClockwise className="size-3.5" />
              Restore
            </Button>
          )}
          {canDestruct && (
            <Button
              size="sm"
              variant="outline"
              className="text-destructive"
              onClick={() => onDelete(item)}
            >
              <Trash className="size-3.5" />
              Delete permanently
            </Button>
          )}
        </div>
      </div>
      <PaneSection title="Details">
        <DetailList size="body" className="gap-y-2.5">
          <Detail label="From" mono>
            {item.originalPath ? (
              <button
                type="button"
                className="text-left break-all focus-ring hover:underline"
                onClick={() => onShowFolder(cameFrom(item))}
              >
                {item.originalPath}
              </button>
            ) : (
              "not recorded"
            )}
          </Detail>
          <Detail label="Kind">
            {item.isSymlink ? `Link to ${item.linkTarget ?? "?"}` : item.isDir ? "Folder" : "File"}
          </Detail>
          <Detail label="Size">{itemSize(item)}</Detail>
          <Detail label="Owner">
            {item.owner}:{item.group}
          </Detail>
          <Detail label="Mode" mono>
            {item.modeOctal}
          </Detail>
          {item.replaced && <Detail label="Why">Replaced by a move, a copy or a restore</Detail>}
          <Detail label="Held in" mono>
            {item.folder}
          </Detail>
        </DetailList>
      </PaneSection>
    </div>
  )
}
