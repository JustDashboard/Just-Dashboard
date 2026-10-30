export type ClipMode = "cut" | "copy"
export type HeldClip = { mode: ClipMode; paths: string[] }

/**
 * Our own clipboard format. Written beside the plain-text paths so a paste —
 * in this tab or another dashboard tab — knows the clipboard still holds what
 * the listing put there, and not something copied elsewhere since.
 */
export const CLIP_TYPE = "application/x-just-dashboard-files"

type Readable = Pick<DataTransfer, "getData" | "types"> & { files: { length: number } }
type Writable = Pick<DataTransfer, "setData">

export function writeClip(data: Writable, clip: HeldClip) {
  // Paths as text are what a terminal or editor on the desktop can use; the
  // browser cannot hand a server file to the desktop as a file.
  data.setData("text/plain", clip.paths.join("\n"))
  data.setData(CLIP_TYPE, JSON.stringify(clip))
}

export type Pasted =
  | { kind: "paths"; clip: HeldClip }
  | { kind: "files" }
  | { kind: "locations" }
  | { kind: "text"; text: string }
  | null

/**
 * What a paste should do. The system clipboard is the newest thing the
 * operator copied, so it decides: our format means paths from a listing,
 * files mean an upload, and text that is not the paths we wrote becomes a
 * file of its own. `held` covers a browser that dropped our format but kept
 * the text.
 */
export function readPaste(data: Readable | null, held: HeldClip | null): Pasted {
  if (!data) return held ? { kind: "paths", clip: held } : null
  const types = Array.from(data.types ?? [])
  if (types.includes(CLIP_TYPE)) {
    const clip = parseClip(data.getData(CLIP_TYPE))
    if (clip) return { kind: "paths", clip }
  }
  if (data.files.length > 0 || types.includes("Files")) return { kind: "files" }
  const text = types.includes("text/plain") ? data.getData("text/plain") : ""
  if (held && text === held.paths.join("\n")) return { kind: "paths", clip: held }
  // A browser that cannot read a copied file hands over only where it lives;
  // its bytes cannot be read from a web page, so this is not text to save.
  const uris = types.includes("text/uri-list") ? data.getData("text/uri-list") : text
  if (onlyFileUris(uris)) return { kind: "locations" }
  if (text.length > 0) return { kind: "text", text }
  return held ? { kind: "paths", clip: held } : null
}

function onlyFileUris(list: string): boolean {
  const lines = list
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"))
  return lines.length > 0 && lines.every((line) => line.startsWith("file://"))
}

function parseClip(raw: string): HeldClip | null {
  try {
    const value = JSON.parse(raw) as Partial<HeldClip>
    if (
      (value.mode === "cut" || value.mode === "copy") &&
      Array.isArray(value.paths) &&
      value.paths.length > 0 &&
      value.paths.every((p) => typeof p === "string" && p.startsWith("/"))
    ) {
      return { mode: value.mode, paths: value.paths }
    }
  } catch {}
  return null
}

const stamp = (at: Date) => {
  const two = (n: number) => String(n).padStart(2, "0")
  return (
    `${at.getFullYear()}-${two(at.getMonth() + 1)}-${two(at.getDate())} ` +
    `${two(at.getHours())}.${two(at.getMinutes())}.${two(at.getSeconds())}`
  )
}

/**
 * A pasted screenshot arrives as "image.png" every time; naming each by when
 * it was pasted keeps a second paste from asking about the first.
 */
export function pastedFileName(name: string, type: string, at: Date): string {
  if (!/^image\.\w+$/i.test(name) || !type.startsWith("image/")) return name
  return `Pasted image ${stamp(at)}${name.slice(name.lastIndexOf("."))}`
}

export function pastedTextName(at: Date): string {
  return `Pasted text ${stamp(at)}.txt`
}
