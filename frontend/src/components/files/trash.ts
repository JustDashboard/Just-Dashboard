import { bytes, plural } from "@/lib/format"

/**
 * The Trash's API types and the words the page says about it. Kept beside the
 * components rather than in `lib/types.ts`: the Trash is the Files page's own.
 */

/** One thing in the Trash: `GET /files/trash`'s `items`. */
export type TrashItem = {
  id: string
  name: string
  /** Where it was deleted from; empty for an item whose record was lost. */
  originalPath: string
  deletedAt: string
  deletedBy?: string
  /** When the background sweep removes it; absent while items are kept until emptied. */
  expiresAt?: string
  /** It went in because a move, a copy or a restore replaced it. */
  replaced?: boolean
  isDir: boolean
  isSymlink: boolean
  linkTarget?: string
  size: number
  sizeKnown: boolean
  sizePartial?: boolean
  owner: string
  group: string
  mode: string
  modeOctal: string
  /** The Trash folder holding it, which says which filesystem. */
  folder: string
}

export type TrashListing = {
  items: TrashItem[]
  bytes: number
  /** At least one size is still being measured, so `bytes` is a floor. */
  partial: boolean
  /** 0 keeps everything until it is emptied. */
  keepDays: number
}

/** `GET /files/trash/check`: would this go to the Trash, and if not, why. */
export type TrashPlan = {
  path: string
  available: boolean
  reason?: string
  folder?: string
  keepDays?: number
}

/** `DELETE /files/delete`'s answer. */
export type DeleteResult = { trashed: boolean; path: string; item?: TrashItem }

/** A move's or a copy's answer when Replace put something in the Trash. */
export type ReplaceResult =
  | {
      /** What now has the destination's name, for an undo to check it still does. */
      identity?: string
      replaced?: { item?: TrashItem; forGood?: string }
    }
  | undefined

/** `POST /files/trash/restore`'s answer. */
export type Restored = { path: string; name: string; replaced?: TrashItem }

export const TRASH_ROUTE = "/files/trash"

const DAY = 24 * 60 * 60 * 1000

/** How long an item stays, in words: "kept 29 more days", "goes today", "kept until emptied". */
export function keptFor(item: Pick<TrashItem, "expiresAt">, now: number): string {
  if (!item.expiresAt) return "kept until emptied"
  const left = Date.parse(item.expiresAt) - now
  if (!Number.isFinite(left)) return "kept until emptied"
  if (left < DAY) return "goes today"
  const days = Math.floor(left / DAY)
  return days === 1 ? "kept 1 more day" : `kept ${days} more days`
}

/** The same, as a short column value: "29 days", "today", "until emptied". */
export function keptShort(item: Pick<TrashItem, "expiresAt">, now: number): string {
  if (!item.expiresAt) return "until emptied"
  const left = Date.parse(item.expiresAt) - now
  if (!Number.isFinite(left)) return "until emptied"
  if (left < DAY) return "today"
  return plural(Math.floor(left / DAY), "day")
}

/** An item's size in words, or what is known of it. */
export function itemSize(item: Pick<TrashItem, "size" | "sizeKnown" | "sizePartial">): string {
  if (!item.sizeKnown) return "measuring"
  return item.sizePartial ? `at least ${bytes(item.size)}` : bytes(item.size)
}

/** What the Trash holds, in one sentence: "3 items, 1.2 GB" or "Nothing in the Trash". */
export function trashSentence(listing: Pick<TrashListing, "items" | "bytes" | "partial">): string {
  if (listing.items.length === 0) return "Nothing in the Trash"
  const size = listing.partial ? `at least ${bytes(listing.bytes)}` : bytes(listing.bytes)
  return `${plural(listing.items.length, "item")}, ${size}`
}

/** How long the Trash keeps things, as a sentence ending: "each kept 30 days". */
export function keepSentence(keepDays: number | undefined): string {
  if (!keepDays) return "kept until the Trash is emptied"
  return `kept ${keepDays === 1 ? "1 day" : `${keepDays} days`}`
}

/** An age in words, as a person says it: "just now", "5 minutes ago", "2 hours ago", "3 days ago". */
export function ageWords(at: string, now: number): string {
  const then = Date.parse(at)
  if (!Number.isFinite(then)) return ""
  return secondsAgo(Math.max(0, now - then) / 1000)
}

/** The same, for an age already measured in seconds. */
export function secondsAgo(seconds: number): string {
  const minutes = seconds / 60
  if (minutes < 1) return "just now"
  if (minutes < 60) return `${plural(Math.floor(minutes), "minute")} ago`
  const hours = minutes / 60
  if (hours < 24) return `${plural(Math.floor(hours), "hour")} ago`
  return `${plural(Math.floor(hours / 24), "day")} ago`
}

/** Who and when, for an item: "2 hours ago by ada". */
export function deletedWhen(item: Pick<TrashItem, "deletedAt" | "deletedBy">, now: number): string {
  const when = ageWords(item.deletedAt, now)
  return item.deletedBy ? `${when} by ${item.deletedBy}` : when
}

/** The folder an item came from, for "from /etc/nginx". */
export function cameFrom(item: Pick<TrashItem, "originalPath">): string {
  const path = item.originalPath
  if (!path) return "an unrecorded place"
  const i = path.lastIndexOf("/")
  return i <= 0 ? "/" : path.slice(0, i)
}

/** What `GET /files/find` adds when the name index answered it. */
export type FindSource = {
  source?: "index" | "walk"
  index?: {
    builtAt: string
    ageSeconds: number
    entries: number
    complete: boolean
    building: boolean
  }
}

/** How old the index that answered Find is, in words: "index from 2 hours ago". */
export function indexAge(found: FindSource | undefined): string {
  if (found?.source !== "index" || !found.index) return ""
  return `index from ${secondsAgo(found.index.ageSeconds)}`
}
