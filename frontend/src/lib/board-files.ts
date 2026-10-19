/**
 * A drawing's images live beside its scene on the server (`/boards/{id}/files`)
 * rather than inside it: a scene carrying them as data URLs reached the 16 MiB
 * limit after a few screenshots, and every save sent all of them again.
 */

/** The types the server stores, which are the images Excalidraw itself accepts. */
export const BOARD_FILE_TYPES = new Set([
  "image/svg+xml",
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/bmp",
  "image/x-icon",
  "image/avif",
])

type FileElement = { type: string; fileId?: string | null; isDeleted?: boolean }
type HeldFile = { mimeType: string; dataURL: string }

/** The files the drawing shows: those of its image elements still on it. */
export function shownFileIds(elements: readonly FileElement[]): Set<string> {
  const ids = new Set<string>()
  for (const element of elements) {
    if (element.type === "image" && element.fileId && !element.isDeleted) ids.add(element.fileId)
  }
  return ids
}

/** The shown files this browser has to read from the server before it can draw them. */
export function filesToFetch(
  elements: readonly FileElement[],
  held: Readonly<Record<string, unknown>>,
): string[] {
  return [...shownFileIds(elements)].filter((id) => !(id in held))
}

/**
 * The shown files the server does not have yet and this browser can send.
 * A file of a type the server refuses stays in the browser, where Excalidraw
 * keeps drawing it, rather than failing every save after it.
 */
export function filesToUpload(
  elements: readonly FileElement[],
  held: Readonly<Record<string, HeldFile>>,
  stored: ReadonlySet<string>,
): string[] {
  return [...shownFileIds(elements)].filter(
    (id) => !stored.has(id) && held[id] && BOARD_FILE_TYPES.has(held[id].mimeType),
  )
}

/**
 * A data URL's bytes. Decoded here rather than through `fetch`: the page's
 * `connect-src` names the dashboard alone, and a `data:` fetch is held to it.
 */
export function dataURLBytes(dataURL: string): Uint8Array<ArrayBuffer> {
  const comma = dataURL.indexOf(",")
  const head = dataURL.slice(0, comma)
  const body = dataURL.slice(comma + 1)
  if (!head.endsWith(";base64")) return new TextEncoder().encode(decodeURIComponent(body))
  const binary = atob(body)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** A file the server sent back, as the data URL Excalidraw draws from. */
export function blobDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}
