"use client"

import { useEffect, useMemo, useState } from "react"
import { CornerDownLeft, FolderOpen, MagnifyingGlass, TextFormat } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileFindResult } from "@/lib/types"
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/state"
import { FileIcon } from "@/components/files/file-icon"
import { PaletteModal } from "@/components/modal"
import { PaneFooter } from "@/components/panel"

/**
 * Type three letters, get the file.
 *
 * The page already had a Search dialog and it answers a different question: a
 * literal substring or a regular expression, optionally inside file contents,
 * for when you know what you are looking for. This is the other one, and it is
 * the one people do fifty times a day — half-remembering a name and most of
 * where it lives. `ngxconf` finds nginx.conf; `srcapp` finds src/app.
 *
 * The matching and the ranking are the server's (see files.Find), because it
 * is the only side that can walk the disk. What is here is the part that makes
 * a fuzzy finder usable: it searches as you type, the keyboard never has to
 * leave the input, and the characters that matched are underlined so the
 * ranking is legible rather than magic.
 */
export function QuickOpen({
  open,
  onOpenChange,
  root,
  home,
  onOpenPath,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Where the walk starts — the folder being browsed. */
  root: string
  /** The wider scope to offer, when the folder being browsed is not it. */
  home?: string
  onOpenPath: (path: string, isDir: boolean) => void
}) {
  return (
    <PaletteModal
      open={open}
      onOpenChange={onOpenChange}
      label="Go to a file"
      description={`Fuzzy search for a file or folder under ${root}`}
    >
      {/* Mounted only while open, so every visit starts with an empty box
            rather than yesterday's query and its stale results. */}
      {open && (
        <QuickOpenBody
          root={root}
          home={home}
          onChoose={(path, isDir) => {
            onOpenChange(false)
            onOpenPath(path, isDir)
          }}
        />
      )}
    </PaletteModal>
  )
}

