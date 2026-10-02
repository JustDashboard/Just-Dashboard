"use client"

import { useState } from "react"
import { bytes } from "@/lib/format"
import { Tag } from "@/components/tag"
import { bytesLabel, isBinary } from "@/components/database/redis/bytes"
import type { RedisReply } from "@/components/database/redis/types"

/** How many elements of one array or map are drawn before the rest are asked for. */
const PAGE = 100

/** The word redis-cli prints before a value that is not a string. */
function Kind({ children }: { children: React.ReactNode }) {
  return <span className="text-muted-foreground/70 select-none">({children}) </span>
}

/**
 * A reply, drawn as what it is.
 *
 * The server says what each part of a reply is — a string, an integer, nil,
 * a list of more replies, a map — and it is drawn as that, the way redis-cli
 * writes it: strings in quotes, a number named as one, elements numbered, a
 * nested list indented under its position. An error reply is a result like
 * any other and is the one thing drawn in red. Where the server cut a reply
 * short, the cut is said with how much there was.
 */
export function Reply({ reply, depth = 0 }: { reply: RedisReply; depth?: number }) {
  switch (reply.type) {
    case "nil":
      return <span className="text-muted-foreground">(nil)</span>
    case "status":
      return <span>{reply.value}</span>
    case "error":
      return (
        <span className="text-destructive">
          <span className="select-none">(error) </span>
          {reply.value}
        </span>
      )
    case "integer":
      return (
        <span>
          <Kind>integer</Kind>
          <span className="numeric">{String(reply.value)}</span>
        </span>
      )
    case "double":
      return (
        <span>
          <Kind>double</Kind>
          <span className="numeric">{String(reply.value)}</span>
        </span>
      )
    case "bignumber":
      return (
        <span>
          <Kind>big number</Kind>
          <span className="numeric">{reply.value}</span>
        </span>
      )
    case "boolean":
      return <span className="text-(--tag-violet)">({String(reply.value)})</span>
    case "string":
      return <Text reply={reply} />
    case "array":
      return <Items reply={reply} depth={depth} />
    case "map":
      return <Entries reply={reply} depth={depth} />
  }
}

function Cut({ shown, length, unit }: { shown: number; length?: number; unit: string }) {
  return (
    <span className="font-sans text-hint text-muted-foreground">
      {length === undefined
        ? "The reply was cut here."
        : `${shown.toLocaleString()} of ${unit === "bytes" ? bytes(length) : `${length.toLocaleString()} ${unit}`} shown.`}
    </span>
  )
}

function Text({ reply }: { reply: Extract<RedisReply, { type: "string" }> }) {
  const binary = isBinary(reply.value)
  const text = bytesLabel(reply.value)
  // What the server prints over several lines — INFO, CLIENT LIST — reads as
  // the lines it is, not as one quoted string full of \r\n.
  const lines = typeof reply.value === "string" && /\n/.test(reply.value)
  return (
    <span className="min-w-0">
      {lines ? (
        <span className="block break-all whitespace-pre-wrap">
          {(reply.value as string).replace(/\r\n/g, "\n").replace(/\n$/, "")}
        </span>
      ) : (
        <span className="break-all whitespace-pre-wrap">&quot;{text}&quot;</span>
      )}
      {binary && <Tag className="ml-2 align-middle">bytes</Tag>}
      {reply.truncated && (
        <span className="ml-2">
          <Cut shown={text.length} length={reply.length} unit="bytes" />
        </span>
      )}
    </span>
  )
}

const index = (n: number, of: number) => String(n).padStart(String(of).length, " ")

function Items({ reply, depth }: { reply: Extract<RedisReply, { type: "array" }>; depth: number }) {
  const [shown, setShown] = useState(PAGE)
  const items = reply.items
  if (items.length === 0) return <span className="text-muted-foreground">(empty array)</span>
  return (
    <span className="block min-w-0">
      {items.slice(0, shown).map((item, at) => (
        <span key={at} className="flex min-w-0 items-start gap-2">
          <span className="shrink-0 whitespace-pre text-muted-foreground/70 select-none">
            {index(at + 1, items.length)})
          </span>
          <span className="min-w-0 flex-1">
            <Reply reply={item} depth={depth + 1} />
          </span>
        </span>
      ))}
      <More shown={shown} total={items.length} onMore={() => setShown(shown + PAGE)} />
      {reply.truncated && <Cut shown={items.length} length={reply.length} unit="elements" />}
    </span>
  )
}

function Entries({ reply, depth }: { reply: Extract<RedisReply, { type: "map" }>; depth: number }) {
  const [shown, setShown] = useState(PAGE)
  const entries = reply.entries
  if (entries.length === 0) return <span className="text-muted-foreground">(empty map)</span>
  return (
    <span className="block min-w-0">
      {entries.slice(0, shown).map((entry, at) => (
        <span key={at} className="flex min-w-0 items-start gap-2">
          <span className="shrink-0 whitespace-pre text-muted-foreground/70 select-none">
            {index(at + 1, entries.length)}#
          </span>
          <span className="shrink-0">
            <Reply reply={entry.key} depth={depth + 1} />
          </span>
          <span className="shrink-0 text-muted-foreground/70 select-none">=&gt;</span>
          <span className="min-w-0 flex-1">
            <Reply reply={entry.value} depth={depth + 1} />
          </span>
        </span>
      ))}
      <More shown={shown} total={entries.length} onMore={() => setShown(shown + PAGE)} />
      {reply.truncated && <Cut shown={entries.length} length={reply.length} unit="entries" />}
    </span>
  )
}

function More({ shown, total, onMore }: { shown: number; total: number; onMore: () => void }) {
  if (total <= shown) return null
  return (
    <button
      type="button"
      onClick={onMore}
      className="block rounded-sm font-sans text-hint text-muted-foreground focus-ring hover:text-foreground"
    >
      Show {Math.min(PAGE, total - shown).toLocaleString()} more of {total.toLocaleString()}
    </button>
  )
}
