"use client"

import { useEffect, useRef, useState } from "react"
import {
  DRAG_PATHS,
  dragCarriesFiles,
  dragCarriesPaths,
  readDraggedPaths,
  isWithin,
} from "@/components/files/media"

// Browsers hide getData during dragover. Keep this tab's native drag payload
// available so a folder never advertises a drop into itself or its children.
let currentPaths: string[] = []

export type DropMode = "move" | "copy"

export type DropHandlers = {
  onDragEnter: (event: React.DragEvent) => void
  onDragOver: (event: React.DragEvent) => void
  onDragLeave: (event: React.DragEvent) => void
  onDrop: (event: React.DragEvent) => void
}

/**
 * Makes an element a place things can be dropped: a folder row, a tree node,
 * a breadcrumb, the listing itself.
 *
 * Two payloads arrive here. Paths dragged from elsewhere on the page move (or
 * copy, with Ctrl or Alt held, the way a desktop does) into the folder; files
 * dragged in from the operator's own machine upload into it. A target says
 * which it takes, and ignores the other so the event carries on to whatever
 * is behind it.
 *
 * The enter/leave counter is what stops the highlight flickering as the
 * pointer crosses the children of the target: the browser fires `dragenter`
 * for the child before `dragleave` for the parent, so the count never touches
 * zero while the pointer is still inside. An accepting target stops the
 * events at its edge, so a folder row under the pointer lights up on its own
 * rather than together with the listing around it.
 */
export function useDropTarget({
  dir,
  paths = true,
  files = true,
  onDropPaths,
  onDropFiles,
}: {
  /** Where a drop lands. `null` makes the element inert. */
  dir: string | null
  paths?: boolean
  files?: boolean
  onDropPaths?: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles?: (transfer: DataTransfer, dir: string) => void
}): { over: boolean; handlers: DropHandlers } {
  const [over, setOver] = useState(false)
  const depth = useRef(0)

  // A drag that ends somewhere else — cancelled with Escape, dropped on the
  // desktop — sends no `dragleave` to the target it was last over, which
  // would leave the highlight on for good. Only the lit target listens.
  useEffect(() => {
    if (!over) return
    const reset = () => {
      depth.current = 0
      setOver(false)
    }
    window.addEventListener("dragend", reset)
    window.addEventListener("drop", reset)
    return () => {
      window.removeEventListener("dragend", reset)
      window.removeEventListener("drop", reset)
    }
  }, [over])

  const accepts = (transfer: DataTransfer) =>
    dir !== null &&
    !(dragCarriesPaths(transfer) && currentPaths.some((path) => isWithin(dir, path))) &&
    ((paths && !!onDropPaths && dragCarriesPaths(transfer)) ||
      (files && !!onDropFiles && dragCarriesFiles(transfer)))

  const mode = (event: React.DragEvent): DropMode =>
    event.ctrlKey || event.altKey ? "copy" : "move"

  const handlers: DropHandlers = {
    onDragEnter: (event) => {
      if (!accepts(event.dataTransfer)) return
      event.preventDefault()
      event.stopPropagation()
      depth.current += 1
      setOver(true)
    },
    onDragOver: (event) => {
      if (!accepts(event.dataTransfer)) return
      event.preventDefault()
      event.stopPropagation()
      event.dataTransfer.dropEffect = dragCarriesPaths(event.dataTransfer) ? mode(event) : "copy"
    },
    onDragLeave: (event) => {
      if (!accepts(event.dataTransfer)) return
      event.stopPropagation()
      depth.current = Math.max(0, depth.current - 1)
      if (depth.current === 0) setOver(false)
    },
    onDrop: (event) => {
      if (!accepts(event.dataTransfer) || dir === null) return
      event.preventDefault()
      event.stopPropagation()
      depth.current = 0
      setOver(false)
      if (dragCarriesPaths(event.dataTransfer)) {
        const dragged = readDraggedPaths(event.dataTransfer)
        if (dragged.length > 0) onDropPaths?.(dragged, dir, mode(event))
        return
      }
      onDropFiles?.(event.dataTransfer, dir)
    },
  }
  return { over, handlers }
}

/** Starts a drag carrying paths, from a row or a tile. */
export function startPathDrag(event: React.DragEvent, paths: string[]) {
  currentPaths = paths
  const reset = () => {
    currentPaths = []
    window.removeEventListener("dragend", reset)
    window.removeEventListener("drop", reset)
  }
  window.addEventListener("dragend", reset)
  window.addEventListener("drop", reset)
  event.dataTransfer.setData(DRAG_PATHS, JSON.stringify(paths))
  // A plain-text copy too, so a path dropped into a terminal or an editor
  // arrives as the path rather than as nothing.
  event.dataTransfer.setData("text/plain", paths.join("\n"))
  event.dataTransfer.effectAllowed = "copyMove"

  // A compact stack keeps a multi-file drag legible; cloning a whole table
  // row otherwise produces a ghost as wide as the listing with no count.
  const preview = document.createElement("div")
  preview.className =
    "pointer-events-none fixed flex max-w-64 items-center gap-2 rounded-md border border-hairline bg-popover px-3 py-2 text-body text-foreground"
  preview.style.left = "-1000px"
  preview.style.top = "0"
  const icon = event.currentTarget
    .querySelector("[data-folder], svg.size-full, img")
    ?.cloneNode(true) as Element | undefined
  if (icon) {
    icon.setAttribute("class", "relative inline-flex size-6 shrink-0")
    preview.append(icon)
  }
  const label = document.createElement("span")
  label.className = "truncate"
  label.textContent = paths.length > 1 ? `${paths.length} items` : paths[0].split("/").pop() || "/"
  preview.append(label)
  document.body.append(preview)
  event.dataTransfer.setDragImage(preview, 16, 16)
  setTimeout(() => preview.remove(), 0)
}
