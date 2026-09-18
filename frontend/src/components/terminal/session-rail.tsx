"use client"

import { useMemo, useState } from "react"
import {
  ChevronDown,
  Cross,
  FolderClosed,
  FolderPlus,
  MoreHorizontal,
  Pencil,
  Pin,
  Plus,
  Trash,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { TerminalActivity, TerminalFolder, TerminalWorkspace } from "@/lib/types"
import { sessionActivity, sessionLabel, useFinished } from "@/lib/terminal-activity"
import { useViewState } from "@/lib/view-state"
import { SearchInput } from "@/components/page"
import { ActivityMark } from "@/components/terminal/activity-mark"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { IconAction, rowReveal } from "@/components/icon-action"
import { Pane, PaneHeader } from "@/components/panel"

type RowHandlers = {
  activeId: string | null
  folders: TerminalFolder[]
  /** What each visited window's socket last said it was doing, by window id. */
  activity: Record<string, TerminalActivity>
  onSelect: (session: TerminalWorkspace) => void
  onRename: (session: TerminalWorkspace, title: string) => void
  onTogglePinned: (session: TerminalWorkspace) => void
  onSetFolder: (id: string, folder: string) => void
  onClose: (session: TerminalWorkspace) => void
  onNew: (folder?: string) => void
}

export function SessionRail({
  sessions,
  folders,
  activeId,
  activity,
  onSelect,
  onRename,
  onTogglePinned,
  onSetFolder,
  onClose,
  onNew,
  onCreateFolder,
  onUpdateFolder,
  onDeleteFolder,
  className,
}: {
  sessions: TerminalWorkspace[]
  folders: TerminalFolder[]
  activeId: string | null
  activity: Record<string, TerminalActivity>
  onSelect: (session: TerminalWorkspace) => void
  onRename: (session: TerminalWorkspace, title: string) => void
  onTogglePinned: (session: TerminalWorkspace) => void
  onSetFolder: (id: string, folder: string) => void
  onClose: (session: TerminalWorkspace) => void
  onNew: (folder?: string) => void
  onCreateFolder: (name: string) => void
  onUpdateFolder: (name: string, next: { name?: string }) => void
  onDeleteFolder: (folder: TerminalFolder) => void
  className?: string
}) {
  const [collapsed, setCollapsed] = useViewState<Record<string, boolean>>(
    "terminal.folders.collapsed",
    {},
  )
  const [creatingFolder, setCreatingFolder] = useState(false)
  const [filter, setFilter] = useState("")
  const matches = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    if (!needle) return sessions
    return sessions.filter((session) =>
      [sessionLabel(session, activity), session.title, session.cwd, session.folder].some((value) =>
        value?.toLowerCase().includes(needle),
      ),
    )
  }, [sessions, filter, activity])
  const groups = useMemo(() => {
    const byFolder = new Map<string, TerminalWorkspace[]>()
    for (const session of matches) {
      const key = session.folder || ""
      byFolder.set(key, [...(byFolder.get(key) ?? []), session])
    }
    const order = (items: TerminalWorkspace[]) =>
      [...items].sort((a, b) => {
        if (a.favourite !== b.favourite) return a.favourite ? -1 : 1
        return b.createdAt.localeCompare(a.createdAt)
      })
    return {
      folders: folders.map((folder) => ({ folder, items: order(byFolder.get(folder.name) ?? []) })),
      unfiled: order(byFolder.get("") ?? []),
    }
  }, [matches, folders])
  const rows = {
    activeId,
    folders,
    activity,
    onSelect,
    onRename,
    onTogglePinned,
    onSetFolder,
    onClose,
    onNew,
  }

  return (
    <Pane flush aria-label="Terminal sessions" className={cn("w-full shrink-0", className)}>
      <PaneHeader className="gap-1 pl-3">
        <span className="text-body font-medium">Sessions</span>
        <span className="flex-1" />
        <IconAction label="New folder" className="size-7" onClick={() => setCreatingFolder(true)}>
          <FolderPlus />
        </IconAction>
        <IconAction label="New session" className="size-7" onClick={() => onNew()}>
          <Plus />
        </IconAction>
      </PaneHeader>
      {/* A filter over one session is a box with nothing to do. It appears
          once the list is long enough that scanning it stops being faster
          than typing. */}
      {(sessions.length > 5 || filter) && (
        <div className="border-b border-hairline px-2 py-1.5">
          <SearchInput
            dense
            value={filter}
            spellCheck={false}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter sessions"
            containerClassName="sm:w-full"
          />
        </div>
      )}
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto p-1.5">
        {creatingFolder && (
          <InlineEdit
            placeholder="Folder name"
            value=""
            onCommit={(value) => {
              if (value) onCreateFolder(value)
              setCreatingFolder(false)
            }}
            onCancel={() => setCreatingFolder(false)}
          />
        )}
        {groups.folders.map(({ folder, items }) => (
          <FolderGroup
            key={folder.name}
            folder={folder}
            items={items}
            collapsed={Boolean(collapsed[folder.name])}
            onToggle={() =>
              setCollapsed((value) => ({ ...value, [folder.name]: !value[folder.name] }))
            }
            onUpdateFolder={onUpdateFolder}
            onDeleteFolder={onDeleteFolder}
            {...rows}
          />
        ))}
        {groups.unfiled.length > 0 && (
          <div data-folder="">
            {folders.length > 0 && (
              <p className="px-2 py-1 text-hint font-medium tracking-wide text-muted-foreground uppercase">
                All sessions
              </p>
            )}
            <div className="space-y-0.5">
              {groups.unfiled.map((session) => (
                <SessionRow key={session.id} session={session} {...rows} />
              ))}
            </div>
          </div>
        )}
        {matches.length === 0 && filter && (
          <p className="px-2 py-4 text-center text-xs text-muted-foreground">
            No session matches <span className="font-medium text-foreground">{filter}</span>.
          </p>
        )}
      </div>
    </Pane>
  )
}

