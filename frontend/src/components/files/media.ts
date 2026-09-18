import { downloadUrl } from "@/lib/api"
import type { FileEntry } from "@/lib/types"

/**
 * What the browser can show of a file, decided by its name.
 *
 * This mirrors the closed list the server is willing to serve inline
 * (`files.MediaType`): a name that is not on it never gets a thumbnail or a
 * viewer, because the request for its bytes would be refused. The two lists
 * are kept the same by hand, and the browser test for the raw route is what
 * notices if they drift.
 */
export type MediaKind = "image" | "video" | "audio" | "pdf"

const IMAGE = new Set(["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "svg"])
const VIDEO = new Set(["mp4", "webm", "ogv", "mov", "mkv"])
const AUDIO = new Set(["mp3", "wav", "ogg", "flac", "m4a", "aac"])

export function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".")
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ""
}

export function mediaKind(name: string): MediaKind | null {
  const ext = extensionOf(name)
  if (IMAGE.has(ext)) return "image"
  if (VIDEO.has(ext)) return "video"
  if (AUDIO.has(ext)) return "audio"
  if (ext === "pdf") return "pdf"
  return null
}

export const isImage = (name: string) => mediaKind(name) === "image"
export const isVideo = (name: string) => mediaKind(name) === "video"

/** Archives the backend can extract in place. */
const EXTRACTABLE_RE = /\.(zip|tar|tar\.gz|tgz)$/i
export const isArchive = (name: string) => EXTRACTABLE_RE.test(name)

/** The raw URL for a file, cache-busted by its own modification time. */
export function rawUrl(path: string, modified?: string) {
  return downloadUrl("/files/raw", { path, v: modified ? Date.parse(modified) : undefined })
}

/**
 * The thumbnail is the file itself, drawn small by the browser. That is honest
 * for the sizes a server holds — an icon, a screenshot, a photograph — and it
 * needs no thumbnail cache to go stale or to fill a disk this page exists to
 * keep tidy. The cap on which images are tried is what keeps a listing from
 * pulling a 40 MB scan over a VPN just to draw it at 28 pixels.
 *
 * A video costs far less than its size suggests: the browser asks for its
 * header and the first frame with range requests, which the raw route honours,
 * so a poster for a two-gigabyte recording is a few hundred kilobytes.
 */
export const THUMBNAIL_MAX_BYTES = 4 << 20

export function thumbnailKind(entry: Pick<FileEntry, "name" | "isDir" | "size">) {
  if (entry.isDir || entry.size === 0) return null
  const kind = mediaKind(entry.name)
  if (kind === "image") return entry.size <= THUMBNAIL_MAX_BYTES ? "image" : null
  if (kind === "video") return "video"
  return null
}

/** A non-colliding name for putting a second copy of something in a folder. */
export function uniqueName(name: string, taken: Set<string>, suffix = "copy"): string {
  if (!taken.has(name)) return name
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  let candidate = `${stem} ${suffix}${ext}`
  let n = 2
  while (taken.has(candidate)) candidate = `${stem} ${suffix} ${n++}${ext}`
  return candidate
}

/**
 * The desktop's answer to a name that is taken — `photo (2).jpg` — for an
 * upload or a move that asked to keep both. `uniqueName` is the other
 * convention, for a duplicate made on purpose next to its original.
 */
export function numberedName(name: string, taken: Set<string>): string {
  if (!taken.has(name)) return name
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  let n = 2
  while (taken.has(`${stem} (${n})${ext}`)) n++
  return `${stem} (${n})${ext}`
}

export const cleanPath = (p: string) => p.replace(/\/+$/, "") || "/"
export const joinPath = (dir: string, name: string) =>
  `${cleanPath(dir)}/${name}`.replace(/\/{2,}/g, "/")
export const parentOf = (p: string) => {
  const c = cleanPath(p)
  const i = c.lastIndexOf("/")
  return i <= 0 ? "/" : c.slice(0, i)
}
export const baseOf = (p: string) => cleanPath(p).split("/").pop() || "/"

/** Whether `path` is `dir` or lives somewhere under it. */
export function isWithin(path: string, dir: string): boolean {
  const d = cleanPath(dir)
  return d === "/" || path === d || path.startsWith(d + "/")
}

/** The MIME type the drag payload of a set of paths travels as. */
export const DRAG_PATHS = "application/x-jd-paths"

export function dragCarriesPaths(transfer: DataTransfer): boolean {
  return Array.from(transfer.types).includes(DRAG_PATHS)
}

export function dragCarriesFiles(transfer: DataTransfer): boolean {
  return Array.from(transfer.types).includes("Files")
}

export function readDraggedPaths(transfer: DataTransfer): string[] {
  try {
    const parsed = JSON.parse(transfer.getData(DRAG_PATHS)) as unknown
    return Array.isArray(parsed) ? parsed.filter((p): p is string => typeof p === "string") : []
  } catch {
    return []
  }
}
