/*
 * The file manager: listings, contents and previews, places and bookmarks,
 * finds, sizes and checksums.
 */

export type FileEntry = {
  name: string
  path: string
  size: number
  mode: string
  modeOctal: string
  isDir: boolean
  isSymlink: boolean
  linkTarget?: string
  linkBroken?: boolean
  modified: string
  owner: string
  group: string
  uid: number
  gid: number
  mimeHint?: string
}

export type FileListing = {
  path: string
  parent: string
  entries: FileEntry[]
  roots: string[]
}

export type FileContent = {
  revision?: string
  path: string
  content: string
  size: number
  language: string
  binary: boolean
  modeOctal: string
}

/** One click on a row: what the thing is, without loading it. */
export type FilePreview = {
  path: string
  name: string
  kind: "dir" | "text" | "image" | "video" | "audio" | "pdf" | "archive" | "binary"
  mime?: string
  size: number
  modified: string
  modeOctal: string
  owner?: string
  group?: string
  language?: string
  text?: string
  lines?: number
  truncated?: boolean
  editable: boolean
  width?: number
  height?: number
  entries?: { name: string; size: number; isDir: boolean }[]
  entryCount?: number
  moreEntries?: boolean
  archiveError?: string
  childCount?: number
  dirCount?: number
  fileCount?: number
  isSymlink?: boolean
  symlinkTarget?: string
  linkBroken?: boolean
}

export type FilePlace = {
  name: string
  path: string
  kind: "home" | "root" | "user" | "notable"
  hint?: string
}

export type FileBookmark = { path: string; name?: string }

export type FilePlaces = {
  home: string
  roots: string[]
  places: FilePlace[]
  bookmarks: FileBookmark[]
  /** The colour each labelled folder is drawn in, by resolved path. Absent before 0.7.0. */
  colours?: Record<string, string>
  /** The server-wide colour chosen for every folder; absent until one is chosen. */
  defaultColour?: string
}

export type FileFindHit = {
  path: string
  name: string
  rel: string
  dir: string
  isDir: boolean
  size: number
  modified: string
  score: number
  /** UTF-16 offsets into `name`, for the highlight. */
  matches?: number[]
}

export type FileFindResult = {
  root: string
  hits: FileFindHit[]
  /** The walk stopped early, on its budget or a cap. */
  truncated: boolean
  /** How many matched before the best `limit` were kept. */
  total?: number
  visited: number
  elapsedMs: number
}

export type FileUsage = {
  path: string
  bytes: number
  files: number
  dirs: number
  truncated: boolean
  elapsedMs: number
  largest?: { name: string; path: string; bytes: number; isDir: boolean }[]
}

export type FileChecksum = { path: string; algo: string; sum: string; size: number }