function FolderGroup({
  folder,
  items,
  collapsed,
  onToggle,
  onUpdateFolder,
  onDeleteFolder,
  ...rows
}: RowHandlers & {
  folder: TerminalFolder
  items: TerminalWorkspace[]
  collapsed: boolean
  onToggle: () => void
  onUpdateFolder: (name: string, next: { name?: string }) => void
  onDeleteFolder: (folder: TerminalFolder) => void
}) {
  const [renaming, setRenaming] = useState(false)
  if (renaming)
    return (
      <InlineEdit
        placeholder="Folder name"
        value={folder.name}
        onCommit={(value) => {
          if (value && value !== folder.name) onUpdateFolder(folder.name, { name: value })
          setRenaming(false)
        }}
        onCancel={() => setRenaming(false)}
      />
    )

  return (
    <div className="min-w-0" data-folder={folder.name}>
      <div className="group/folder flex items-center gap-1 px-1 py-0.5">
        <button
          className="flex min-w-0 flex-1 items-center gap-1.5 rounded-md py-1 text-left focus-ring hover:text-foreground"
          onClick={onToggle}
        >
          <ChevronDown
            className={cn(
              "size-3 shrink-0 text-muted-foreground transition-transform",
              collapsed && "-rotate-90",
            )}
          />
          <span className="truncate text-hint font-medium tracking-wide text-muted-foreground uppercase">
            {folder.name}
          </span>
        </button>
        <span className={cn("flex shrink-0", rowReveal("folder"))}>
          <IconAction
            label={`New session in ${folder.name}`}
            className="size-6"
            onClick={() => rows.onNew(folder.name)}
          >
            <Plus />
          </IconAction>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`More for ${folder.name}`}
                className="size-6"
              >
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-44">
              <DropdownMenuItem className="gap-2 text-xs" onSelect={() => setRenaming(true)}>
                <Pencil className="size-3.5" /> Rename folder
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                className="gap-2 text-xs"
                onSelect={() => onDeleteFolder(folder)}
              >
                <Trash className="size-3.5" /> Delete folder
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </span>
      </div>
      {!collapsed && items.length > 0 && (
        <div className="mt-0.5 space-y-0.5 pl-3">
          {items.map((session) => (
            <SessionRow key={session.id} session={session} {...rows} />
          ))}
        </div>
      )}
    </div>
  )
}

