import {
  CodeBracket,
  Hash,
  Layers,
  ListOrdered,
  ListUnordered,
  Puzzle,
  Rss,
  TextFormat,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { Tag } from "@/components/tag"

/**
 * The legend of key types: one type, one hue, one glyph — in the rail's
 * chips and rows, on a key's head, in the composition bar of the memory
 * analysis and beside a top key.
 *
 * The hues are the `--tag-*` set, which sit at one lightness so no type
 * reads louder than another; none of them is a reading of state. A type the
 * dashboard has no reader for — a module's own — keeps the muted voice and
 * the module glyph rather than borrowing a neighbour's colour.
 */
export type RedisKind = "string" | "hash" | "list" | "set" | "zset" | "stream" | "json" | "other"

export type RedisKindInfo = {
  id: RedisKind
  /** The word on a chip and a tag. */
  label: string
  /** What the server calls it, for `?type=`. */
  wire: string
  /** One of what it holds, for "1,200 fields". */
  member: string
  members: string
  icon: Icon
  /** The hue as a text utility, a fill utility and the bare colour. */
  text: string
  fill: string
  color: string
}

const kind = (
  id: RedisKind,
  label: string,
  wire: string,
  member: string,
  members: string,
  icon: Icon,
  hue: string | null,
): RedisKindInfo => ({
  id,
  label,
  wire,
  member,
  members,
  icon,
  text: hue ? HUE_TEXT[hue] : "text-muted-foreground",
  fill: hue ? HUE_FILL[hue] : "bg-muted-foreground",
  color: hue ? `var(--tag-${hue})` : "var(--muted-foreground)",
})

// Written out so the stylesheet holds them: a class assembled at run time is
// one Tailwind never saw.
const HUE_TEXT: Record<string, string> = {
  blue: "text-(--tag-blue)",
  violet: "text-(--tag-violet)",
  green: "text-(--tag-green)",
  amber: "text-(--tag-amber)",
  pink: "text-(--tag-pink)",
  cyan: "text-(--tag-cyan)",
  slate: "text-(--tag-slate)",
}
const HUE_FILL: Record<string, string> = {
  blue: "bg-(--tag-blue)",
  violet: "bg-(--tag-violet)",
  green: "bg-(--tag-green)",
  amber: "bg-(--tag-amber)",
  pink: "bg-(--tag-pink)",
  cyan: "bg-(--tag-cyan)",
  slate: "bg-(--tag-slate)",
}

export const REDIS_KINDS: Record<RedisKind, RedisKindInfo> = {
  string: kind("string", "String", "string", "byte", "bytes", TextFormat, "blue"),
  hash: kind("hash", "Hash", "hash", "field", "fields", Hash, "violet"),
  list: kind("list", "List", "list", "element", "elements", ListOrdered, "green"),
  set: kind("set", "Set", "set", "member", "members", ListUnordered, "amber"),
  zset: kind("zset", "Sorted set", "zset", "member", "members", Layers, "pink"),
  stream: kind("stream", "Stream", "stream", "entry", "entries", Rss, "cyan"),
  json: kind("json", "JSON", "ReJSON-RL", "", "", CodeBracket, "slate"),
  other: kind("other", "Module type", "", "", "", Puzzle, null),
}

/** The types a key browser filters by and creates, in the order they are offered. */
export const KIND_ORDER: RedisKind[] = ["string", "hash", "list", "set", "zset", "stream", "json"]

/** The legend's entry for a type as the server names it. */
export function kindOf(type: string): RedisKindInfo {
  if (type === "ReJSON-RL" || type === "json") return REDIS_KINDS.json
  return Object.hasOwn(REDIS_KINDS, type) && type !== "other"
    ? REDIS_KINDS[type as RedisKind]
    : REDIS_KINDS.other
}

/** The word for a type: the legend's, or a module's own name as it gave it. */
export function kindLabel(type: string): string {
  const info = kindOf(type)
  return info.id === "other" ? type || info.label : info.label
}

/** A type's glyph in its hue: the mark before a key's name. */
export function KindMark({ type, className }: { type: string; className?: string }) {
  const info = kindOf(type)
  const Glyph = info.icon
  return <Glyph aria-hidden className={cn("size-3.5 shrink-0", info.text, className)} />
}

/** A type as a word in its hue: the property a key's head states. */
export function KindTag({ type, className }: { type: string; className?: string }) {
  const info = kindOf(type)
  return (
    <Tag data-kind={info.id} className={cn(info.text, className)}>
      {kindLabel(type)}
    </Tag>
  )
}

/** "1,200 fields", "50 bytes": a key's size in its type's own word. */
export function sizeOf(type: string, size: number): string {
  const info = kindOf(type)
  if (!info.member) return ""
  return `${size.toLocaleString()} ${size === 1 ? info.member : info.members}`
}
