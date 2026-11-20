import type { FileEntry, FileListing } from "@/lib/types"

/** One batch from `GET /files/watch`: the rows that changed, and the names that went. */
export type FolderChange = { entries: FileEntry[]; removed: string[] }

/** What the page knows that the last listing does not: a name's new row, or null for gone. */
export type FolderPatches = ReadonlyMap<string, FileEntry | null>

/**
 * A listing with what arrived since it was read laid over it. Rows nobody
 * changed are the same objects as before, so a row that did not change is not
 * a new row to React or to the selection.
 */
export function applyPatches(listing: FileListing, patches: FolderPatches): FileListing {
  if (patches.size === 0) return listing
  const entries: FileEntry[] = []
  const seen = new Set<string>()
  for (const entry of listing.entries) {
    seen.add(entry.name)
    const patch = patches.get(entry.name)
    if (patch === undefined) entries.push(entry)
    else if (patch !== null) entries.push(sameEntry(entry, patch) ? entry : patch)
  }
  for (const [name, patch] of patches) {
    if (patch && !seen.has(name)) entries.push(patch)
  }
  return { ...listing, entries }
}

/** Patches as held while following: each with the order it arrived in. */
export type HeldPatches = ReadonlyMap<string, { entry: FileEntry | null; seq: number }>

/** A batch folded into the patches already held, numbered after `seq`. */
export function foldChange(
  held: HeldPatches,
  change: FolderChange,
  seq: number,
): Map<string, { entry: FileEntry | null; seq: number }> {
  const next = new Map(held)
  for (const entry of change.entries ?? []) next.set(entry.name, { entry, seq })
  for (const name of change.removed ?? []) next.set(name, { entry: null, seq })
  return next
}

/** The held patches that arrived after `floor`, as `applyPatches` takes them. */
export function patchesAfter(held: HeldPatches, floor: number): FolderPatches {
  const out = new Map<string, FileEntry | null>()
  for (const [name, patch] of held) if (patch.seq > floor) out.set(name, patch.entry)
  return out
}

function sameEntry(a: FileEntry, b: FileEntry) {
  return (
    a.path === b.path &&
    a.size === b.size &&
    a.modified === b.modified &&
    a.modeOctal === b.modeOctal &&
    a.mode === b.mode &&
    a.isDir === b.isDir &&
    a.isSymlink === b.isSymlink &&
    a.linkTarget === b.linkTarget &&
    a.linkBroken === b.linkBroken &&
    a.uid === b.uid &&
    a.gid === b.gid &&
    a.owner === b.owner &&
    a.group === b.group
  )
}
