"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { CornerDownLeft, Cross, FolderOpen, MagnifyingGlass } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry, FileFindResult } from "@/lib/types"
import { Checkbox } from "@/components/ui/checkbox"
import { Button } from "@/components/ui/button"
import { ErrorState, Spinner } from "@/components/state"
import { FileIcon } from "@/components/files/file-icon"
import { PaletteModal } from "@/components/modal"
import { PaneFooter } from "@/components/panel"
import { tabClasses } from "@/components/tabs"
import { localFileMatches, mergeFileMatches, type FileSearchHit } from "./search"

export type FileSearchMode = "names" | "content"

export function QuickOpen({
  open,
  onOpenChange,
  root,
  home,
  entries = [],
  initialMode = "names",
  onOpenPath,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  root: string
  home?: string
  entries?: FileEntry[]
  initialMode?: FileSearchMode
  onOpenPath: (path: string, isDir: boolean, line?: number) => void
}) {
  return (
    <PaletteModal
      open={open}
      onOpenChange={onOpenChange}
      label="Find files"
      description="Find a file by name or search its contents. Arrow keys select, Enter opens."
    >
      {open && (
        <SearchBody
          root={root}
          home={home}
          entries={entries}
          initialMode={initialMode}
          onChoose={(hit) => {
            onOpenChange(false)
            onOpenPath(hit.path, hit.isDir, hit.line)
          }}
        />
      )}
    </PaletteModal>
  )
}

