import { postForm } from "@/lib/api"

export const TERMINAL_IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"] as const

type ClipboardItemLike = {
  kind: string
  type: string
  getAsFile: () => File | null
}

export type ImageChoice =
  { kind: "none" } | { kind: "image"; file: File } | { kind: "unsupported"; mime: string }

type PasteEventLike = {
  clipboardData: { items: ArrayLike<ClipboardItemLike> } | null
  preventDefault: () => void
}

const supported = new Set<string>(TERMINAL_IMAGE_TYPES)
const terminalUploadPath =
  /^\/tmp\/just-dashboard\/[0-9a-f]{16}\/clipboard-[0-9a-f]{32}\.(png|jpg|webp)$/

/** Find an image without touching text-only clipboard data. */
export function chooseClipboardImage(items: ArrayLike<ClipboardItemLike>): ImageChoice {
  let unsupported = ""
  for (const item of Array.from(items)) {
    if (item.kind !== "file" || !item.type.toLowerCase().startsWith("image/")) continue
    const mime = item.type.toLowerCase()
    if (!supported.has(mime)) {
      unsupported ||= mime
      continue
    }
    const file = item.getAsFile()
    if (file) return { kind: "image", file }
    unsupported ||= mime
  }
  return unsupported ? { kind: "unsupported", mime: unsupported } : { kind: "none" }
}

/**
 * Intercept only image clipboard data. Returning false is the text-paste
 * contract: the caller must leave the event entirely to xterm and the browser.
 */
export function interceptClipboardImagePaste(
  event: PasteEventLike,
  onImage: (file: File) => void,
  onUnsupported: (mime: string) => void,
): boolean {
  if (!event.clipboardData) return false
  const choice = chooseClipboardImage(event.clipboardData.items)
  if (choice.kind === "none") return false
  event.preventDefault()
  if (choice.kind === "image") onImage(choice.file)
  else onUnsupported(choice.mime)
  return true
}

/** Choose the first supported dragged image, or explain why none qualifies. */
export function chooseDroppedImage(files: ArrayLike<File>): ImageChoice {
  const list = Array.from(files)
  for (const file of list) {
    if (supported.has(file.type.toLowerCase())) return { kind: "image", file }
  }
  return list.length ? { kind: "unsupported", mime: list[0].type || "unknown" } : { kind: "none" }
}

export type TerminalImageUpload = {
  path: string
  name: string
  mime: string
  size: number
}

export async function uploadTerminalImage(
  sessionID: string,
  file: File,
  signal?: AbortSignal,
): Promise<TerminalImageUpload> {
  const form = new FormData()
  form.append("file", file)
  return postForm<TerminalImageUpload>(
    `/terminal/${encodeURIComponent(sessionID)}/clipboard`,
    form,
    { signal },
  )
}

/** Send exactly the returned path: no synthetic key event and no Enter. */
export function insertTerminalPath(path: string, send: (data: string) => void) {
  if (!terminalUploadPath.test(path)) {
    throw new Error("The server returned an invalid temporary file path")
  }
  send(path)
}

export function formatUploadSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(bytes < 10 * 1024 * 1024 ? 1 : 0)} MB`
}
