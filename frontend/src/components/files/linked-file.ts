"use client"

import { useCallback } from "react"
import type { FileEntry } from "@/lib/types"
import { baseOf, cleanPath, parentOf } from "@/components/files/media"

/**
 * What following a link in a rendered file does: a folder is entered, and a
 * file is shown where it lives — chosen in its folder's listing, or, when the
 * viewer is open on the same folder, brought up in the viewer.
 *
 * The listing has not read a folder it has not been to, so the entry chosen
 * there is a stand-in carrying the path; the page looks it up by path once the
 * listing arrives, as it does for `?entry=` in an address.
 */
export function useLinkedFile({
  path,
  viewable,
  viewing,
  navigate,
  setViewing,
  setActive,
}: {
  path: string | null
  viewable: FileEntry[]
  viewing: number | null
  navigate: (path: string) => void
  setViewing: (index: number | null) => void
  setActive: (active: { dir: string; entry: FileEntry | null }) => void
}) {
  return useCallback(
    (target: string, folder: boolean) => {
      if (folder) {
        navigate(target)
        return
      }
      const dir = parentOf(target)
      if (dir === cleanPath(path ?? "")) {
        const index = viewable.findIndex((entry) => entry.path === target)
        if (viewing !== null && index >= 0) setViewing(index)
      } else {
        navigate(dir)
      }
      setActive({ dir, entry: standIn(target) })
    },
    [path, viewable, viewing, navigate, setViewing, setActive],
  )
}

function standIn(path: string): FileEntry {
  return {
    name: baseOf(path),
    path,
    isDir: false,
    isSymlink: false,
    size: 0,
    mode: "",
    modeOctal: "",
    modified: "",
    owner: "",
    group: "",
    uid: 0,
    gid: 0,
  }
}
