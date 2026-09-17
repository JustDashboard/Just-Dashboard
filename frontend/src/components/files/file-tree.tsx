"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ChevronRight,
  Download,
  FolderOpen,
  FolderPlus,
  MoreHorizontal,
  PlusSquareSmall,
  RefreshClockwise,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, post } from "@/lib/api"
import { truncateMiddle } from "@/lib/format"
import { cn } from "@/lib/utils"
import { describeChange, gitLetter, gitStyle, gitTone } from "@/lib/git-status"
import { FileIcon } from "@/components/files/file-icon"
import type { FileEntry, FileListing, GitFileChange } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/state"
import { RowActions } from "@/components/icon-action"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** What the inline confirm surface needs; the tools panel renders it. */
export type ConfirmRequest = {
  title: string
  body?: React.ReactNode
  confirmLabel?: string
  danger?: boolean
  run: () => Promise<void> | void
}

/**
 * A lazy, git-aware file tree rooted at one directory.
 *
 * The dialogs here are deliberately inline — a create row is an input woven
 * into the tree, delete goes through the panel's own confirm — because this
 * panel is expected to be open while the terminal is in the browser's real
 * fullscreen, where anything portalled to `document.body` renders outside the
 * fullscreen element and is simply invisible. Tooltips are the exception and
 * no longer have to be: `usePortalContainer` mounts them inside whatever is
 * fullscreen, so an icon button here can carry a real label rather than a
 * native `title` the browser sits on for a second before showing.
 *
 * Each folder fetches its children the first time it is opened and caches them;
 * `reload` drops one folder's cache so a change (a new file, a delete, a git
 * operation that rewrote the tree) is picked up without collapsing the rest.
 */