function QuickOpenBody({
  root,
  home,
  onChoose,
}: {
  root: string
  home?: string
  onChoose: (path: string, isDir: boolean) => void
}) {
  const [query, setQuery] = useState("")
  const [hidden, setHidden] = useState(false)
  // The folder being browsed is the right default — it is the thing being
  // worked on — but a finder that can only ever see one folder sends people
  // back to the breadcrumb to get to where the file actually is. Widening is
  // one click, and the footer says which of the two answered.
  const [wide, setWide] = useState(false)
  const scope = wide && home ? home : root
  const [result, setResult] = useState<FileFindResult>()
  const [busy, setBusy] = useState(false)
  const [cursor, setCursor] = useState(0)

  const ready = query.trim().length >= 2

  useEffect(() => {
    if (!ready) return
    const controller = new AbortController()
    // Debounced rather than per keystroke: each run is a bounded walk of the
    // tree, and a request per character would have four of them racing.
    const timer = setTimeout(() => {
      setBusy(true)
      get<FileFindResult>(
        "/files/find",
        { path: scope, q: query, hidden, limit: 60 },
        controller.signal,
      )
        .then((res) => {
          setResult(res)
          setCursor(0)
        })
        .catch(() => undefined)
        .finally(() => !controller.signal.aborted && setBusy(false))
    }, 130)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [query, hidden, scope, ready])

  // Derived rather than cleared: a query too short to search shows nothing,
  // and the last answer stays in hand for when it grows back.
  const hits = ready ? (result?.hits ?? []) : []

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowDown") {
      event.preventDefault()
      setCursor((c) => Math.min(hits.length - 1, c + 1))
    } else if (event.key === "ArrowUp") {
      event.preventDefault()
      setCursor((c) => Math.max(0, c - 1))
    } else if (event.key === "Enter" && hits[cursor]) {
      event.preventDefault()
      onChoose(hits[cursor].path, hits[cursor].isDir)
    }
  }

  return (
    // min-w-0 is load-bearing: the dialog is a grid, a grid item's minimum
    // width is its content, and one long absolute path in the footer would
    // otherwise push the whole panel wider than the dialog it sits in.
    <div className="min-w-0" onKeyDown={onKeyDown}>
      <div className="flex items-center gap-2 border-b border-hairline px-3">
        <MagnifyingGlass className="size-4 shrink-0 text-muted-foreground" />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Part of a name, in order — ngxconf, srcapp, dockcomp"
          spellCheck={false}
          className="h-12 flex-1 bg-transparent text-sm focus-ring placeholder:text-muted-foreground"
        />
        {busy && <Spinner className="size-4 text-muted-foreground" />}
      </div>

      <div className="max-h-[52vh] overflow-y-auto p-1">
        {hits.map((hit, i) => (
          <button
            key={hit.path}
            className={cn(
              "flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left",
              i === cursor ? "bg-accent text-accent-foreground" : "hover:bg-menu-hover",
            )}
            onMouseEnter={() => setCursor(i)}
            onClick={() => onChoose(hit.path, hit.isDir)}
          >
            <FileIcon
              entry={{ name: hit.name, path: hit.path, isDir: hit.isDir, isSymlink: false }}
              className="size-5"
            />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body">
                <Highlighted text={hit.name} matches={hit.matches} />
              </span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {hit.dir === "." ? "" : `${hit.dir}/`}
              </span>
            </span>
            <span className="numeric shrink-0 text-hint text-muted-foreground">
              {hit.isDir ? relativeTime(hit.modified) : bytes(hit.size)}
            </span>
          </button>
        ))}

        {!busy && ready && hits.length === 0 && (
          <p className="py-8 text-center text-body text-muted-foreground">
            Nothing under this folder matches those letters, in that order.
          </p>
        )}
        {!ready && (
          <p className="flex items-center justify-center gap-2 py-8 text-center text-body text-muted-foreground">
            <TextFormat className="size-4" />
            Type at least two characters. They need only appear in order.
          </p>
        )}
      </div>

      <PaneFooter className="justify-between gap-3 px-3 text-hint text-muted-foreground">
        <span className="flex min-w-0 items-center gap-1.5">
          <FolderOpen className="size-3 shrink-0" />
          <span className="truncate font-mono">{scope}</span>
          {home && home !== root && (
            <button
              type="button"
              className="shrink-0 rounded-sm border border-hairline px-1.5 py-0.5 hover:bg-accent hover:text-accent-foreground"
              onClick={() => setWide((v) => !v)}
            >
              {wide ? "search this folder" : "search from home"}
            </button>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-3">
          <label className="flex items-center gap-1.5">
            <Checkbox checked={hidden} onCheckedChange={(v) => setHidden(v === true)} />
            Hidden files
          </label>
          {ready && result?.truncated && (
            <span className="numeric text-warning">
              partial — {result.visited.toLocaleString()} looked at
            </span>
          )}
          {ready && result && !result.truncated && <span>{result.elapsedMs} ms</span>}
          <span className="flex items-center gap-1">
            <CornerDownLeft className="size-3" />
            open
          </span>
        </span>
      </PaneFooter>
    </div>
  )
}

/**
 * The matched characters, underlined.
 *
 * The offsets are UTF-16 code units — the server counts them that way for
 * exactly this reason — so slicing the string by them is correct even for a
 * name carrying an accent or an emoji.
 */
function Highlighted({ text, matches }: { text: string; matches?: number[] }) {
  const segments = useMemo(() => {
    if (!matches || matches.length === 0) return [{ text, hit: false }]
    const hits = new Set(matches)
    const out: { text: string; hit: boolean }[] = []
    let start = 0
    let current = hits.has(0)
    // Sliced by code unit, and the offsets always land on a character
    // boundary because the server converted them from rune indices — so a
    // surrogate pair is never cut in half into a replacement glyph.
    for (let i = 1; i <= text.length; i++) {
      const hit = hits.has(i)
      if (i === text.length || hit !== current) {
        out.push({ text: text.slice(start, i), hit: current })
        start = i
        current = hit
      }
    }
    return out
  }, [text, matches])

  return (
    <>
      {segments.map((segment, i) =>
        segment.hit ? (
          <b key={i} className="font-semibold text-primary underline underline-offset-2">
            {segment.text}
          </b>
        ) : (
          <span key={i}>{segment.text}</span>
        ),
      )}
    </>
  )
}
