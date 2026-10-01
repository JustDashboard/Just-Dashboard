"use client"

import { useMemo, useState } from "react"
import { Copy, Download, FloppyDisk, Pencil } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes as formatBytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useMemoryState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import { CodeEditor } from "@/components/code-editor"
import { PaneFooter } from "@/components/panel"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { redisMembers, redisRawUrl, redisWrite } from "@/components/database/redis/api"
import {
  bytesId,
  fromBytes,
  hexLines,
  isBinary,
  joinBytes,
  looksLikeJson,
  parseHex,
  textShape,
  toBytes,
  toHex,
} from "@/components/database/redis/bytes"
import { EditorStrip, saveFrom } from "@/components/database/redis/keys/editor-parts"
import type { KeyEditorProps } from "@/components/database/redis/keys/key-pane"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisBytes, RedisMembers } from "@/components/database/redis/types"
import { usePaged } from "@/components/database/redis/use-paged"
import { useRowWindow } from "@/components/database/redis/use-row-window"

/** How many bytes of a string one read asks for. */
const WINDOW = 262_144
/** The largest value offered as editable hex: past it the box is slower than the download. */
const HEX_EDIT_LIMIT = 65_536
const HEX_ROW = 20

type View = "text" | "json" | "hex"

/** A value being edited: as text, or as the hex of its bytes. */
type Draft = { as: "text" | "hex"; text: string }

type Windows = { parts: RedisBytes[]; length: number }

function addWindow(held: Windows | undefined, page: RedisMembers): Windows {
  const part = page.string?.value ?? ""
  return { parts: held ? [...held.parts, part] : [part], length: page.length }
}

/** Hex as a person reads and types it: sixteen bytes a line, a gap after eight. */
function hexText(value: Uint8Array): string {
  const pairs = toHex(value).match(/../g) ?? []
  const lines: string[] = []
  for (let i = 0; i < pairs.length; i += 16) {
    lines.push(`${pairs.slice(i, i + 8).join(" ")}  ${pairs.slice(i + 8, i + 16).join(" ")}`.trim())
  }
  return lines.join("\n")
}

/**
 * A string: text, JSON or bytes — three readings of one value.
 *
 * Redis strings are bytes, and the reading is chosen by what they are: text
 * opens as text, text that is a JSON document opens highlighted, and bytes
 * that are not text open as a hex dump and are never put in a text box.
 * Neither is text that a box would not hand back as it got it — a NUL, an
 * escape, a stray carriage return: it is valid UTF-8 and still bytes to
 * whoever edits it. Lines that all end in CR LF are edited as lines and
 * saved with the endings they had. A large value arrives in windows; it can
 * be read as it loads and edited once it is all here, because saving a part
 * would replace the whole with it.
 *
 * Saving writes the value and nothing else: the key keeps its expiry. What
 * is typed and not yet saved is held for the tab, so opening another key and
 * coming back does not lose it.
 */
