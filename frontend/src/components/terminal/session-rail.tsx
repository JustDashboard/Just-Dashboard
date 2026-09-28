"use client"

import { useMemo, useState } from "react"
import { Cross, Plus, SidebarLeftClose } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { TerminalActivity, TerminalWorkspace } from "@/lib/types"
import { sessionLabel, sessionProgram, sessionWorking } from "@/lib/terminal-activity"
import { SearchInput } from "@/components/page"
import { ActivityMark, ProgramMark } from "@/components/terminal/activity-mark"
import { Button } from "@/components/ui/button"
import { IconAction, rowReveal } from "@/components/icon-action"
import { Pane, PaneHeader } from "@/components/panel"

type RowHandlers = {
  activeId: string | null
  /** What each visited window's socket last said it was doing, by window id. */
  activity: Record<string, TerminalActivity>
  /** The sessions with a window whose socket in this browser has dropped. */
  disconnected: ReadonlySet<string>
  onSelect: (session: TerminalWorkspace) => void
  onClose: (session: TerminalWorkspace) => void
}

/**
 * The sessions, newest first, and nothing to do to one but open it or close
 * it. Folders, renaming and pinning were a filing system for shells that
 * name themselves: a row already says what its session is doing.
 */
export function SessionRail({
  sessions,
  activeId,
  activity,
  disconnected,
  onSelect,
  onClose,
  onNew,
  onHide,
  className,
}: RowHandlers & {
  sessions: TerminalWorkspace[]
  onNew: () => void
  /** Given when the rail covers the emulator rather than sitting beside it,
   *  so the only way back to the shell is not a session pick. */
  onHide?: () => void
  className?: string
}) {
  const [filter, setFilter] = useState("")
  const matches = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const found = needle
      ? sessions.filter((session) =>
          [sessionLabel(session, activity), session.title, session.cwd].some((value) =>
            value?.toLowerCase().includes(needle),
          ),
        )
      : sessions
    return [...found].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  }, [sessions, filter, activity])
  const rows = { activeId, activity, disconnected, onSelect, onClose }

  return (
    <Pane flush aria-label="Terminal sessions" className={cn("w-full shrink-0", className)}>
      <PaneHeader className="h-10 gap-1 py-0 pl-3">
        <span className="text-body font-medium">Sessions</span>
        <span className="flex-1" />
        <IconAction label="New session" className="size-7" onClick={onNew}>
          <Plus />
        </IconAction>
        {onHide && (
          <IconAction label="Hide this panel" className="size-7" onClick={onHide}>
            <SidebarLeftClose />
          </IconAction>
        )}
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
      <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-1.5">
        {matches.map((session) => (
          <SessionRow key={session.id} session={session} {...rows} />
        ))}
        {matches.length === 0 && filter && (
          <p className="px-2 py-4 text-center text-xs text-muted-foreground">
            No session matches <span className="font-medium text-foreground">{filter}</span>.
          </p>
        )}
      </div>
    </Pane>
  )
}

function SessionRow({
  session,
  activeId,
  activity,
  disconnected,
  onSelect,
  onClose,
}: RowHandlers & { session: TerminalWorkspace }) {
  const active = activeId === session.id
  // One line: what the session is called, which — unless somebody named it —
  // is what its current window is doing. The directory used to sit under the
  // title, but the title now carries it while the shell is at a prompt, and
  // while a program runs its name is the more useful line. The dot at the end
  // says whether a window in here is working, is idle or has lost its
  // connection — the reason to look at a row you are not in.
  const label = sessionLabel(session, activity)
  const working = sessionWorking(session, activity)
  const dropped = disconnected.has(session.id)

  return (
    <div
      data-session={session.id}
      data-active={active || undefined}
      data-working={working || undefined}
      data-disconnected={dropped || undefined}
      className={cn(
        "group flex min-w-0 items-center gap-1 rounded-md py-2 pr-1 pl-3 transition-colors",
        active ? "bg-accent" : "hover:bg-row-hover",
      )}
    >
      <button
        onClick={() => onSelect(session)}
        title={session.cwd}
        className="flex min-w-0 flex-1 items-center gap-1.5 text-left focus-ring-inset"
      >
        <ProgramMark process={sessionProgram(session, activity)} />
        <span
          className={cn("min-w-0 flex-1 truncate text-body leading-tight", active && "font-medium")}
        >
          {label}
        </span>
        <ActivityMark working={working} disconnected={dropped} className="pr-1" />
      </button>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Close ${label}`}
        className={cn("size-6 shrink-0 text-muted-foreground hover:text-destructive", rowReveal())}
        onClick={() => onClose(session)}
      >
        <Cross className="size-3.5" />
      </Button>
    </div>
  )
}