function SearchBody({
  root,
  home,
  entries,
  initialMode,
  onChoose,
}: {
  root: string
  home?: string
  entries: FileEntry[]
  initialMode: FileSearchMode
  onChoose: (hit: FileSearchHit) => void
}) {
  const [query, setQuery] = useState("")
  const [mode, setMode] = useState(initialMode)
  const [hidden, setHidden] = useState(false)
  const [wide, setWide] = useState(false)
  const [regex, setRegex] = useState(false)
  const [matchCase, setMatchCase] = useState(false)
  const [run, setRun] = useState(0)
  const [selected, setSelected] = useState<string>()
  const [pending, setPending] = useState<string>()
  const [answer, setAnswer] = useState<{
    key: string
    hits: FileSearchHit[]
    truncated?: boolean
    elapsedMs?: number
    unreadable?: number
    error?: Error
  }>()
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const id = useId()
  const scope = wide && home ? home : root
  const trimmed = query.trim()
  const ready = trimmed.length >= (mode === "names" ? 2 : 1)
  const key = JSON.stringify([scope, trimmed, mode, hidden, regex, matchCase, run])
  const current = answer?.key === key ? answer : undefined
  const busy = ready && (!current || pending === key)
  const local = useMemo(
    () =>
      mode === "names" && scope === root
        ? localFileMatches(
            entries.filter((e) => hidden || !e.name.startsWith(".")),
            query,
          )
        : [],
    [entries, hidden, mode, query, root, scope],
  )
  const hits =
    mode === "names" ? mergeFileMatches(local, current?.hits ?? []) : (current?.hits ?? [])
  const cursor = Math.max(
    0,
    hits.findIndex((hit) => hit.path + ":" + (hit.line ?? 0) === selected),
  )

  useEffect(() => {
    if (!ready) return
    const controller = new AbortController()
    const timer = setTimeout(
      async () => {
        setPending(key)
        try {
          if (mode === "names") {
            const result = await get<FileFindResult>(
              "/files/find",
              {
                path: scope,
                q: trimmed,
                hidden,
                limit: 60,
              },
              controller.signal,
            )
            if (!controller.signal.aborted) setAnswer({ key, ...result })
          } else {
            const result = await get<{
              hits: FileSearchHit[]
              truncated: boolean
              elapsedMs: number
              unreadable: number
            }>(
              "/files/search",
              {
                path: scope,
                q: trimmed,
                content: true,
                regex,
                ignoreCase: !matchCase,
                hidden,
                limit: 200,
                detailed: true,
              },
              controller.signal,
            )
            if (!controller.signal.aborted) setAnswer({ key, ...result })
          }
        } catch (error) {
          if (!controller.signal.aborted) setAnswer({ key, hits: [], error: error as Error })
        } finally {
          if (!controller.signal.aborted) setPending(undefined)
        }
      },
      run ? 0 : mode === "names" ? 130 : 400,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [key, matchCase, mode, ready, regex, hidden, run, scope, trimmed])

  useEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" })
  }, [cursor, hits.length])

  return (
    <div className="min-w-0">
      <div className="flex items-center gap-2 border-b border-hairline px-3">
        <MagnifyingGlass aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        <input
          ref={input}
          autoFocus
          role="combobox"
          aria-label={mode === "names" ? "Find by name" : "Search file contents"}
          aria-expanded
          aria-autocomplete="list"
          aria-controls={id + "-results"}
          aria-activedescendant={hits[cursor] ? id + "-" + cursor : undefined}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setRun(0)
          }}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault()
              const next =
                (cursor + (event.key === "ArrowDown" ? 1 : -1) + hits.length) % hits.length
              if (hits[next]) setSelected(hits[next].path + ":" + (hits[next].line ?? 0))
            } else if (event.key === "Enter") {
              event.preventDefault()
              if (hits[cursor]) onChoose(hits[cursor])
              else if (ready) setRun((v) => v + 1)
            }
          }}
          placeholder={mode === "names" ? "Find a file or folder…" : "Find text inside files…"}
          spellCheck={false}
          className="h-12 min-w-0 flex-1 bg-transparent text-body focus-ring-inset placeholder:text-muted-foreground"
        />
        {busy && <Spinner className="size-4 text-muted-foreground" />}
        {query && (
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Clear search"
            onClick={() => {
              setQuery("")
              input.current?.focus()
            }}
          >
            <Cross />
          </Button>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline px-3">
        <div role="group" aria-label="Search mode" className="flex gap-3">
          {(["names", "content"] as const).map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={mode === value}
              className={tabClasses(mode === value, "h-9")}
              onClick={() => {
                setMode(value)
                setRun(0)
                input.current?.focus()
              }}
            >
              {value === "names" ? "Names" : "Contents"}
            </button>
          ))}
        </div>
        {mode === "content" && (
          <div className="flex items-center gap-1">
            <Button
              size="icon-xs"
              variant={matchCase ? "secondary" : "ghost"}
              aria-label="Match case"
              aria-pressed={matchCase}
              onClick={() => setMatchCase((v) => !v)}
            >
              Aa
            </Button>
            <Button
              size="icon-xs"
              variant={regex ? "secondary" : "ghost"}
              aria-label="Regular expression"
              aria-pressed={regex}
              onClick={() => setRegex((v) => !v)}
            >
              .*
            </Button>
          </div>
        )}
      </div>

      <div
        ref={list}
        id={id + "-results"}
        role="listbox"
        aria-label="Search results"
        aria-busy={busy}
        className="max-h-[min(24rem,48vh)] overflow-y-auto p-1"
      >
        {current?.error && (
          <ErrorState error={current.error} onRetry={() => setRun((v) => v + 1)} className="m-3" />
        )}
        {hits.map((hit, i) => (
          <button
            key={hit.path + ":" + (hit.line ?? 0)}
            id={id + "-" + i}
            type="button"
            role="option"
            aria-selected={i === cursor}
            tabIndex={-1}
            className={cn(
              "flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left transition-colors",
              i === cursor ? "bg-accent text-accent-foreground" : "hover:bg-menu-hover",
            )}
            onMouseMove={() => setSelected(hit.path + ":" + (hit.line ?? 0))}
            onClick={() => onChoose(hit)}
          >
            <FileIcon entry={{ ...hit, isSymlink: false }} className="mt-0.5 size-6 shrink-0" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body">
                <Highlighted text={hit.name} matches={hit.matches} />
              </span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {hit.path.startsWith(scope + "/") ? hit.path.slice(scope.length + 1) : hit.path}
                {hit.line ? ":" + hit.line : ""}
              </span>
              {hit.snippet && (
                <span className="mt-1 block truncate font-mono text-body text-muted-foreground">
                  <Snippet
                    text={hit.snippet}
                    ranges={hit.ranges}
                    query={trimmed}
                    regex={regex}
                    matchCase={matchCase}
                  />
                </span>
              )}
            </span>
            <span className="numeric shrink-0 pt-0.5 text-hint text-muted-foreground">
              {hit.isDir ? "Folder" : hit.size === undefined ? "" : bytes(hit.size)}
            </span>
          </button>
        ))}
        {!current?.error && hits.length === 0 && (
          <div className="flex min-h-40 flex-col items-center justify-center gap-2 px-5 text-center text-body text-muted-foreground">
            {busy ? (
              <>
                <Spinner className="size-5" />
                <span>Searching this folder…</span>
              </>
            ) : ready ? (
              <>
                <span className="font-medium text-foreground">No matches for “{trimmed}”</span>
                <span>Try fewer words, include hidden files, or search from home.</span>
              </>
            ) : (
              <>
                <span className="font-medium text-foreground">
                  {mode === "names" ? "Find your next file" : "Search inside your files"}
                </span>
                <span>
                  {mode === "names"
                    ? "Type a few letters of its name or path."
                    : "Type text or a regular expression to find matching lines."}
                </span>
              </>
            )}
          </div>
        )}
      </div>

      <PaneFooter className="flex-wrap justify-between gap-x-3 gap-y-2 px-3 py-2 text-hint text-muted-foreground">
        <div className="flex min-w-0 items-center gap-1.5">
          <FolderOpen aria-hidden className="size-3 shrink-0" />
          <span className="max-w-48 truncate font-mono" title={scope}>
            {scope}
          </span>
          {home && home !== root && (
            <Button
              size="xs"
              variant="ghost"
              onClick={() => {
                setWide((v) => !v)
                setRun(0)
              }}
            >
              {wide ? "This folder" : "From home"}
            </Button>
          )}
        </div>
        <label className="flex items-center gap-1.5">
          <Checkbox checked={hidden} onCheckedChange={(v) => setHidden(v === true)} />
          Hidden files
        </label>
        <span role="status" aria-live="polite" className={cn(current?.truncated && "text-warning")}>
          {current?.truncated
            ? "Partial results · narrow your search"
            : busy
              ? "Searching…"
              : hits.length + (mode === "content" ? " matches" : " files")}
        </span>
        <span className="flex items-center gap-1">
          <CornerDownLeft aria-hidden className="size-3" />
          Open
        </span>
        {!!current?.unreadable && (
          <span className="w-full text-warning">
            {current.unreadable} entries could not be read.
          </span>
        )}
      </PaneFooter>
    </div>
  )
}

