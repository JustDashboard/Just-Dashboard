import { cleanPath } from "./media"

export type FolderHistory = { id: string; paths: string[]; index: number }

export function folderHref(path: string, current: string): string {
  const url = new URL(current)
  url.searchParams.set("path", cleanPath(path))
  // An investigation's initial selection belongs to its original folder.
  url.searchParams.delete("entry")
  return `${url.pathname}${url.search}${url.hash}`
}

export function visitFolder(history: FolderHistory, path: string): FolderHistory {
  const next = cleanPath(path)
  if (history.paths[history.index] === next) return history
  const paths = [...history.paths.slice(0, history.index + 1), next]
  return { ...history, paths, index: paths.length - 1 }
}

/** Repeated letters cycle through names; a longer prefix narrows the match. */
export function matchName(names: string[], query: string, active: number): number {
  const prefix = query.toLocaleLowerCase()
  for (let step = 1; step <= names.length; step++) {
    const index = (Math.max(-1, active) + step) % names.length
    if (names[index].toLocaleLowerCase().startsWith(prefix)) return index
  }
  return -1
}