function SessionRow({
  session,
  activeId,
  folders,
  activity,
  onSelect,
  onRename,
  onTogglePinned,
  onSetFolder,
  onClose,
}: RowHandlers & { session: TerminalWorkspace }) {
  const [renaming, setRenaming] = useState(false)
  const active = activeId === session.id
  // One line: what the session is called, which — unless somebody named it —
  // is what its current window is doing. The directory used to sit under the
  // title, but the title now carries it while the shell is at a prompt, and
  // while a program runs its name is the more useful line. The mark at the
  // end says a window in here is still working, or has just stopped — the
  // reason to look at a row you are not in.
  const label = sessionLabel(session, activity)
  const { working, finishedAt } = sessionActivity(session, activity)
  const finished = useFinished(session.id, active, finishedAt)
  if (renaming)
    return (
      <InlineEdit
        placeholder="Name this session"
        value={session.named ? session.title : label}
        onCommit={(value) => {
          if (value) onRename(session, value)
          setRenaming(false)
        }}
        onCancel={() => setRenaming(false)}
      />
    )

  return (
    <div
      data-session={session.id}
      data-active={active || undefined}
      data-working={working || undefined}
      data-finished={finished || undefined}
      className={cn(
        "group flex min-w-0 items-center gap-1 rounded-md py-2 pr-1 pl-3 transition-colors",
        active ? "bg-accent" : "hover:bg-row-hover",
      )}
    >
      {/* No terminal glyph on the row: every row in this list is a terminal,
          so the icon said nothing the column had not already said. */}
      <button
        onClick={() => onSelect(session)}
        title={session.cwd}
        className="flex min-w-0 flex-1 items-center gap-1.5 text-left focus-ring-inset"
      >
        {session.favourite && <Pin className="size-2.5 shrink-0 text-muted-foreground" />}
        <span
          className={cn("min-w-0 flex-1 truncate text-body leading-tight", active && "font-medium")}
        >
          {label}
        </span>
        <ActivityMark working={working} finished={finished} className="pr-1" />
      </button>
      {/* Closing is the one thing done often enough to earn its own control,
          so it sits on the card rather than two clicks into the menu. The
          menu keeps what is done rarely: rename, pin, refile. */}
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Close ${label}`}
        className={cn("size-6 shrink-0 text-muted-foreground hover:text-destructive", rowReveal())}
        onClick={() => onClose(session)}
      >
        <Cross className="size-3.5" />
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`More for ${label}`}
            className={cn("size-6 shrink-0", rowReveal())}
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuItem className="gap-2 text-xs" onSelect={() => setRenaming(true)}>
            <Pencil className="size-3.5" /> Rename
          </DropdownMenuItem>
          <DropdownMenuItem className="gap-2 text-xs" onSelect={() => onTogglePinned(session)}>
            <Pin className="size-3.5" /> {session.favourite ? "Unpin" : "Pin"}
          </DropdownMenuItem>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger className="gap-2 text-xs">
              <FolderClosed className="size-3.5" /> Move to
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-44">
              <DropdownMenuItem className="text-xs" onSelect={() => onSetFolder(session.id, "")}>
                All sessions
              </DropdownMenuItem>
              {folders.map((folder) => (
                <DropdownMenuItem
                  key={folder.name}
                  className="text-xs"
                  disabled={folder.name === session.folder}
                  onSelect={() => onSetFolder(session.id, folder.name)}
                >
                  {folder.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function InlineEdit({
  value,
  placeholder,
  onCommit,
  onCancel,
}: {
  value: string
  placeholder: string
  onCommit: (value: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(value)
  return (
    <Input
      autoFocus
      value={draft}
      placeholder={placeholder}
      className="h-8 text-xs"
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => onCommit(draft.trim())}
      onKeyDown={(event) => {
        if (event.key === "Enter") onCommit(draft.trim())
        if (event.key === "Escape") onCancel()
      }}
    />
  )
}