function Highlighted({ text, matches }: { text: string; matches?: number[] }) {
  const positions = new Set(matches)
  let offset = 0
  return (
    <>
      {Array.from(text).map((char) => {
        const index = offset
        offset += char.length
        return positions.has(index) ? (
          <b key={index} className="font-semibold text-brand">
            {char}
          </b>
        ) : (
          <span key={index}>{char}</span>
        )
      })}
    </>
  )
}

function Snippet({
  text,
  query,
  regex,
  matchCase,
  ranges,
}: {
  text: string
  query: string
  regex: boolean
  matchCase: boolean
  ranges?: [number, number][]
}) {
  if (ranges?.length) {
    const parts: React.ReactNode[] = []
    let offset = 0
    ranges.forEach(([start, end], index) => {
      parts.push(<span key={"plain" + index}>{text.slice(offset, start)}</span>)
      parts.push(
        <mark key={"match" + index} className="bg-wash-brand text-brand">
          {text.slice(start, end)}
        </mark>,
      )
      offset = end
    })
    parts.push(<span key="end">{text.slice(offset)}</span>)
    return <>{parts}</>
  }
  if (regex || !query) return <>{text}</>
  const index = (matchCase ? text : text.toLocaleLowerCase()).indexOf(
    matchCase ? query : query.toLocaleLowerCase(),
  )
  if (index < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, index)}
      <mark className="bg-wash-brand text-brand">{text.slice(index, index + query.length)}</mark>
      {text.slice(index + query.length)}
    </>
  )
}
