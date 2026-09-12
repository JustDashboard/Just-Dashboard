"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { ChevronRight, CornerDownLeft, FolderClosed, Home, Pencil } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { rowReveal } from "@/components/icon-action"

const clean = (p: string) => p.replace(/\/+$/, "") || "/"

/**
 * Where you are, and the way to say where you want to be.
 *
 * The breadcrumb answers the first; the second used to be a dialog called
 * "Go to…", which is a modal in front of a text field in front of a page whose
 * whole subject is that path. Clicking the bar — or Ctrl+L, which is the same
 * chord as the browser's own address bar — turns it into that text field in
 * place, with completion on Tab, so typing `/var/www` is as fast here as it is
 * in a shell.
 *
 * The separators are not decoration either: each one lists the folders beside
 * the crumb after it, which is how you get from `/var/www/site-a/public` to
 * `site-b` without walking back up three levels.
 */
export function PathBar({
  path,
  home,
  onNavigate,
  className,
}: {
  path: string
  home?: string
  onNavigate: (path: string) => void
  className?: string
}) {
  const [editing, setEditing] = useState(false)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "l" || !(event.metaKey || event.ctrlKey) || event.shiftKey) {
        return
      }
      event.preventDefault()
      setEditing(true)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const crumbs = useMemo(() => {
    const parts = clean(path).split("/").filter(Boolean)
    const out: { label: string; href: string }[] = []
    let acc = ""
    for (const part of parts) {
      acc += `/${part}`
      out.push({ label: part, href: acc })
    }
    return out
  }, [path])

  if (editing) {
    return (
      <PathInput
        initial={path}
        onCancel={() => setEditing(false)}
        onSubmit={(next) => {
          setEditing(false)
          onNavigate(clean(next))
        }}
        className={className}
      />
    )
  }

  return (
    <div className={cn("group/path flex min-w-0 items-center gap-0.5", className)}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            onClick={() => onNavigate(home ?? "/")}
          >
            <Home className="size-3.5" />
            <span className="sr-only">Home</span>
          </button>
        </TooltipTrigger>
        <TooltipContent>{home ? `Home — ${home}` : "Root"}</TooltipContent>
      </Tooltip>

      <div className="flex min-w-0 flex-1 items-center overflow-x-auto">
        <SiblingMenu dir="/" onNavigate={onNavigate} />
        {crumbs.map((crumb, i) => (
          <div key={crumb.href} className="flex shrink-0 items-center">
            <button
              type="button"
              className={cn(
                "rounded-md px-1.5 py-0.5 text-body transition-colors hover:bg-accent hover:text-accent-foreground",
                i === crumbs.length - 1 && "font-medium",
              )}
              onClick={() => onNavigate(crumb.href)}
            >
              {crumb.label}
            </button>
            <SiblingMenu dir={crumb.href} onNavigate={onNavigate} />
          </div>
        ))}
      </div>

      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            size="icon-xs"
            variant="ghost"
            className={cn("shrink-0 text-muted-foreground", rowReveal("path"))}
            onClick={() => setEditing(true)}
            aria-label="Type a path"
          >
            <Pencil />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Type a path (Ctrl+L)</TooltipContent>
      </Tooltip>
    </div>
  )
}

