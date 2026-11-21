"use client"

import { useEffect, useRef } from "react"
import { get } from "@/lib/api"
import type { FileContent } from "@/lib/types"
import { useFileFollow } from "./live-folder"

/**
 * An open file that changed on disk. With nothing unsaved the editor takes
 * the new contents quietly (`onReload`); with unsaved edits nothing is
 * replaced, and the editor's own conflict handling is raised (`onConflict`),
 * the same one a refused Save would raise. A change that is only the editor's
 * own save arriving back is neither.
 */
export function useEditorFollow({
  path,
  file,
  draft,
  saving,
  onReload,
  onConflict,
}: {
  path: string | null
  file: FileContent | undefined
  draft: string
  saving: boolean
  onReload: (file: FileContent) => void
  onConflict: () => void
}) {
  const now = useRef({ file, draft, saving, onReload, onConflict })
  useEffect(() => {
    now.current = { file, draft, saving, onReload, onConflict }
  })
  const reading = useRef<"idle" | "reading" | "again">("idle")

  useFileFollow(path && file && !file.binary ? path : null, () => {
    if (!path) return
    if (reading.current !== "idle") {
      reading.current = "again"
      return
    }
    reading.current = "reading"
    const read = () =>
      get<FileContent>("/files/read", { path })
        .then((disk) => {
          const { file: open, draft: typed, saving: busy } = now.current
          if (!open || busy || disk.binary) return
          const same = disk.revision
            ? disk.revision === open.revision
            : disk.content === open.content
          if (same) return
          if (typed === open.content || typed === disk.content) now.current.onReload(disk)
          else now.current.onConflict()
        })
        .catch(() => undefined)
        .finally(() => {
          if (reading.current === "again") {
            reading.current = "reading"
            void read()
          } else reading.current = "idle"
        })
    void read()
  })
}
