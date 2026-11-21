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

/** Archives the backend can extract in place. */
const EXTRACTABLE_RE = /\.(zip|tar|tar\.gz|tgz)$/i
export const isArchive = (name: string) => EXTRACTABLE_RE.test(name)

/** The raw URL for a file, cache-busted by its own modification time. */
export function rawUrl(path: string, modified?: string) {
  return downloadUrl("/files/raw", { path, v: modified ? Date.parse(modified) : undefined })
}

/**
 * A picture's thumbnail is a small copy the server makes once and keeps
 * (`GET /files/thumbnail`, `internal/thumbnail`), for every picture in a
 * format it decodes (PNG, JPEG, GIF), however large: a folder of forty 12 MB
 * photographs costs forty copies of tens of kilobytes. The address carries the
 * file's modification time, as the raw route's does, so a changed picture is a
 * new copy. `THUMBNAILED` mirrors `thumbnail.Decodable`.
 *
 * Any other picture (WebP, SVG, AVIF, an icon) is still the file itself,
 * drawn small by the browser, up to `THUMBNAIL_MAX_BYTES`: honest for the
 * sizes a server holds of those, and the cap keeps a listing from pulling a
 * 40 MB scan over a VPN to draw it at 28 pixels. A copy the server cannot make
 * (a canvas over its pixel limit, bytes that are not the picture the name
 * says) falls back to the same.
 *
 * A video costs far less than its size suggests: the browser asks for its
 * header and the first frame with range requests, which the raw route honours,
 * so a poster for a two-gigabyte recording is a few hundred kilobytes.
 */
export const THUMBNAIL_MAX_BYTES = 4 << 20

const THUMBNAILED = new Set(["png", "jpg", "jpeg", "gif"])

/** Whether the server makes a small copy of this picture. */
export const hasServerThumbnail = (name: string) => THUMBNAILED.has(extensionOf(name))

/** The small copy's address, cache-busted by the file's modification time. */
export function thumbnailUrl(path: string, modified?: string) {
  return downloadUrl("/files/thumbnail", { path, v: modified ? Date.parse(modified) : undefined })
}

export function thumbnailKind(entry: Pick<FileEntry, "name" | "isDir" | "size">) {
  if (entry.isDir || entry.size === 0) return null
  const kind = mediaKind(entry.name)
  if (kind === "image") {
    return hasServerThumbnail(entry.name) || entry.size <= THUMBNAIL_MAX_BYTES ? "image" : null
  }
  if (kind === "video") return "video"
  return null
}

/**
 * Where a picture's thumbnail is drawn from: the server's copy, or the file
 * itself for a format it does not decode or after its copy failed — and only
 * while the file is small enough to draw that way.
 */
export function thumbnailSrc(
  entry: Pick<FileEntry, "name" | "path" | "size" | "modified">,
  copyFailed = false,
): string | null {
  if (hasServerThumbnail(entry.name) && !copyFailed) return thumbnailUrl(entry.path, entry.modified)
  return entry.size <= THUMBNAIL_MAX_BYTES ? rawUrl(entry.path, entry.modified) : null
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

/**
 * The disk a path is on: the mount that holds it and is deepest, as `df` would
 * answer. Nothing while no frame has carried mounts yet, or for no path.
 */
export function mountOf<M extends { mountpoint: string }>(
  mounts: readonly M[] | null | undefined,
  path: string | null | undefined,
): M | undefined {
  if (!path || !Array.isArray(mounts)) return undefined
  let best: M | undefined
  for (const m of mounts) {
    if (isWithin(path, m.mountpoint) && (!best || m.mountpoint.length > best.mountpoint.length)) {
      best = m
    }
  }
  return best
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