export function FileTree({
  root,
  statusMap,
  canWrite,
  canDelete,
  activeFile,
  activeDir,
  onOpenFile,
  onNavigate,
  onConfirm,
  onChanged,
  onOpenInFiles,
}: {
  root: string
  /** Absolute path → its git status, so a changed file can carry a badge. */
  statusMap: Record<string, GitFileChange>
  canWrite: boolean
  canDelete: boolean
  activeFile?: string
  /** Highlight this folder as the one currently open elsewhere on the page. */
  activeDir?: string
  onOpenFile: (path: string) => void
  /** When set, clicking a folder also reports it — the Files page navigates its
   *  main listing there while the tree keeps expanding as usual. */
  onNavigate?: (path: string) => void
  onConfirm: (req: ConfirmRequest) => void
  onChanged: () => void
  onOpenInFiles?: (path: string) => void
}) {
  const [showHidden, setShowHidden] = useViewState("files.tree.hidden", false)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([root]))
  const [children, setChildren] = useState<Record<string, FileEntry[]>>({})
  const [loading, setLoading] = useState<Set<string>>(new Set())
  const [failed, setFailed] = useState<Record<string, string>>({})
  // The folder an inline create-row is attached to, and what it makes.
  const [creating, setCreating] = useState<{ parent: string; kind: "file" | "folder" } | null>(null)

  const load = useCallback(
    async (path: string) => {
      setLoading((s) => new Set(s).add(path))
      try {
        const listing = await get<FileListing>("/files/list", { path, hidden: showHidden })
        setChildren((c) => ({ ...c, [path]: sortEntries(listing.entries) }))
        setFailed((f) => {
          if (!f[path]) return f
          const next = { ...f }
          delete next[path]
          return next
        })
      } catch (err) {
        setFailed((f) => ({ ...f, [path]: String(err) }))
      } finally {
        setLoading((s) => {
          const next = new Set(s)
          next.delete(path)
          return next
        })
      }
    },
    [showHidden],
  )

  // Load the root, and re-load every already-open folder, whenever the hidden
  // toggle changes — otherwise a "show hidden" flip would take effect at the
  // top level while the open subfolders kept their old contents. The tree is
  // keyed on `root` by its caller, so a new root arrives as a fresh mount with
  // only the root open; that mount lands here too and loads it.
  useEffect(() => {
    void load(root)
    for (const path of expanded) if (path !== root) void load(path)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [root, showHidden])

  const reload = useCallback(
    (path: string) => {
      if (expanded.has(path) || path === root) void load(path)
    },
    [expanded, root, load],
  )

  /**
   * Opens a folder without the possibility of closing it.
   *
   * A double-click on a folder is two clicks, and two toggles is where it
   * started — so the second half of the gesture opens rather than flips.
   */
  const expand = (path: string) => {
    setExpanded((s) => {
      if (s.has(path)) return s
      if (!children[path]) void load(path)
      return new Set(s).add(path)
    })
  }

  const toggle = (path: string) => {
    setExpanded((s) => {
      const next = new Set(s)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
        if (!children[path]) void load(path)
      }
      return next
    })
  }

  const submitCreate = async (name: string) => {
    if (!creating) return
    const trimmed = name.trim()
    if (!trimmed) {
      setCreating(null)
      return
    }
    const target = `${creating.parent.replace(/\/$/, "")}/${trimmed}`
    try {
      if (creating.kind === "folder") await post("/files/mkdir", { path: target })
      else await post("/files/touch", { path: target })
      notify.success(`Created ${trimmed}`)
      const parent = creating.parent
      setCreating(null)
      setExpanded((s) => new Set(s).add(parent))
      await load(parent)
      onChanged()
      if (creating.kind === "file") onOpenFile(target)
    } catch (err) {
      notify.error("Could not create that", err)
    }
  }

  const requestDelete = (entry: FileEntry, parent: string) =>
    onConfirm({
      title: `Delete ${entry.name}`,
      danger: true,
      confirmLabel: "Delete",
      body: (
        <>
          <span className="font-mono break-all">{entry.path}</span> is deleted permanently
          {entry.isDir ? ", along with everything inside it." : "."}
        </>
      ),
      run: async () => {
        await del("/files/delete", {
          confirm: entry.name,
          query: { path: entry.path, recursive: entry.isDir },
        })
        notify.success(`Deleted ${entry.name}`)
        await load(parent)
        onChanged()
      },
    })

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* The root, and what can be done at it. Two verbs inline — a new file
          and a refresh are the ones pressed all day — and the rest behind one
          menu with a word each: five icon buttons and a checkbox in a strip
          336px wide was the strip. The path keeps its last segments, because
          the leaf is the part that says where you are. */}
      <div className="flex h-9 shrink-0 items-center gap-0.5 border-b border-hairline pr-1.5 pl-3">
        <span
          className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground"
          title={root}
        >
          {truncateMiddle(root, 36)}
        </span>
        {canWrite && (
          <TreeButton
            label="New file in this folder"
            onClick={() => {
              setExpanded((s) => new Set(s).add(root))
              setCreating({ parent: root, kind: "file" })
            }}
          >
            <PlusSquareSmall className="size-3.5" />
          </TreeButton>
        )}
        <TreeButton label="Refresh" onClick={() => reload(root)}>
          <RefreshClockwise className="size-3.5" />
        </TreeButton>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  aria-label="More for this folder"
                  className="size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground"
                >
                  <MoreHorizontal className="size-3.5" />
                </Button>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent>Hidden files, new folder, open in Files</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuCheckboxItem
              className="text-xs"
              checked={showHidden}
              onCheckedChange={(v) => setShowHidden(v === true)}
            >
              Show hidden files
            </DropdownMenuCheckboxItem>
            {canWrite && (
              <DropdownMenuItem
                className="gap-2 text-xs"
                onSelect={() => {
                  setExpanded((s) => new Set(s).add(root))
                  setCreating({ parent: root, kind: "folder" })
                }}
              >
                <FolderPlus className="size-3.5" /> New folder here
              </DropdownMenuItem>
            )}
            {onOpenInFiles && (
              <DropdownMenuItem className="gap-2 text-xs" onSelect={() => onOpenInFiles(root)}>
                <FolderOpen className="size-3.5" /> Open in Files
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="min-h-0 flex-1 overflow-auto py-1">
        <TreeLevel
          parent={root}
          depth={0}
          entries={children[root]}
          loadingThis={loading.has(root)}
          error={failed[root]}
          expanded={expanded}
          entriesByPath={children}
          loading={loading}
          failed={failed}
          statusMap={statusMap}
          activeFile={activeFile}
          activeDir={activeDir}
          canWrite={canWrite}
          canDelete={canDelete}
          creating={creating}
          onToggle={toggle}
          onExpand={expand}
          onOpenFile={onOpenFile}
          onNavigate={onNavigate}
          onStartCreate={(parent, kind) => {
            setExpanded((s) => new Set(s).add(parent))
            setCreating({ parent, kind })
          }}
          onCancelCreate={() => setCreating(null)}
          onSubmitCreate={submitCreate}
          onDelete={requestDelete}
        />
      </div>
    </div>
  )
}

