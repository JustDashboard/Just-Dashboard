"use client"

import { useLayoutEffect, useMemo, useRef } from "react"
import type { FileEntry } from "@/lib/types"
import type { FileActions } from "@/components/files/file-actions"
import type { DropMode } from "@/components/files/dnd"

/**
 * What a row or a tile asks of the page, each taking the entry it is about.
 *
 * One object for the life of the page. The page used to hand every row six
 * closures and a set of actions made for that row on that render, so a row's
 * props were new each time the page drew, and a click, an arrow key or a frame
 * of a drag drew every row of the folder again: 160 to 190 ms of a 521-row
 * folder on a fast machine. With these, a row's props change only when the row
 * does, and `memo` leaves the rest alone.
 */
export type EntryHandlers = {
  onToggle: (entry: FileEntry, checked: boolean) => void
  onSelect: (entry: FileEntry, event: React.MouseEvent) => void
  onFocusEntry: (entry: FileEntry) => void
  onOpen: (entry: FileEntry) => void
  onDragStart: (entry: FileEntry, event: React.DragEvent) => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
  /** Made when a verb is pressed, not for every row on every render. */
  actionsFor: (entry: FileEntry) => FileActions
}

/**
 * The page's handlers behind an object that never changes. Each call reaches
 * the handler of the latest render, so what it does is what the page would do
 * now: the selection it reads is the current one, not the one of the render
 * the row last drew in.
 */
export function useEntryHandlers(latest: EntryHandlers): EntryHandlers {
  const now = useRef(latest)
  // Before the browser can deliver an event to what this render drew.
  useLayoutEffect(() => {
    now.current = latest
  })
  return useMemo<EntryHandlers>(
    () => ({
      onToggle: (entry, checked) => now.current.onToggle(entry, checked),
      onSelect: (entry, event) => now.current.onSelect(entry, event),
      onFocusEntry: (entry) => now.current.onFocusEntry(entry),
      onOpen: (entry) => now.current.onOpen(entry),
      onDragStart: (entry, event) => now.current.onDragStart(entry, event),
      onDropPaths: (paths, dir, mode) => now.current.onDropPaths(paths, dir, mode),
      onDropFiles: (transfer, dir) => now.current.onDropFiles(transfer, dir),
      actionsFor: (entry) => now.current.actionsFor(entry),
    }),
    [],
  )
}
