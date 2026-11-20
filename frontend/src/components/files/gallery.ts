import type { FileEntry } from "@/lib/types"
import { mediaKind, parentOf } from "@/components/files/media"

/**
 * The arithmetic of walking a folder of media: the viewer's gallery strip and
 * slideshow, and the audio queue. Pure, so `gallery.test.js` holds it.
 */

/** A queued track: what the player needs after its folder is no longer the one listed. */
export type Track = { path: string; name: string; modified: string; folder: string }

/**
 * The queue a track starts: the audio files of its folder in the listing's
 * order, from the first, with the chosen one current. A track that is not in
 * the listing (it was opened from elsewhere) is a queue of one.
 */
export function queueFrom(
  entry: Pick<FileEntry, "path" | "name" | "modified">,
  entries: Pick<FileEntry, "path" | "name" | "modified" | "isDir">[],
): { tracks: Track[]; index: number } {
  const track = (e: Pick<FileEntry, "path" | "name" | "modified">): Track => ({
    path: e.path,
    name: e.name,
    modified: e.modified,
    folder: parentOf(e.path),
  })
  const audio = entries.filter((e) => !e.isDir && mediaKind(e.name) === "audio")
  const index = audio.findIndex((e) => e.path === entry.path)
  if (index < 0) return { tracks: [track(entry)], index: 0 }
  return { tracks: audio.map(track), index }
}

/** A track's length or position as a clock reads it: 0:07, 3:45, 1:02:03. */
export function clockTime(seconds: number): string {
  const total = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = String(total % 60).padStart(2, "0")
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`
}

/** How far through the folder the viewer is, in words: "Picture 3 of 40", "3 of 12". */
export function positionWords(index: number, items: Pick<FileEntry, "name">[]): string | null {
  if (items.length < 2) return null
  const pictures = items.every((item) => mediaKind(item.name) === "image")
  return `${pictures ? "Picture " : ""}${index + 1} of ${items.length}`
}

/**
 * The slice of a folder the strip draws: the current item and up to `radius`
 * either side, so a folder of two thousand pictures is a strip of eighty.
 */
export function stripWindow(count: number, index: number, radius = 40): [number, number] {
  if (count <= radius * 2 + 1) return [0, count]
  const start = Math.min(Math.max(0, index - radius), count - (radius * 2 + 1))
  return [start, start + radius * 2 + 1]
}

/** The pictures fetched ahead of the one on screen: the next two, and the one before. */
export function aheadOf(items: Pick<FileEntry, "name">[], index: number): number[] {
  return [index + 1, index + 2, index - 1].filter(
    (i) => i >= 0 && i < items.length && mediaKind(items[i].name) === "image",
  )
}

/** The slideshow's next picture after `index`, or nothing at the end of the folder. */
export function nextPicture(items: Pick<FileEntry, "name">[], index: number): number | null {
  for (let i = index + 1; i < items.length; i++) {
    if (mediaKind(items[i].name) === "image") return i
  }
  return null
}

/** Whether a folder has pictures enough for a slideshow. */
export function hasSlideshow(items: Pick<FileEntry, "name">[]): boolean {
  return items.filter((item) => mediaKind(item.name) === "image").length > 1
}

/**
 * A finger's movement across the stage as a step through the folder: a
 * mostly sideways move of 48px or more, leftwards for the next.
 */
export function swipeStep(dx: number, dy: number): -1 | 0 | 1 {
  if (Math.abs(dx) < 48 || Math.abs(dx) < Math.abs(dy) * 1.5) return 0
  return dx < 0 ? 1 : -1
}