export function StringEditor({ redis, name, meta, epoch, onChanged }: KeyEditorProps) {
  const { id, target, db, engine, canWrite } = redis
  const read = usePaged(
    {
      fetch: (cursor, signal) => redisMembers(target, name, { cursor, count: WINDOW }, signal),
      merge: addWindow,
      next: (page) => (page.done ? null : page.cursor),
    },
    JSON.stringify([target.id, target.db ?? null, bytesId(name)]),
    { epoch },
  )

  const value = useMemo(() => (read.state ? joinBytes(read.state.parts) : undefined), [read.state])
  const raw = useMemo(() => (value === undefined ? undefined : toBytes(value)), [value])
  const binary = value !== undefined && isBinary(value)
  const stored = typeof value === "string" ? value : ""
  const shape = useMemo(() => textShape(stored), [stored])
  // Bytes either way: what is not text, and text a box would rewrite.
  const opaque = binary || !shape.plain
  // What a text view holds: one kind of line break, as a box keeps them.
  const text = useMemo(() => (shape.crlf ? stored.replace(/\r\n/g, "\n") : stored), [shape, stored])
  const whole = read.done

  // Held in memory only, by key: a value may be a secret, and a draft of one
  // is not written to the browser's storage.
  const [drafts, setDrafts] = useMemoryState<Record<string, Draft>>(
    `databases.${id}.redis.drafts`,
    {},
  )
  const slot = `${db ?? "default"}:${bytesId(name)}`
  const draft = drafts[slot] as Draft | undefined
  const setDraft = (next: Draft | null) =>
    setDrafts((held) => {
      const rest = Object.fromEntries(Object.entries(held).filter(([key]) => key !== slot))
      if (next === null) return rest
      // The newest few: a tab left open all day does not keep every value it touched.
      const kept = Object.entries(rest).slice(-11)
      return { ...Object.fromEntries(kept), [slot]: next }
    })

  const [picked, setPicked] = useState<View | null>(null)
  const natural: View = opaque ? "hex" : looksLikeJson(text) ? "json" : "text"
  const view: View = draft?.as === "hex" ? "hex" : opaque ? "hex" : (picked ?? natural)

  const [busy, setBusy] = useState(false)
  const editable = canWrite && whole
  const shownText = draft?.as === "text" ? draft.text : text
  const typedText = draft?.as === "text" ? draft.text.replace(/\r\n/g, "\n") : undefined
  const dirty =
    draft !== undefined &&
    (draft.as === "text"
      ? typedText !== text
      : raw !== undefined && draft.text.replace(/\s+/g, "") !== toHex(raw))
  const typedBytes = draft?.as === "hex" ? parseHex(draft.text) : undefined
  const invalidJson = view === "json" && shownText.trim() !== "" && !validJson(shownText)

  const save = async () => {
    if (!draft || !dirty || busy) return
    const next: RedisBytes | null =
      typedText !== undefined
        ? // The line endings the value had are the ones it keeps.
          shape.crlf
          ? typedText.replace(/\n/g, "\r\n")
          : typedText
        : typedBytes
          ? fromBytes(typedBytes)
          : null
    if (next === null) {
      notify.error("Not saved", undefined, {
        description: "Hex is pairs of digits 0–9 and a–f; one is missing its other half.",
      })
      return
    }
    setBusy(true)
    try {
      // No `ttl`: the server keeps the key's expiry across a value write.
      await redisWrite(target, { key: name, type: "string", value: next })
      setDraft(null)
      read.reload()
      onChanged()
      notify.success("Value saved")
    } catch (err) {
      notify.error("Could not save the value", err)
    } finally {
      setBusy(false)
    }
  }

  const format = (indent: number) => {
    try {
      setDraft({ as: "text", text: JSON.stringify(JSON.parse(shownText), null, indent) })
    } catch {
      notify.error("Not formatted", undefined, { description: "The value is not valid JSON." })
    }
  }

  if (read.error && !read.state) {
    return <ReadError error={read.error} onRetry={read.reload} className="m-3" />
  }

  const tab = (id: View, label: string, disabled?: boolean) => (
    <button
      key={id}
      type="button"
      aria-pressed={view === id}
      disabled={disabled}
      onClick={() => setPicked(id)}
      className={cn(
        tabClasses(view === id, "h-10"),
        "disabled:pointer-events-none disabled:opacity-40",
      )}
    >
      {label}
    </button>
  )

  return (
    <>
      <EditorStrip className="py-0">
        <div role="group" aria-label="How the value is read" className="-ml-3 flex self-stretch">
          {tab("text", "Text", opaque || draft?.as === "hex")}
          {tab("json", "JSON", opaque || draft?.as === "hex")}
          {tab("hex", "Hex")}
        </div>
        {/* One group, so the commands that do not fit beside the views wrap
            together and stay against the right edge. */}
        <div className="ml-auto flex flex-wrap items-center justify-end gap-x-2 gap-y-1 py-1.5">
          {dirty && (
            <span className="text-hint font-medium text-(--git-modified)">Unsaved changes</span>
          )}
          {draft !== undefined && (
            <Button size="xs" variant="ghost" disabled={busy} onClick={() => setDraft(null)}>
              {dirty ? "Revert" : "Stop editing"}
            </Button>
          )}
          {view === "json" && editable && (
            <>
              <Button size="xs" variant="ghost" disabled={invalidJson} onClick={() => format(2)}>
                Format
              </Button>
              <Button size="xs" variant="ghost" disabled={invalidJson} onClick={() => format(0)}>
                Minify
              </Button>
            </>
          )}
          {view === "hex" &&
            editable &&
            draft === undefined &&
            raw &&
            raw.length <= HEX_EDIT_LIMIT && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => setDraft({ as: "hex", text: hexText(raw) })}
              >
                <Pencil />
                Edit bytes
              </Button>
            )}
          {!opaque && (
            <Button
              size="xs"
              variant="ghost"
              onClick={() => void copyText(shownText, "Value copied")}
            >
              <Copy />
              Copy
            </Button>
          )}
          {engine.can("valueDownload") && (
            <Button size="xs" variant="ghost" onClick={() => saveFrom(redisRawUrl(target, name))}>
              <Download />
              Download
            </Button>
          )}
          {canWrite && (
            <Button size="xs" pending={busy} disabled={!dirty || !whole} onClick={save}>
              <FloppyDisk />
              Save
            </Button>
          )}
        </div>
      </EditorStrip>

      {!whole && read.state && (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground">
          <span className="min-w-0 flex-1">
            The first {formatBytes(raw?.length ?? 0)} of {formatBytes(read.state.length)} are here.
            {canWrite && " The value can be edited once all of it is."}
          </span>
          <Button size="xs" variant="outline" pending={read.loadingMore} onClick={read.more}>
            Load more
          </Button>
        </div>
      )}

      {value === undefined ? (
        <div className="space-y-2 p-3" aria-hidden>
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-4" style={{ width: `${86 - ((i * 19) % 46)}%` }} />
          ))}
        </div>
      ) : view === "hex" ? (
        draft?.as === "hex" ? (
          <textarea
            aria-label="The value's bytes, as hex"
            value={draft.text}
            spellCheck={false}
            autoComplete="off"
            aria-invalid={typedBytes === null || undefined}
            onChange={(event) => setDraft({ as: "hex", text: event.target.value })}
            onKeyDown={(event) => saveKey(event, save)}
            className="min-h-0 flex-1 resize-none bg-transparent p-3 font-mono text-xs leading-5 focus-ring-inset outline-none"
          />
        ) : (
          <HexDump bytes={raw ?? new Uint8Array()} />
        )
      ) : view === "json" ? (
        <CodeEditor
          className="min-h-0 flex-1"
          language="json"
          value={shownText}
          readOnly={!editable}
          wordWrap
          onChange={(next) => setDraft({ as: "text", text: next })}
          onSave={save}
        />
      ) : (
        <textarea
          aria-label="The value, as text"
          value={shownText}
          readOnly={!editable}
          spellCheck={false}
          autoComplete="off"
          onChange={(event) => setDraft({ as: "text", text: event.target.value })}
          onKeyDown={(event) => saveKey(event, save)}
          className="min-h-0 flex-1 resize-none bg-transparent p-3 font-mono text-xs leading-relaxed focus-ring-inset outline-none"
        />
      )}

      <PaneFooter className="gap-x-4 gap-y-1 text-hint text-muted-foreground">
        <span className="numeric">
          {binary
            ? "Bytes, not text"
            : !shape.plain
              ? "Control characters, read as bytes"
              : shape.crlf
                ? "UTF-8 text, CR LF line endings"
                : "UTF-8 text"}{" "}
          · {(read.state?.length ?? meta.length).toLocaleString()} bytes
        </span>
        {invalidJson && <span className="text-warning">Not valid JSON</span>}
        {draft?.as === "hex" && typedBytes === null && (
          <span className="text-warning">Hex is pairs of digits 0–9 and a–f</span>
        )}
        <span className="min-w-0 flex-1" />
        {canWrite && <span>Saving keeps the key&apos;s expiry</span>}
      </PaneFooter>
    </>
  )
}