/** The folders inside one crumb, so a sideways move is one click. */
function SiblingMenu({ dir, onNavigate }: { dir: string; onNavigate: (path: string) => void }) {
  const [entries, setEntries] = useState<FileEntry[]>()
  const [loading, setLoading] = useState(false)

  const load = async () => {
    if (entries || loading) return
    setLoading(true)
    try {
      const listing = await get<{ entries: FileEntry[] }>("/files/list", { path: dir })
      setEntries(listing.entries.filter((e) => e.isDir).slice(0, 100))
    } catch {
      setEntries([])
    } finally {
      setLoading(false)
    }
  }

  return (
    <DropdownMenu onOpenChange={(open) => open && load()}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground/70 transition-colors hover:bg-accent hover:text-accent-foreground"
          aria-label={`Folders in ${dir}`}
        >
          <ChevronRight className="size-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-56 overflow-y-auto">
        {loading && <DropdownMenuItem disabled>Reading…</DropdownMenuItem>}
        {entries?.length === 0 && <DropdownMenuItem disabled>No folders here</DropdownMenuItem>}
        {entries?.map((entry) => (
          <DropdownMenuItem key={entry.path} onSelect={() => onNavigate(entry.path)}>
            <FolderClosed className="size-3.5 fill-primary/20 text-primary" />
            <span className="truncate">{entry.name}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The path as a text field, with completion.
 *
 * Tab completes to the longest common prefix of what matches — the behaviour a
 * shell has, and the one people's hands already know. Arrow keys walk the
 * suggestions, Enter goes, Escape puts the breadcrumb back.
 */
function PathInput({
  initial,
  onSubmit,
  onCancel,
  className,
}: {
  initial: string
  onSubmit: (path: string) => void
  onCancel: () => void
  className?: string
}) {
  const [value, setValue] = useState(initial)
  const [matches, setMatches] = useState<FileEntry[]>([])
  const [highlight, setHighlight] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    // Debounced, because this fires per keystroke and each one is a readdir on
    // the server.
    const timer = setTimeout(() => {
      get<FileEntry[]>("/files/complete", { prefix: value, limit: 40 }, controller.signal)
        .then((entries) => {
          setMatches(entries)
          setHighlight(-1)
        })
        .catch(() => undefined)
    }, 90)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [value])

  const accept = (entry: FileEntry) => {
    setValue(entry.isDir ? `${entry.path}/` : entry.path)
    inputRef.current?.focus()
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    switch (event.key) {
      case "Escape":
        event.preventDefault()
        onCancel()
        break
      case "Enter":
        event.preventDefault()
        if (highlight >= 0 && matches[highlight]) {
          const chosen = matches[highlight]
          if (chosen.isDir) accept(chosen)
          else onSubmit(chosen.path)
        } else {
          onSubmit(value)
        }
        break
      case "ArrowDown":
        event.preventDefault()
        setHighlight((h) => Math.min(matches.length - 1, h + 1))
        break
      case "ArrowUp":
        event.preventDefault()
        setHighlight((h) => Math.max(-1, h - 1))
        break
      case "Tab": {
        event.preventDefault()
        if (matches.length === 0) return
        if (matches.length === 1) {
          accept(matches[0])
          return
        }
        // The longest common prefix, exactly as a shell does it: completing to
        // the first match instead would take you somewhere you did not ask for
        // the moment two folders share a beginning.
        const common = longestCommonPrefix(matches.map((m) => m.name))
        const base = value.endsWith("/") ? value : value.slice(0, value.lastIndexOf("/") + 1)
        if (common) setValue(base + common)
        break
      }
    }
  }

  return (
    <div className={cn("relative min-w-0 flex-1", className)}>
      <Input
        ref={inputRef}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={(e) => {
          // A click on a suggestion must not be read as leaving the field.
          if (!e.relatedTarget?.closest("[data-path-suggestions]")) onCancel()
        }}
        spellCheck={false}
        autoComplete="off"
        placeholder="/var/www"
        className="h-7 pr-16 font-mono text-xs"
      />
      <span className="pointer-events-none absolute top-1/2 right-2 flex -translate-y-1/2 items-center gap-1 text-micro text-muted-foreground">
        Tab completes
        <CornerDownLeft className="size-3" />
      </span>
      {matches.length > 0 && (
        <div
          data-path-suggestions
          className="absolute top-full right-0 left-0 z-50 mt-1 max-h-72 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md"
        >
          {matches.map((entry, i) => (
            <button
              key={entry.path}
              type="button"
              className={cn(
                "flex w-full items-center gap-2 rounded-md px-2 py-1 text-left font-mono text-xs",
                i === highlight ? "bg-accent text-accent-foreground" : "hover:bg-menu-hover",
              )}
              onMouseEnter={() => setHighlight(i)}
              onClick={() => (entry.isDir ? accept(entry) : onSubmit(entry.path))}
            >
              <FolderClosed
                className={cn(
                  "size-3.5 shrink-0",
                  entry.isDir ? "fill-primary/20 text-primary" : "opacity-0",
                )}
              />
              <span className="truncate">{entry.name}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function longestCommonPrefix(names: string[]): string {
  if (names.length === 0) return ""
  let prefix = names[0]
  for (const name of names.slice(1)) {
    let i = 0
    while (i < prefix.length && i < name.length && prefix[i] === name[i]) i++
    prefix = prefix.slice(0, i)
    if (!prefix) break
  }
  return prefix
}
