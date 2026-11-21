import { ago } from "@/lib/format"
import type { FileVersion, FileVersions } from "./versions-types"

/**
 * The words and addresses of a file's history (`version-history.tsx`), kept
 * apart from the components so `versions.test.js` can read them.
 */

/** The history is a view of the editor page: these keys carry it in the address. */
export const HISTORY_KEYS = ["view", "version", "against"] as const

/** The editor page's address with the history open, on `version` when one is picked. */
export function historySearch(
  search: string | URLSearchParams,
  version?: string | null,
  against?: "current" | "previous",
): string {
  const params = new URLSearchParams(search)
  params.set("view", "history")
  if (version) params.set("version", version)
  else params.delete("version")
  if (against === "previous") params.set("against", "previous")
  else params.delete("against")
  return `/files/editor?${params}`
}

/** The editor page's address with the history closed: back to the editor. */
export function editorSearch(search: string | URLSearchParams): string {
  const params = new URLSearchParams(search)
  for (const key of HISTORY_KEYS) params.delete(key)
  return `/files/editor?${params}`
}

/** The address of the history for a file, from anywhere: the details pane's line. */
export function historyHref(path: string, root?: string): string {
  const params = new URLSearchParams({ path })
  if (root) params.set("root", root)
  params.set("view", "history")
  return `/files/editor?${params}`
}

/**
 * What the editor is keyed on: the address without the history's keys, so
 * opening the history and picking versions never remounts the editor and its
 * unsaved draft stays where it was.
 */
export function editorKey(search: string | URLSearchParams): string {
  const params = new URLSearchParams(search)
  for (const key of HISTORY_KEYS) params.delete(key)
  return params.toString()
}

/**
 * Whether an address is the same file's editor as the one open, the history
 * in or out: moving there is not leaving, and the guard against losing a
 * draft lets it by.
 */
export function sameEditorFile(href: string, current: string): boolean {
  try {
    const to = new URL(href, current)
    const from = new URL(current)
    return (
      to.origin === from.origin &&
      to.pathname === "/files/editor" &&
      from.pathname === "/files/editor" &&
      editorKey(to.search) === editorKey(from.search)
    )
  } catch {
    return false
  }
}

/** The version a pick names, or the newest when none is picked or it is gone. */
export function pickedVersion(history: FileVersions | undefined, id: string | null) {
  if (!history || history.versions.length === 0) return undefined
  return history.versions.find((v) => v.id === id) ?? history.versions[0]
}

/** The version before `version`: the next older row of the list. */
export function previousVersion(history: FileVersions | undefined, version: FileVersion) {
  if (!history) return undefined
  const at = history.versions.findIndex((v) => v.id === version.id)
  return at < 0 ? undefined : history.versions[at + 1]
}

const SERVICES: Record<string, string> = {
  local: "this server",
  s3: "S3",
  b2: "Backblaze B2",
  sftp: "SFTP",
  rclone: "rclone",
}

/** A row's first line: what this content is, in a few words. */
export function versionName(version: FileVersion): string {
  if (version.kind === "backup") return version.backup?.job || "Backup"
  const by = version.by ? ` by ${version.by}` : ""
  if (version.reason === "displaced") return `Saved${by}`
  if (version.outside) return "Changed outside the dashboard"
  switch (version.reason) {
    case "restore":
      return `Before a restore${by}`
    case "image":
      return `Before a picture save${by}`
    case "upload":
      return `Before an upload${by}`
    default:
      return `Before a save${by}`
  }
}

/**
 * A row's second line: when, and the fact that tells it from its
 * neighbours. `ago` is the time in words, given rather than computed so the
 * line is the same in a test as on the page.
 */
export function versionLine(version: FileVersion, ago: string): string {
  if (version.kind === "backup") {
    const where = SERVICES[version.backup?.targetKind ?? "local"] ?? "a backup"
    return `Backup ${ago} · ${version.backup?.local ? "kept on this server" : `in ${where}`}`
  }
  if (version.reason === "displaced") return `${ago} · then changed outside`
  if (version.outside) return `Found ${ago}${version.by ? ` by ${version.by}'s save` : ""}`
  return `Replaced ${ago}`
}

/** The details pane's line about a file's history. */
export function versionsSentence(history: FileVersions, ago: (iso: string) => string): string {
  const count = history.versions.length
  // The pane's label already says "Versions", so the line is the count.
  const counted = String(count)
  if (count === 0) {
    if (history.tooLarge) return "Too large to keep earlier versions"
    if (!history.keeping) return "Earlier versions are not kept"
    return history.lastSave ? `Saved ${ago(history.lastSave.at)}` : "No earlier versions yet"
  }
  const last = history.lastSave?.at ?? history.versions[0].at
  const tail = history.tooLarge ? ", too large to keep more" : `, last changed ${ago(last)}`
  return counted + tail
}

/** When something was, in its largest unit: "5m ago", "2d ago". */
export function whenAgo(iso: string, now = Date.now()): string {
  return ago(Date.parse(iso), now)
}