type LevelProps = {
  parent: string
  depth: number
  entries: FileEntry[] | undefined
  loadingThis: boolean
  error?: string
  expanded: Set<string>
  entriesByPath: Record<string, FileEntry[]>
  loading: Set<string>
  failed: Record<string, string>
  statusMap: Record<string, GitFileChange>
  activeFile?: string
  activeDir?: string
  canWrite: boolean
  canDelete: boolean
  creating: { parent: string; kind: "file" | "folder" } | null
  onToggle: (path: string) => void
  onExpand: (path: string) => void
  onOpenFile: (path: string) => void
  onNavigate?: (path: string) => void
  onStartCreate: (parent: string, kind: "file" | "folder") => void
  onCancelCreate: () => void
  onSubmitCreate: (name: string) => void
  onDelete: (entry: FileEntry, parent: string) => void
}

/** One directory's contents, plus the create-row that belongs to it. */
function TreeLevel(props: LevelProps) {
  const { parent, depth, entries, loadingThis, error, creating } = props
  const indent = 8 + depth * 14

  return (
    <>
      {creating?.parent === parent && (
        <CreateRow
          indent={indent + 16}
          kind={creating.kind}
          onCancel={props.onCancelCreate}
          onSubmit={props.onSubmitCreate}
        />
      )}
      {loadingThis && !entries && (
        <div
          className="flex items-center gap-2 px-2 py-1 text-hint text-muted-foreground"
          style={{ paddingLeft: indent + 16 }}
        >
          <Spinner className="size-3" />
          Loading…
        </div>
      )}
      {error && (
        <div
          className="truncate px-2 py-1 text-hint text-destructive"
          style={{ paddingLeft: indent + 16 }}
          title={error}
        >
          {error}
        </div>
      )}
      {entries?.length === 0 && creating?.parent !== parent && (
        <div
          className="px-2 py-1 text-hint text-muted-foreground italic"
          style={{ paddingLeft: indent + 16 }}
        >
          empty
        </div>
      )}
      {entries?.map((entry) => (
        <TreeNode key={entry.path} entry={entry} {...props} />
      ))}
    </>
  )
}