function validJson(text: string): boolean {
  try {
    JSON.parse(text)
    return true
  } catch {
    return false
  }
}

/** Ctrl+S or Cmd+S in a field saves the value rather than the page. */
function saveKey(event: React.KeyboardEvent, save: () => void) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
    event.preventDefault()
    save()
  }
}

/** Bytes as offset, hex and text, sixteen to a line; only the lines on screen are drawn. */
function HexDump({ bytes }: { bytes: Uint8Array }) {
  const count = Math.ceil(bytes.length / 16)
  const { attach, onScroll, slice, height, offset } = useRowWindow(count, HEX_ROW)
  const lines = hexLines(bytes, slice.start * 16, slice.end * 16)
  if (bytes.length === 0) {
    return (
      <p className="p-3 text-xs text-muted-foreground/60 italic">The value is empty: zero bytes.</p>
    )
  }
  return (
    <div
      ref={attach}
      onScroll={onScroll}
      tabIndex={0}
      role="group"
      aria-label="The value's bytes"
      className="min-h-0 flex-1 overflow-auto px-3 py-2 focus-ring-inset"
    >
      <div style={{ height }} className="relative">
        <div style={{ transform: `translateY(${offset}px)` }}>
          {lines.map((line) => (
            <div
              key={line.offset}
              className="flex h-5 items-center gap-4 font-mono text-xs whitespace-pre"
            >
              <span className="text-muted-foreground/60">{line.offset}</span>
              <span className="w-[49ch] shrink-0">{line.hex}</span>
              <span className="text-muted-foreground">{line.text}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