function TreeNode({ entry, depth, ...props }: LevelProps & { entry: FileEntry }) {
  const { expanded, entriesByPath, statusMap, activeFile, activeDir } = props
  const indent = 8 + depth * 14
  const isOpen = expanded.has(entry.path)
  const status = statusMap[entry.path]
  const tone = status ? gitTone(status) : null
  const dirHasChanges =
    entry.isDir && Object.keys(statusMap).some((p) => p.startsWith(entry.path + "/"))

  const onFolderClick = () => {
    props.onToggle(entry.path)
    props.onNavigate?.(entry.path)
  }

  /**
   * What a double-click means, which depends entirely on what was clicked.
   *
   * A folder opens; it is emphatically *not* handed to `onOpenFile`. That is
   * the file reader, and asking the server to read a directory comes back as
   * "path is a directory" — a red row in the tree for the most ordinary
   * gesture in any file manager, and one that looked like a stale-path bug
   * because the click that followed it worked.
   */
  const onDoubleClick = () => {
    if (!entry.isDir) return
    props.onExpand(entry.path)
    props.onNavigate?.(entry.path)
  }

  return (
    <>
      <div
        className={cn(
          "group relative flex items-center gap-1 py-[3px] pr-1 text-body hover:bg-row-hover",
          // The tint and the rule down the edge are the same fact as the
          // letter, said in a way that survives being scanned rather than
          // read: a wall of filenames with one green line in it answers
          // "what did I touch" without any of them being looked at.
          tone &&
            "bg-(--git-tint) before:absolute before:inset-y-0 before:left-0 before:w-[2px] before:bg-(--git-edge) before:content-['']",
          activeFile === entry.path && "bg-accent",
          entry.isDir && activeDir === entry.path && "bg-accent",
        )}
        style={{ paddingLeft: indent, ...(tone ? gitStyle(tone) : null) }}
      >
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-1 text-left"
          onClick={() => (entry.isDir ? onFolderClick() : props.onOpenFile(entry.path))}
          onDoubleClick={onDoubleClick}
          title={entry.path}
        >
          <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground">
            {entry.isDir ? (
              <ChevronRight
                className={cn("size-3.5 transition-transform", isOpen && "rotate-90")}
              />
            ) : null}
          </span>
          {/* The same kind icons the listing and the tiles draw, so a file is
              recognisable wherever this page shows it rather than reverting to
              a grey sheet of paper in the rail. */}
          <FileIcon entry={entry} open={isOpen} className="size-4" />
          <span
            className={cn(
              "truncate",
              tone && "text-(--git-colour)",
              status?.label === "deleted" && "line-through",
            )}
          >
            {entry.name}
          </span>
          {status ? (
            <StatusMark change={status} />
          ) : dirHasChanges ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="ml-auto size-1.5 shrink-0 rounded-full bg-(--git-modified)" />
              </TooltipTrigger>
              <TooltipContent>Contains changes</TooltipContent>
            </Tooltip>
          ) : null}
        </button>

        <RowActions className="gap-0">
          {entry.isDir && props.canWrite && (
            <>
              <TreeButton
                label="New file here"
                onClick={() => props.onStartCreate(entry.path, "file")}
              >
                <PlusSquareSmall className="size-3" />
              </TreeButton>
              <TreeButton
                label="New folder here"
                onClick={() => props.onStartCreate(entry.path, "folder")}
              >
                <FolderPlus className="size-3" />
              </TreeButton>
            </>
          )}
          {!entry.isDir && (
            <TreeButton label="Download" asChild>
              <a href={downloadUrl("/files/download", { path: entry.path })} download>
                <Download className="size-3" />
              </a>
            </TreeButton>
          )}
          {props.canDelete && (
            <TreeButton
              label="Delete"
              className="text-destructive"
              onClick={() => props.onDelete(entry, props.parent)}
            >
              <Trash className="size-3" />
            </TreeButton>
          )}
        </RowActions>
      </div>

      {entry.isDir && isOpen && (
        <TreeLevel
          {...props}
          parent={entry.path}
          depth={depth + 1}
          entries={entriesByPath[entry.path]}
          loadingThis={props.loading.has(entry.path)}
          error={props.failed[entry.path]}
        />
      )}
    </>
  )
}

function CreateRow({
  indent,
  kind,
  onCancel,
  onSubmit,
}: {
  indent: number
  kind: "file" | "folder"
  onCancel: () => void
  onSubmit: (name: string) => void
}) {
  const [value, setValue] = useState("")
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <div className="flex items-center gap-1 py-[3px] pr-1" style={{ paddingLeft: indent }}>
      {kind === "folder" ? (
        <FolderPlus className="size-4 shrink-0 text-primary" />
      ) : (
        <PlusSquareSmall className="size-4 shrink-0 text-muted-foreground" />
      )}
      <Input
        ref={ref}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onSubmit(value)
          if (e.key === "Escape") onCancel()
        }}
        onBlur={() => (value.trim() ? onSubmit(value) : onCancel())}
        placeholder={kind === "folder" ? "folder name" : "file name"}
        className="h-6 flex-1 font-mono text-xs"
      />
    </div>
  )
}

/**
 * The one-letter git mark on a changed file.
 *
 * The colour says *what* changed and the ring says whether it is staged —
 * previously the colour said "staged" and the kind of change was left to the
 * letter, which meant a deleted file and an added one were the same shade of
 * green and the only difference was a character most people do not read.
 */
function StatusMark({ change }: { change: GitFileChange }) {
  const tone = gitTone(change)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn(
            "ml-auto shrink-0 rounded-sm px-1 font-mono text-micro leading-none",
            "bg-(--git-tint) text-(--git-colour)",
            change.staged && "ring-1 ring-(--git-edge)",
          )}
          style={gitStyle(tone)}
        >
          {gitLetter(change)}
        </span>
      </TooltipTrigger>
      <TooltipContent>{describeChange(change)}</TooltipContent>
    </Tooltip>
  )
}

function TreeButton({
  label,
  onClick,
  asChild,
  className,
  children,
}: {
  label: string
  onClick?: () => void
  asChild?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          asChild={asChild}
          aria-label={label}
          className={cn(
            "size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground",
            className,
          )}
          onClick={onClick}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function sortEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" })
  })
}
